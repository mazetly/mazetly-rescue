package rescue

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMakeExportsDirIsPrivate(t *testing.T) {
	dir := t.TempDir()
	// A db/ left over with loose permissions is tightened, not trusted.
	if err := os.Mkdir(filepath.Join(dir, "db"), 0o755); err != nil {
		t.Fatal(err)
	}
	exports, err := makeExportsDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(exports)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("db/ is %o, want 700", perm)
	}
}

func TestWriteKitFiles(t *testing.T) {
	dir := t.TempDir()
	m := &Manifest{Format: Format, App: "demo", AppVersion: "1.0"}
	if err := writeKitFiles(dir, m); err != nil {
		t.Fatal(err)
	}
	got, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.App != "demo" || got.AppVersion != "1.0" {
		t.Errorf("manifest read back as %+v", got)
	}
	if info, err := os.Stat(filepath.Join(dir, "RESTORE.md")); err != nil || info.Size() == 0 {
		t.Errorf("RESTORE.md missing or empty: %v", err)
	}
}

func TestProtectSecrets(t *testing.T) {
	dir := t.TempDir()
	if err := protectSecrets(dir); err != nil {
		t.Errorf("no .env: %v", err)
	}
	env := filepath.Join(dir, ".env")
	if err := os.WriteFile(env, []byte("SECRET=x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(env, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := protectSecrets(dir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(env)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf(".env is %o, want 600", perm)
	}
}
