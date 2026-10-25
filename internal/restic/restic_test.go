package restic

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mazetly/mazetly-rescue/internal/docker"
	"github.com/mazetly/mazetly-rescue/internal/fakedocker"
)

// tail is what follows the image: the restic command line.
func tail(t *testing.T, c fakedocker.Call) []string {
	t.Helper()
	i := slices.Index(c.Args, docker.ResticImage)
	if i < 0 {
		t.Fatalf("restic image not in %v", c.Args)
	}
	return c.Args[i+1:]
}

// before is what precedes the image: docker's own options.
func before(t *testing.T, c fakedocker.Call) string {
	t.Helper()
	return strings.Join(c.Args[:slices.Index(c.Args, docker.ResticImage)], " ")
}

func TestLocal(t *testing.T) {
	for location, want := range map[string]bool{
		"/srv/repo":                    true,
		"repo":                         true,
		"s3:https://s3.example/b/i":    false,
		"sftp:user@host:/srv/restic":   false,
		"rest:https://host:8000/repo":  false,
		"rclone:remote:path/to/restic": false,
		"azure:container:/path":        false,
		"swift:container:/path":        false,
		"gs:bucket:/path":              false,
		"b2:bucket:path":               false,
	} {
		if got := (Repo{Location: location}).local(); got != want {
			t.Errorf("%s: local() = %v, want %v", location, got, want)
		}
	}
}

func TestCommandKeepsThePasswordOffTheCommandLine(t *testing.T) {
	fake := fakedocker.Install(t)
	t.Setenv("AWS_ACCESS_KEY_ID", "id")
	t.Setenv("UNRELATED", "x")
	dir := t.TempDir()
	r := Repo{Location: dir, Password: "s3cret"}
	if err := r.run([]string{"/a:/b"}, "check"); err != nil {
		t.Fatal(err)
	}
	c := fake.Last()
	if strings.Contains(c.Line(), "s3cret") {
		t.Errorf("the password is on the command line: %s", c.Line())
	}
	if c.Env["RESTIC_PASSWORD"] != "s3cret" || c.Env["RESTIC_REPOSITORY"] != "/repo" {
		t.Errorf("environment: RESTIC_PASSWORD=%q RESTIC_REPOSITORY=%q", c.Env["RESTIC_PASSWORD"], c.Env["RESTIC_REPOSITORY"])
	}
	opts := before(t, c)
	for _, want := range []string{"-v " + dir + ":/repo", "-e RESTIC_PASSWORD", "-e RESTIC_REPOSITORY", "-v /a:/b", "-e AWS_ACCESS_KEY_ID"} {
		if !strings.Contains(opts, want) {
			t.Errorf("%q missing from %q", want, opts)
		}
	}
	if strings.Contains(opts, "UNRELATED") {
		t.Errorf("an unrelated variable is passed: %q", opts)
	}
	if got := tail(t, c); !slices.Equal(got, []string{"check"}) {
		t.Errorf("restic args: %v", got)
	}
}

func TestRemoteRepositoryIsNotMounted(t *testing.T) {
	fake := fakedocker.Install(t)
	r := Repo{Location: "s3:https://s3.example/bucket/app", Password: "p"}
	if err := r.run(nil, "snapshots"); err != nil {
		t.Fatal(err)
	}
	c := fake.Last()
	if strings.Contains(before(t, c), ":/repo") {
		t.Errorf("a remote repository is mounted: %s", c.Line())
	}
	if c.Env["RESTIC_REPOSITORY"] != r.Location {
		t.Errorf("RESTIC_REPOSITORY = %q", c.Env["RESTIC_REPOSITORY"])
	}
}

func TestInitCreatesALocalRepository(t *testing.T) {
	fake := fakedocker.Install(t)
	dir := filepath.Join(t.TempDir(), "new")
	if err := (Repo{Location: dir, Password: "p"}).Init(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("repository directory is %o, want 700", perm)
	}
	if got := tail(t, fake.Last()); !slices.Equal(got, []string{"init"}) {
		t.Errorf("restic args: %v", got)
	}
}

func TestInitFromAndCopyFromPassBothRepositories(t *testing.T) {
	fake := fakedocker.Install(t)
	src := Repo{Location: "s3:https://s3.example/mazetly/app", Password: "mazetly"}
	dst := Repo{Location: filepath.Join(t.TempDir(), "copy"), Password: "customer"}
	if err := dst.InitFrom(src); err != nil {
		t.Fatal(err)
	}
	if err := dst.CopyFrom(src); err != nil {
		t.Fatal(err)
	}
	calls := fake.Calls()
	if len(calls) != 2 {
		t.Fatalf("%d calls, want 2", len(calls))
	}
	if got := tail(t, calls[0]); !slices.Equal(got, []string{"init", "--copy-chunker-params"}) {
		t.Errorf("init args: %v", got)
	}
	if got := tail(t, calls[1]); !slices.Equal(got, []string{"copy", "--tag", kitTag}) {
		t.Errorf("copy args: %v", got)
	}
	for _, c := range calls {
		if c.Env["RESTIC_FROM_PASSWORD"] != "mazetly" || c.Env["RESTIC_FROM_REPOSITORY"] != src.Location {
			t.Errorf("source not passed: %v", c.Env)
		}
		if c.Env["RESTIC_PASSWORD"] != "customer" || c.Env["RESTIC_REPOSITORY"] != "/repo" {
			t.Errorf("destination not passed: %v", c.Env)
		}
		if !strings.Contains(before(t, c), "-e RESTIC_FROM_PASSWORD") {
			t.Errorf("RESTIC_FROM_PASSWORD not handed to the container: %s", c.Line())
		}
	}
}

