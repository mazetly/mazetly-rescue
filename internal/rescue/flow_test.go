package rescue

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mazetly/mazetly-rescue/internal/fakedocker"
	"github.com/mazetly/mazetly-rescue/internal/restic"
)

// The flows below run against a fake docker: what they check is the sequence
// of commands and what they do with the answers. test/roundtrip.sh runs the
// same flows against real containers.

const dump = "CREATE TABLE monitors (id int);\n"

var testRepo = restic.Repo{Location: "s3:https://s3.example/bucket/app", Password: "customer"}

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// config is what `docker compose config` prints for the demo recipe. With
// sqlite, a third service keeps its database under dir/data.
func config(dir string, sqlite bool) string {
	c := `name: kit
services:
  app:
    image: demo/app:1.0
    ports:
      - target: 80
        published: "8080"
    x-mazetly:
      hooks:
        pre_backup:
          - echo maintenance on
        post_backup:
          - echo maintenance off
        post_restore:
          - user: www-data
            run: echo restored
  db:
    image: postgres:18
    x-mazetly:
      database:
        engine: postgres
`
	if sqlite {
		c += `  kuma:
    image: louislam/uptime-kuma:2
    volumes:
      - type: bind
        source: ` + dir + `/data
        target: /app/data
    x-mazetly:
      database:
        engine: sqlite
        path: /app/data/kuma.db
`
	}
	return c + `x-mazetly:
  app: demo
  version:
    service: app
    label: org.opencontainers.image.version
`
}

// literal is the recipe as written in compose.yaml, before pinning.
const literal = `services:
  app:
    image: demo/app:1.0
  db:
    image: postgres:18
  kuma:
    image: louislam/uptime-kuma:2
`

var snapshotJSON = `[{"id":"abc123","short_id":"abc1","time":"` + time.Now().UTC().Add(-time.Hour).Format(time.RFC3339) +
	`","hostname":"mazetly-1","paths":["/srv/app"]}]`

