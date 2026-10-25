package rescue

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPinImages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "compose.yaml")
	compose := `services:
  db:
    image: mariadb:11.4
  app:
    image: "matomo:5-apache"
  worker:
    image: matomo:5-apache
`
	if err := os.WriteFile(path, []byte(compose), 0o644); err != nil {
		t.Fatal(err)
	}
	current := map[string]string{"db": "mariadb:11.4", "app": "matomo:5-apache", "worker": "matomo:5-apache"}
	pinned := map[string]string{"db": "mariadb@sha256:aa", "app": "matomo@sha256:bb", "worker": "matomo@sha256:bb"}
	if err := pinImages(path, current, pinned); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	want := `services:
  db:
    image: mariadb@sha256:aa
  app:
    image: matomo@sha256:bb
  worker:
    image: matomo@sha256:bb
`
	if string(got) != want {
		t.Errorf("got\n%s", got)
	}
}

func TestPinImagesNeedsALiteralImage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "compose.yaml")
	if err := os.WriteFile(path, []byte("services:\n  app:\n    image: ${IMAGE}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := pinImages(path, map[string]string{"app": "matomo:5"}, map[string]string{"app": "matomo@sha256:bb"})
	if err == nil {
		t.Error("an image set through a variable cannot be pinned and must be reported")
	}
}

func TestParseManifestChecksFormat(t *testing.T) {
	if _, err := parseManifest([]byte(`{"format":"mazetly-kit/9"}`)); err == nil {
		t.Error("an unknown format must be refused")
	}
}

func TestRepoOf(t *testing.T) {
	cases := map[string]string{
		"matomo:5-apache":                      "matomo",
		"ghcr.io/upellift99/gatecha:0.13.0":    "ghcr.io/upellift99/gatecha",
		"registry:5000/team/app":               "registry:5000/team/app",
		"matomo@sha256:bb":                     "matomo",
		"docker.io/library/mariadb:11.4":       "mariadb",
		"pgvector/pgvector:0.8.0-pg14":         "pgvector/pgvector",
		"docker.io/pgvector/pgvector@sha256:1": "pgvector/pgvector",
	}
	for ref, want := range cases {
		if got := repoOf(ref); got != want {
			t.Errorf("repoOf(%q) = %q, want %q", ref, got, want)
		}
	}
}
