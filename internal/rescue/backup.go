package rescue

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mazetly/mazetly-rescue/internal/compose"
	"github.com/mazetly/mazetly-rescue/internal/docker"
	"github.com/mazetly/mazetly-rescue/internal/i18n"
	"github.com/mazetly/mazetly-rescue/internal/restic"
	"github.com/mazetly/mazetly-rescue/kit"
)

var dumpScripts = map[string]string{
	"mariadb":  `exec mariadb-dump --single-transaction --routines -u"$MARIADB_USER" -p"$MARIADB_PASSWORD" "$MARIADB_DATABASE"`,
	"mysql":    `exec mysqldump --single-transaction --routines -u"$MYSQL_USER" -p"$MYSQL_PASSWORD" "$MYSQL_DATABASE"`,
	"postgres": `exec pg_dump -U "$POSTGRES_USER" -d "${POSTGRES_DB:-$POSTGRES_USER}"`,
}

// SQLite's online backup API: a consistent copy while the application writes,
// readable by the tool's user only.
const sqliteBackup = `import os, sqlite3, sys
out = "/out/" + sys.argv[2]
os.umask(0o077)
src = sqlite3.connect("file:/src/" + sys.argv[1] + "?mode=ro", uri=True)
dst = sqlite3.connect(out)
src.backup(dst)
dst.close()
os.chown(out, int(sys.argv[3]), int(sys.argv[4]))`

func step(msg string) { fmt.Printf("\n==> %s\n", msg) }

// Backup exports the databases of the running instance in dir, records what
// ran in manifest.json and sends the whole directory to the repository.
func Backup(repo restic.Repo, dir string) (err error) {
	if dir, err = filepath.Abs(dir); err != nil {
		return err
	}
	p, err := compose.Load(dir)
	if err != nil {
		return err
	}

	// post_backup undoes pre_backup (maintenance mode...): it runs even when
	// the backup fails.
	defer func() {
		step(i18n.T("backup.hooks", "post_backup"))
		if hookErr := runHooks(p, "post_backup"); hookErr != nil && err == nil {
			err = hookErr
		}
	}()
	step(i18n.T("backup.hooks", "pre_backup"))
	if err := runHooks(p, "pre_backup"); err != nil {
		return err
	}

	m := &Manifest{Format: Format, App: p.App, CreatedAt: time.Now().UTC().Truncate(time.Second), Images: map[string]string{}}
	if err := protectSecrets(dir); err != nil {
		return err
	}
	exports, err := makeExportsDir(dir)
	if err != nil {
		return err
	}
	defer os.RemoveAll(exports)
	excludes, err := exportDatabases(p, m)
	if err != nil {
		return err
	}
	if err := recordImages(p, m); err != nil {
		return err
	}
	if m.AppVersion, err = appVersion(p); err != nil {
		return err
	}
	if err := writeKitFiles(dir, m); err != nil {
		return err
	}

	step(i18n.T("backup.restic"))
	if err := repo.Backup(dir, excludes); err != nil {
		return err
	}
	fmt.Println(i18n.T("backup.done", p.App, m.AppVersion))
	return nil
}

// makeExportsDir creates db/ in dir. Database exports are secrets in clear:
// they exist only for the time of the backup, readable by the tool's user only.
func makeExportsDir(dir string) (string, error) {
	exports := filepath.Join(dir, "db")
	if err := os.MkdirAll(exports, 0o700); err != nil {
		return "", err
	}
	return exports, os.Chmod(exports, 0o700)
}

// exportDatabases exports every database into db/ and records it in m. It
// returns the live SQLite files, which the backup leaves out.
func exportDatabases(p *compose.Project, m *Manifest) ([]string, error) {
	var excludes []string
	for _, db := range p.Databases() {
		step(i18n.T("backup.export", db.Service, db.Engine))
		export, err := exportDB(p, db)
		if err != nil {
			return nil, err
		}
		m.Databases = append(m.Databases, export)
		if db.Engine == "sqlite" {
			// The live file is replaced by its consistent copy in db/.
			live, _ := p.HostPath(db.Service, db.Path)
			excludes = append(excludes, live, live+"-wal", live+"-shm", live+"-journal")
		}
	}
	return excludes, nil
}

// recordImages records in m the digest each service runs.
func recordImages(p *compose.Project, m *Manifest) error {
	for _, name := range p.ServiceNames() {
		image, err := runningImage(p, name)
		if err != nil {
			return err
		}
		m.Images[name] = image
	}
	return nil
}

