package rescue

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mazetly/mazetly-rescue/internal/compose"
	"github.com/mazetly/mazetly-rescue/internal/docker"
	"github.com/mazetly/mazetly-rescue/internal/i18n"
	"github.com/mazetly/mazetly-rescue/internal/restic"
)

var importScripts = map[string]string{
	"mariadb":  `exec mariadb -u"$MARIADB_USER" -p"$MARIADB_PASSWORD" "$MARIADB_DATABASE"`,
	"mysql":    `exec mysql -u"$MYSQL_USER" -p"$MYSQL_PASSWORD" "$MYSQL_DATABASE"`,
	"postgres": `exec psql -q -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "${POSTGRES_DB:-$POSTGRES_USER}" >/dev/null`,
}

// Restore brings the kit of a snapshot back into target and starts it, by the
// steps of RESTORE.md. An empty snapshot ID means the latest.
func Restore(repo restic.Repo, target, snapshotID string) error {
	target, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	if entries, err := os.ReadDir(target); err == nil && len(entries) > 0 {
		return errors.New(i18n.T("restore.notempty", target))
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return err
	}
	s, err := pickSnapshot(repo, snapshotID)
	if err != nil {
		return err
	}

	step(i18n.T("restore.snapshot", s.Time.Local().Format("2006-01-02 15:04"), s.Hostname, s.ShortID))
	if err := repo.Restore(s, target); err != nil {
		return err
	}
	m, err := readManifest(target)
	if err != nil {
		return err
	}

	step(i18n.T("restore.verify"))
	for _, e := range m.Databases {
		sum, err := fileSHA256(filepath.Join(target, e.File))
		if err != nil {
			return err
		}
		if sum != e.SHA256 {
			return errors.New(i18n.T("check.mismatch", e.File))
		}
		fmt.Println(e.File + ": OK")
	}

	step(i18n.T("restore.pin"))
	p, err := compose.Load(target)
	if err != nil {
		return err
	}
	current := map[string]string{}
	for name, s := range p.Services {
		current[name] = s.Image
	}
	if err := pinImages(filepath.Join(target, "compose.yaml"), current, m.Images); err != nil {
		return err
	}
	if p, err = compose.Load(target); err != nil {
		return err
	}

	for _, db := range p.Databases() {
		step(i18n.T("restore.import", db.Service, db.Engine))
		if err := importDB(p, db); err != nil {
			return err
		}
	}
	step(i18n.T("restore.start"))
	if err := docker.Run(docker.Compose(target, "up", "-d", "--wait", "--wait-timeout", "600")); err != nil {
		return err
	}
	step(i18n.T("backup.hooks", "post_restore"))
	if err := runHooks(p, "post_restore"); err != nil {
		return err
	}
	// The point of pinning: what runs now is exactly what was backed up.
	running, err := appVersion(p)
	if err != nil {
		return err
	}
	if running != m.AppVersion {
		return errors.New(i18n.T("restore.version", running, m.AppVersion))
	}
	fmt.Println()
	fmt.Println(i18n.T("restore.done", m.App, m.AppVersion, target))
	fmt.Println(i18n.T("restore.next"))
	return nil
}

func pickSnapshot(repo restic.Repo, id string) (restic.Snapshot, error) {
	snapshots, err := repo.Snapshots()
	if err != nil {
		return restic.Snapshot{}, err
	}
	if len(snapshots) == 0 {
		return restic.Snapshot{}, errors.New(i18n.T("check.none"))
	}
	if id == "" || id == "latest" {
		return snapshots[len(snapshots)-1], nil
	}
	for _, s := range snapshots {
		if strings.HasPrefix(s.ID, id) {
			return s, nil
		}
	}
	return restic.Snapshot{}, fmt.Errorf("no snapshot %s", id)
}

func importDB(p *compose.Project, db compose.DB) error {
	if db.Engine == "sqlite" {
		// Before anything starts: same owner as the directory the application
		// writes to.
		live, _ := p.HostPath(db.Service, db.Path)
		rel, err := filepath.Rel(p.Dir, live)
		if err != nil || strings.HasPrefix(rel, "..") {
			return fmt.Errorf("service %s: %s is outside the kit", db.Service, db.Path)
		}
		return docker.AsRoot([]string{p.Dir + ":/kit"},
			`cp "/kit/$1" "/kit/$2" && chown "$(stat -c %u:%g "$(dirname "/kit/$2")")" "/kit/$2"`,
			db.File(), rel)
	}
	if err := docker.Run(docker.Compose(p.Dir, "up", "-d", "--wait", "--wait-timeout", "600", db.Service)); err != nil {
		return err
	}
	f, err := os.Open(filepath.Join(p.Dir, db.File()))
	if err != nil {
		return err
	}
	defer f.Close()
	cmd := docker.Compose(p.Dir, "exec", "-T", db.Service, "sh", "-c", importScripts[db.Engine])
	cmd.Stdin = f
	return docker.Run(cmd)
}
