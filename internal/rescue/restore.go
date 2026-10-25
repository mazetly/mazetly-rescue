package rescue

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"

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

type RestoreOptions struct {
	// Snapshot is a snapshot ID prefix; empty or "latest" means the latest.
	Snapshot string
	// Yes skips the confirmation; needed when there is no terminal to ask on.
	Yes bool
	// AllowUnsafe accepts a kit that reaches beyond its directory.
	AllowUnsafe bool
	// Isolated publishes no port, for a test restore next to a live instance.
	Isolated bool
}

// Restore brings the kit of a snapshot back into target and starts it, by the
// steps of RESTORE.md. Nothing from the kit runs before it has been checked,
// shown and accepted: whoever can write to the repository writes the kit.
func Restore(repo restic.Repo, target string, opts RestoreOptions) error {
	target, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	if entries, err := os.ReadDir(target); err == nil && len(entries) > 0 {
		return errors.New(i18n.T("restore.notempty", target))
	}
	if err := os.MkdirAll(target, 0o700); err != nil {
		return err
	}
	s, err := pickSnapshot(repo, opts.Snapshot)
	if err != nil {
		return err
	}

	step(i18n.T("restore.snapshot", s.Time.Local().Format("2006-01-02 15:04"), s.Hostname, s.ShortID))
	if err := repo.Restore(s, target); err != nil {
		return err
	}
	// Secrets and database exports in clear: for root only, whatever the
	// owners and modes the backup recorded.
	if err := docker.AsRoot([]string{target + ":/kit"}, `chmod 600 /kit/.env 2>/dev/null; chmod -R go= /kit/db`); err != nil {
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
	if opts.Isolated {
		if err := isolate(p); err != nil {
			return err
		}
	}
	if p, err = compose.Load(target); err != nil {
		return err
	}
	if err := review(p, m, opts); err != nil {
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
	// The exports are in the databases now, and in the backup: no clear copy
	// is left behind.
	if err := docker.AsRoot([]string{target + ":/kit"}, `rm -rf /kit/db`); err != nil {
		return err
	}
	fmt.Println()
	fmt.Println(i18n.T("restore.done", m.App, m.AppVersion, target))
	if !opts.Isolated {
		fmt.Println(i18n.T("restore.next"))
	}
	return nil
}

// review shows what the kit is about to run and asks before running it. A kit
// reaching beyond its directory is refused unless explicitly allowed.
func review(p *compose.Project, m *Manifest, opts RestoreOptions) error {
	step(i18n.T("restore.review"))
	fmt.Printf("%s %s\n", m.App, m.AppVersion)
	for _, name := range p.ServiceNames() {
		s := p.Services[name]
		var ports []string
		for _, port := range s.Ports {
			if port.Published != "" {
				ports = append(ports, fmt.Sprintf("%s->%d", port.Published, port.Target))
			}
		}
		line := fmt.Sprintf("  %s: %s", name, s.Image)
		if len(ports) > 0 {
			line += "  [" + strings.Join(ports, ", ") + "]"
		}
		fmt.Println(line)
	}
	for _, h := range p.Hooks("post_restore") {
		fmt.Printf("  %s %s: %s\n", i18n.T("restore.hook"), h.Service, h.Run)
	}
	if unsafe := p.Unsafe(); len(unsafe) > 0 {
		fmt.Println(i18n.T("restore.unsafe"))
		for _, u := range unsafe {
			fmt.Println("  - " + u)
		}
		if !opts.AllowUnsafe {
			return errors.New(i18n.T("restore.refused"))
		}
	}
	if opts.Yes {
		return nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return errors.New(i18n.T("restore.needyes"))
	}
	fmt.Print(i18n.T("restore.confirm"))
	answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes", "o", "oui":
		return nil
	}
	return errors.New(i18n.T("restore.aborted"))
}

// isolate keeps a test restore off the host's ports and container names, so it
// can run next to the live instance.
func isolate(p *compose.Project) error {
	var b strings.Builder
	b.WriteString("services:\n")
	for _, name := range p.ServiceNames() {
		fmt.Fprintf(&b, "  %s:\n    ports: !reset []\n", name)
		if p.Services[name].ContainerName != "" {
			b.WriteString("    container_name: !reset null\n")
		}
	}
	return os.WriteFile(filepath.Join(p.Dir, "compose.override.yaml"), []byte(b.String()), 0o644)
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
			`cp "/kit/$1" "/kit/$2" && chown "$(stat -c %u:%g "$(dirname "/kit/$2")")" "/kit/$2" && chmod 600 "/kit/$2"`,
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