// writeKitFiles writes manifest.json and RESTORE.md next to compose.yaml.
func writeKitFiles(dir string, m *Manifest) error {
	if err := writeManifest(dir, m); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "RESTORE.md"), kit.Restore, 0o644)
}

func exportDB(p *compose.Project, db compose.DB) (Export, error) {
	file := filepath.Join(p.Dir, db.File())
	if db.Engine == "sqlite" {
		live, _ := p.HostPath(db.Service, db.Path)
		cmd := exec.Command("docker", "run", "--rm",
			"-v", filepath.Dir(live)+":/src:ro", "-v", filepath.Dir(file)+":/out",
			docker.PythonImage, "python", "-c", sqliteBackup, filepath.Base(live), filepath.Base(file),
			strconv.Itoa(os.Getuid()), strconv.Itoa(os.Getgid()))
		if err := docker.Run(cmd); err != nil {
			return Export{}, err
		}
	} else {
		f, err := os.OpenFile(file, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return Export{}, err
		}
		cmd := docker.Compose(p.Dir, "exec", "-T", db.Service, "sh", "-c", dumpScripts[db.Engine])
		cmd.Stdout = f
		err = docker.Run(cmd)
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return Export{}, err
		}
	}
	sum, err := fileSHA256(file)
	if err != nil {
		return Export{}, err
	}
	return Export{Service: db.Service, Engine: db.Engine, File: db.File(), SHA256: sum, Path: db.Path}, nil
}

// protectSecrets makes .env readable by its owner only: it holds the
// application's secrets, and it is what the backup carries.
func protectSecrets(dir string) error {
	env := filepath.Join(dir, ".env")
	info, err := os.Stat(env)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0o077 == 0 {
		return nil
	}
	fmt.Println(i18n.T("backup.envmode", info.Mode().Perm()))
	return os.Chmod(env, 0o600)
}

func container(p *compose.Project, service string) (string, error) {
	id, err := docker.Output(docker.Compose(p.Dir, "ps", "-q", service))
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", fmt.Errorf("service %s is not running", service)
	}
	return id, nil
}

// runningImage returns the registry digest of the image a service runs, which
// is what a restore must run again.
func runningImage(p *compose.Project, service string) (string, error) {
	id, err := container(p, service)
	if err != nil {
		return "", err
	}
	imageID, err := docker.Output(exec.Command("docker", "inspect", "-f", "{{.Image}}", id))
	if err != nil {
		return "", err
	}
	out, err := docker.Output(exec.Command("docker", "image", "inspect", "-f", "{{json .RepoDigests}}", imageID))
	if err != nil {
		return "", err
	}
	var digests []string
	if err := json.Unmarshal([]byte(out), &digests); err != nil {
		return "", err
	}
	want := repoOf(p.Services[service].Image)
	for _, d := range digests {
		if repoOf(d) == want {
			return d, nil
		}
	}
	if len(digests) > 0 {
		return digests[0], nil
	}
	return "", fmt.Errorf("service %s: image %s has no registry digest; a restore could not pull it", service, p.Services[service].Image)
}

// repoOf strips the tag or digest of an image reference, and Docker Hub's
// implicit prefixes.
func repoOf(ref string) string {
	if i := strings.Index(ref, "@"); i >= 0 {
		ref = ref[:i]
	}
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		ref = ref[:i]
	}
	ref = strings.TrimPrefix(ref, "docker.io/")
	return strings.TrimPrefix(ref, "library/")
}

func appVersion(p *compose.Project) (string, error) {
	v := p.Version
	if v == nil {
		return "", nil
	}
	if v.Label != "" {
		id, err := container(p, v.Service)
		if err != nil {
			return "", err
		}
		return docker.Output(exec.Command("docker", "inspect", "-f", `{{index .Config.Labels "`+v.Label+`"}}`, id))
	}
	return docker.Output(docker.Compose(p.Dir, "exec", "-T", v.Service, "sh", "-c", v.Run))
}

func runHooks(p *compose.Project, phase string) error {
	for _, h := range p.Hooks(phase) {
		fmt.Printf("%s: %s\n", h.Service, h.Run)
		args := []string{"exec", "-T"}
		if h.User != "" {
			args = append(args, "-u", h.User)
		}
		if err := docker.Run(docker.Compose(p.Dir, append(args, h.Service, "sh", "-c", h.Run)...)); err != nil {
			return fmt.Errorf("%s hook on %s: %w", phase, h.Service, err)
		}
	}
	return nil
}