func manifestJSON(t *testing.T, sqlSum string, sqlite bool) string {
	t.Helper()
	m := Manifest{Format: Format, App: "demo", AppVersion: "1.0",
		Images:    map[string]string{"app": "demo/app@sha256:aa", "db": "postgres@sha256:bb"},
		Databases: []Export{{Service: "db", Engine: "postgres", File: "db/db.sql", SHA256: sqlSum}}}
	if sqlite {
		m.Images["kuma"] = "louislam/uptime-kuma@sha256:cc"
		m.Databases = append(m.Databases, Export{Service: "kuma", Engine: "sqlite", File: "db/kuma.sqlite", SHA256: sum("sqlite"), Path: "/app/data/kuma.db"})
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// writeKit writes the kit a snapshot holds into dir.
func writeKit(t *testing.T, dir, manifest string) {
	t.Helper()
	for name, content := range map[string]string{
		"compose.yaml": literal, "manifest.json": manifest, "db/db.sql": dump, "db/kuma.sqlite": "sqlite", ".env": "SECRET=x\n",
	} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// restoreFake answers like a repository holding one snapshot of kit, and an
// instance that starts and runs version.
func restoreFake(t *testing.T, kit, configDir string, sqlite bool, version string) *fakedocker.Fake {
	t.Helper()
	fake := fakedocker.Install(t)
	fake.Reply(" snapshots ", snapshotJSON, "", 0)
	// restic restore writes the kit into the directory mounted at /restore.
	fake.Exec(" restore abc123:/srv/app ", `prev=
for a; do
	if [ "$prev" = -v ]; then case "$a" in *:/restore) cp -R "`+kit+`/." "${a%:/restore}/" ;; esac; fi
	prev=$a
done`)
	fake.Reply(" config", config(configDir, sqlite), "", 0)
	fake.Reply(" ps -q app", "cid-app\n", "", 0)
	fake.Reply("{{index .Config.Labels", version+"\n", "", 0)
	return fake
}

func calls(fake *fakedocker.Fake) string {
	var lines []string
	for _, c := range fake.Calls() {
		lines = append(lines, c.Line())
	}
	return strings.Join(lines, "\n")
}

func TestCheck(t *testing.T) {
	fake := fakedocker.Install(t)
	fake.Reply(" snapshots ", snapshotJSON, "", 0)
	fake.Reply("/srv/app/manifest.json", manifestJSON(t, sum(dump), false), "", 0)
	fake.Reply("/srv/app/db/db.sql", dump, "", 0)
	if err := Check(testRepo, 0); err != nil {
		t.Fatal(err)
	}
	if err := Check(testRepo, 26*time.Hour); err != nil {
		t.Errorf("a backup an hour old is too old: %v", err)
	}
	if err := Check(testRepo, time.Minute); !errors.Is(err, ErrTooOld) {
		t.Errorf("a backup an hour old with --max-age 1m: %v", err)
	}
}

func TestCheckFailures(t *testing.T) {
	fake := fakedocker.Install(t)
	fake.Reply(" snapshots ", snapshotJSON, "", 0)
	fake.Reply("/srv/app/manifest.json", manifestJSON(t, sum("something else"), false), "", 0)
	fake.Reply("/srv/app/db/db.sql", dump, "", 0)
	if err := Check(testRepo, 0); err == nil || !strings.Contains(err.Error(), "db/db.sql") {
		t.Errorf("a corrupted export: %v", err)
	}

	fake = fakedocker.Install(t)
	fake.Reply(" snapshots ", "[]", "", 0)
	if err := Check(testRepo, 0); err == nil {
		t.Error("an empty repository passes")
	}

	fake = fakedocker.Install(t)
	fake.Reply(" snapshots ", snapshotJSON, "", 0)
	fake.Reply("/srv/app/manifest.json", `{"format":"mazetly-kit/9"}`, "", 0)
	if err := Check(testRepo, 0); err == nil {
		t.Error("a kit of an unknown format passes")
	}
}

func TestPickSnapshot(t *testing.T) {
	fake := fakedocker.Install(t)
	fake.Reply(" snapshots ", `[{"id":"aaa111","paths":["/a"]},{"id":"bbb222","paths":["/b"]}]`, "", 0)
	for id, want := range map[string]string{"": "bbb222", "latest": "bbb222", "aaa": "aaa111"} {
		s, err := pickSnapshot(testRepo, id)
		if err != nil || s.ID != want {
			t.Errorf("%q: got %q, %v; want %q", id, s.ID, err, want)
		}
	}
	if _, err := pickSnapshot(testRepo, "ccc"); err == nil {
		t.Error("an unknown snapshot found")
	}
}

func TestCopyWithInit(t *testing.T) {
	fake := fakedocker.Install(t)
	fake.Reply(" snapshots ", snapshotJSON, "", 0)
	dst := restic.Repo{Location: filepath.Join(t.TempDir(), "copy"), Password: "own"}
	if err := Copy(testRepo, dst, true, "customer-key"); err != nil {
		t.Fatal(err)
	}
	all := fake.Calls()
	var steps []string
	for _, c := range all {
		for _, step := range []string{" init --copy-chunker-params", " key add ", " copy --tag ", " snapshots "} {
			if strings.Contains(" "+c.Line()+" ", step) {
				steps = append(steps, strings.TrimSpace(step))
			}
		}
	}
	if got := strings.Join(steps, ", "); got != "init --copy-chunker-params, key add, copy --tag, snapshots" {
		t.Errorf("steps: %s", got)
	}
	if all[1].Stdin != "customer-key" {
		t.Errorf("customer key not added: stdin %q", all[1].Stdin)
	}

	fake = fakedocker.Install(t)
	fake.Reply(" snapshots ", snapshotJSON, "", 0)
	if err := Copy(testRepo, dst, false, ""); err != nil {
		t.Fatal(err)
	}
	if got := calls(fake); strings.Contains(got, " init") || strings.Contains(got, " key add") {
		t.Errorf("without --init, the destination is created anyway:\n%s", got)
	}
}

func TestRestore(t *testing.T) {
	kit := t.TempDir()
	writeKit(t, kit, manifestJSON(t, sum(dump), true))
	target := filepath.Join(t.TempDir(), "app")
	fake := restoreFake(t, kit, target, true, "1.0")
	if err := Restore(testRepo, target, RestoreOptions{Yes: true}); err != nil {
		t.Fatalf("%v\ncalls:\n%s", err, calls(fake))
	}
	pinned, err := os.ReadFile(filepath.Join(target, "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"demo/app@sha256:aa", "postgres@sha256:bb", "louislam/uptime-kuma@sha256:cc"} {
		if !strings.Contains(string(pinned), want) {
			t.Errorf("compose.yaml not pinned to %s:\n%s", want, pinned)
		}
	}
	if _, err := os.Stat(filepath.Join(target, "compose.override.yaml")); err == nil {
		t.Error("a real restore isolated")
	}
	got := calls(fake)
	// In order: the database alone, its import, then everything, then hooks.
	order := []string{"up -d --wait --wait-timeout 600 db", "exec -T db sh -c exec psql", "up -d --wait --wait-timeout 600\n",
		"exec -T -u www-data app sh -c echo restored", "rm -rf /kit/db"}
	at := 0
	for _, want := range order {
		i := strings.Index(got[at:]+"\n", want)
		if i < 0 {
			t.Fatalf("%q missing, or out of order, in:\n%s", want, got)
		}
		at += i
	}
	for _, c := range fake.Calls() {
		if strings.Contains(c.Line(), "exec psql") && c.Stdin != dump {
			t.Errorf("the dump is not fed to psql: %q", c.Stdin)
		}
		if strings.Contains(c.Line(), `cp "/kit/$1" "/kit/$2"`) && !strings.HasSuffix(c.Line(), "db/kuma.sqlite data/kuma.db") {
			t.Errorf("SQLite file copied as %s", c.Line())
		}
	}
}

func TestRestoreRefuses(t *testing.T) {
	kit := t.TempDir()
	writeKit(t, kit, manifestJSON(t, sum(dump), false))

	t.Run("another version", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "app")
		restoreFake(t, kit, target, false, "2.0")
		if err := Restore(testRepo, target, RestoreOptions{Yes: true}); err == nil || !strings.Contains(err.Error(), "2.0") {
			t.Errorf("an application running 2.0 instead of 1.0: %v", err)
		}
	})
	t.Run("a corrupted export", func(t *testing.T) {
		bad := t.TempDir()
		writeKit(t, bad, manifestJSON(t, sum("something else"), false))
		target := filepath.Join(t.TempDir(), "app")
		fake := restoreFake(t, bad, target, false, "1.0")
		if err := Restore(testRepo, target, RestoreOptions{Yes: true}); err == nil {
			t.Error("restored")
		}
		if got := calls(fake); strings.Contains(got, " up ") {
			t.Errorf("started anyway:\n%s", got)
		}
	})
	t.Run("a target in use", func(t *testing.T) {
		target := t.TempDir()
		if err := os.WriteFile(filepath.Join(target, "x"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		fake := restoreFake(t, kit, target, false, "1.0")
		if err := Restore(testRepo, target, RestoreOptions{Yes: true}); err == nil {
			t.Error("restored over existing files")
		}
		if len(fake.Calls()) != 0 {
			t.Errorf("docker called:\n%s", calls(fake))
		}
	})
}

func TestVerify(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	kit := t.TempDir()
	writeKit(t, kit, manifestJSON(t, sum(dump), false))
	fake := restoreFake(t, kit, "", false, "1.0")
	if err := Verify(testRepo, "latest", false); err != nil {
		t.Fatalf("%v\ncalls:\n%s", err, calls(fake))
	}
	got := calls(fake)
	if !strings.Contains(got, "down --volumes --remove-orphans") {
		t.Errorf("the test instance is not removed:\n%s", got)
	}
	var override []byte
	matches, _ := filepath.Glob(filepath.Join(os.Getenv("TMPDIR"), "mazetly-rescue-verify-*", "compose.override.yaml"))
	if len(matches) == 1 {
		override, _ = os.ReadFile(matches[0])
	}
	if !strings.Contains(string(override), "ports: !reset []") {
		t.Errorf("the test instance publishes its ports: %q", override)
	}
}

func TestBackup(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("SECRET=x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	captured := t.TempDir()
	fake := fakedocker.Install(t)
	fake.Reply(" config", config(dir, true), "", 0)
	fake.Reply("pg_dump", dump, "", 0)
	// The SQLite backup writes its copy into the directory mounted at /out,
	// under the name that follows the live file's.
	fake.Exec("sqlite3", `prev= out= n=0
for a; do
	if [ "$prev" = -v ]; then case "$a" in *:/out) out=${a%:/out} ;; esac; fi
	if [ "$n" -gt 0 ]; then n=$((n + 1)); [ "$n" -eq 5 ] && file=$a; fi
	[ "$a" = python ] && n=1
	prev=$a
done
echo sqlite > "$out/$file"`)
	fake.Reply(" ps -q ", "cid\n", "", 0)
	fake.Reply("{{.Image}}", "sha256:local\n", "", 0)
	fake.Reply("{{json .RepoDigests}}", `["demo/app@sha256:aa","postgres@sha256:bb","louislam/uptime-kuma@sha256:cc"]`, "", 0)
	fake.Reply("{{index .Config.Labels", "1.0\n", "", 0)
	// What restic is handed is captured before Backup cleans up.
	fake.Exec(" backup --tag ", `for a; do last=$a; done; cp -R "$last/." "`+captured+`/"`)

	if err := Backup(testRepo, dir); err != nil {
		t.Fatalf("%v\ncalls:\n%s", err, calls(fake))
	}
	m, err := readManifest(captured)
	if err != nil {
		t.Fatal(err)
	}
	if m.App != "demo" || m.AppVersion != "1.0" || m.Images["db"] != "postgres@sha256:bb" || m.Images["app"] != "demo/app@sha256:aa" {
		t.Errorf("manifest: %+v", m)
	}
	if len(m.Databases) != 2 || m.Databases[0].SHA256 != sum(dump) || m.Databases[1].File != "db/kuma.sqlite" {
		t.Errorf("exports: %+v", m.Databases)
	}
	if _, err := os.Stat(filepath.Join(captured, "RESTORE.md")); err != nil {
		t.Error("RESTORE.md not in the kit")
	}
	if _, err := os.Stat(filepath.Join(dir, "db")); !os.IsNotExist(err) {
		t.Errorf("the clear exports outlive the backup: %v", err)
	}
	if info, _ := os.Stat(filepath.Join(dir, ".env")); info.Mode().Perm() != 0o600 {
		t.Errorf(".env left at %o", info.Mode().Perm())
	}
	got := calls(fake)
	live := filepath.Join(dir, "data", "kuma.db")
	for _, want := range []string{"--exclude " + live + " --exclude " + live + "-wal", "sh -c echo maintenance on", "sh -c echo maintenance off"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q missing from:\n%s", want, got)
		}
	}
}

func TestBackupRunsPostBackupOnFailure(t *testing.T) {
	dir := t.TempDir()
	fake := fakedocker.Install(t)
	fake.Reply(" config", config(dir, false), "", 0)
	fake.Reply("pg_dump", "", "connection refused", 1)
	if err := Backup(testRepo, dir); err == nil {
		t.Fatal("a failed export reported as success")
	}
	got := calls(fake)
	if !strings.Contains(got, "echo maintenance off") {
		t.Errorf("post_backup did not run after the failure:\n%s", got)
	}
	if strings.Contains(got, " backup --tag ") {
		t.Errorf("sent to the repository anyway:\n%s", got)
	}
}

func TestRunningImageNeedsARegistryDigest(t *testing.T) {
	dir := t.TempDir()
	fake := fakedocker.Install(t)
	fake.Reply(" config", config(dir, false), "", 0)
	fake.Reply(" ps -q ", "cid\n", "", 0)
	fake.Reply("{{.Image}}", "sha256:local\n", "", 0)
	fake.Reply("{{json .RepoDigests}}", "[]", "", 0)
	if err := Backup(testRepo, dir); err == nil || !strings.Contains(err.Error(), "no registry digest") {
		t.Errorf("a locally built image: %v", err)
	}
}