func TestLocalSourceIsMountedAtFrom(t *testing.T) {
	fake := fakedocker.Install(t)
	src := Repo{Location: t.TempDir(), Password: "a"}
	dst := Repo{Location: "s3:https://s3.example/b/c", Password: "b"}
	if err := dst.CopyFrom(src); err != nil {
		t.Fatal(err)
	}
	c := fake.Last()
	if !strings.Contains(before(t, c), "-v "+src.Location+":/from") || c.Env["RESTIC_FROM_REPOSITORY"] != "/from" {
		t.Errorf("local source not mounted at /from: %s", c.Line())
	}
}

func TestAddKeyReadsThePasswordFromStdin(t *testing.T) {
	fake := fakedocker.Install(t)
	if err := (Repo{Location: "s3:x", Password: "p"}).AddKey("new-key", "customer", "mazetly-rescue"); err != nil {
		t.Fatal(err)
	}
	c := fake.Last()
	if c.Stdin != "new-key" {
		t.Errorf("stdin = %q", c.Stdin)
	}
	if strings.Contains(c.Line(), "new-key") {
		t.Errorf("the new key is on the command line: %s", c.Line())
	}
	want := []string{"key", "add", "--new-password-file", "/dev/stdin", "--user", "customer", "--host", "mazetly-rescue"}
	if got := tail(t, c); !slices.Equal(got, want) {
		t.Errorf("restic args: %v", got)
	}
}

func TestBackup(t *testing.T) {
	fake := fakedocker.Install(t)
	if err := (Repo{Location: "s3:x", Password: "p"}).Backup("/srv/app", []string{"/srv/app/data/app.db"}); err != nil {
		t.Fatal(err)
	}
	c := fake.Last()
	if !strings.Contains(before(t, c), "-v /srv/app:/srv/app:ro") {
		t.Errorf("instance not mounted read-only at its own path: %s", c.Line())
	}
	want := []string{"backup", "--tag", kitTag, "--exclude", "/srv/app/data/app.db", "/srv/app"}
	if got := tail(t, c); !slices.Equal(got, want) {
		t.Errorf("restic args: %v", got)
	}
}

func TestSnapshots(t *testing.T) {
	fake := fakedocker.Install(t)
	fake.Reply("snapshots", `[{"id":"abc123","short_id":"abc1","time":"2026-10-25T04:00:00Z","hostname":"h","paths":["/srv/app"]}]`, "", 0)
	snapshots, err := (Repo{Location: "s3:x", Password: "p"}).Snapshots()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 1 || snapshots[0].ID != "abc123" || snapshots[0].Paths[0] != "/srv/app" || snapshots[0].Time.Year() != 2026 {
		t.Errorf("snapshots: %+v", snapshots)
	}
	want := []string{"snapshots", noLock, "--json", "--tag", kitTag}
	if got := tail(t, fake.Last()); !slices.Equal(got, want) {
		t.Errorf("restic args: %v", got)
	}
}

func TestSnapshotsErrors(t *testing.T) {
	fake := fakedocker.Install(t)
	fake.Reply("snapshots", "not json", "", 0)
	if _, err := (Repo{Location: "s3:x", Password: "p"}).Snapshots(); err == nil {
		t.Error("unreadable output accepted")
	}
	fake = fakedocker.Install(t)
	fake.Reply("snapshots", "", "wrong password or no key found", 1)
	_, err := (Repo{Location: "s3:x", Password: "p"}).Snapshots()
	if err == nil || !strings.Contains(err.Error(), "wrong password") {
		t.Errorf("restic's error not reported: %v", err)
	}
}

func TestRestore(t *testing.T) {
	fake := fakedocker.Install(t)
	s := Snapshot{ID: "abc123", Paths: []string{"/srv/app"}}
	if err := (Repo{Location: "s3:x", Password: "p"}).Restore(s, "/tmp/target"); err != nil {
		t.Fatal(err)
	}
	c := fake.Last()
	if !strings.Contains(before(t, c), "-v /tmp/target:/restore") {
		t.Errorf("target not mounted: %s", c.Line())
	}
	want := []string{"restore", "abc123:/srv/app", noLock, "--target", "/restore"}
	if got := tail(t, c); !slices.Equal(got, want) {
		t.Errorf("restic args: %v", got)
	}
}

func TestDump(t *testing.T) {
	fake := fakedocker.Install(t)
	fake.Reply("manifest.json", `{"format":"mazetly-kit/0"}`, "", 0)
	fake.Reply("missing", "", "path not found", 1)
	r := Repo{Location: "s3:x", Password: "p"}
	s := Snapshot{ID: "abc123", Paths: []string{"/srv/app"}}
	var out bytes.Buffer
	if err := r.Dump(s, "manifest.json", &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != `{"format":"mazetly-kit/0"}` {
		t.Errorf("dumped %q", out.String())
	}
	if got, want := tail(t, fake.Last()), []string{"dump", noLock, "abc123", "/srv/app/manifest.json"}; !slices.Equal(got, want) {
		t.Errorf("restic args: %v", got)
	}
	err := r.Dump(s, "missing", &out)
	if err == nil || !strings.Contains(err.Error(), "path not found") {
		t.Errorf("restic's error not reported: %v", err)
	}
}
