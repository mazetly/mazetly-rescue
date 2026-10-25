package rescue

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mazetly/mazetly-rescue/internal/compose"
)

func TestPrepareTarget(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "app")
	got, err := prepareTarget(dir)
	if err != nil || got != dir {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("not created: %v", err)
	}
	if _, err := prepareTarget(dir); err != nil {
		t.Errorf("an empty directory refused: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "x"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareTarget(dir); err == nil {
		t.Error("a directory with files accepted")
	}
}

func TestVerifyExports(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "db"), 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "db", "db.sql")
	if err := os.WriteFile(file, []byte("CREATE TABLE t;\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sum, err := fileSHA256(file)
	if err != nil {
		t.Fatal(err)
	}
	m := &Manifest{Databases: []Export{{File: "db/db.sql", SHA256: sum}}}
	if err := verifyExports(dir, m); err != nil {
		t.Errorf("a matching export refused: %v", err)
	}
	m.Databases[0].SHA256 = "00"
	if err := verifyExports(dir, m); err == nil {
		t.Error("a mismatching export accepted")
	}
	m.Databases[0].File = "db/missing.sql"
	if err := verifyExports(dir, m); err == nil {
		t.Error("a missing export accepted")
	}
}

func TestServiceLine(t *testing.T) {
	s := compose.Service{Image: "matomo:5", Ports: []compose.Port{{Target: 80, Published: "8080"}, {Target: 9000}}}
	if got, want := serviceLine("app", s), "  app: matomo:5  [8080->80]"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, want := serviceLine("db", compose.Service{Image: "mariadb:11.4"}), "  db: mariadb:11.4"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestReviewRefusesUnsafe(t *testing.T) {
	p := &compose.Project{Dir: "/srv/kit", Services: map[string]compose.Service{"app": {Image: "x", Privileged: true}}}
	m := &Manifest{App: "demo"}
	if err := review(p, m, RestoreOptions{Yes: true}); err == nil {
		t.Error("a privileged service accepted")
	}
	if err := review(p, m, RestoreOptions{Yes: true, AllowUnsafe: true}); err != nil {
		t.Errorf("--allow-unsafe refused: %v", err)
	}
}
