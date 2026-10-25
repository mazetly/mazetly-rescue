// Package rescue backs up, restores and checks kits.
package rescue

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const Format = "mazetly-kit/0"

// Manifest is what a backup produced; everything else is in compose.yaml.
type Manifest struct {
	Format     string            `json:"format"`
	App        string            `json:"app"`
	AppVersion string            `json:"app_version"`
	CreatedAt  time.Time         `json:"created_at"`
	Images     map[string]string `json:"images"`
	Databases  []Export          `json:"databases"`
}

type Export struct {
	Service string `json:"service"`
	Engine  string `json:"engine"`
	File    string `json:"file"`
	SHA256  string `json:"sha256"`
	Path    string `json:"path,omitempty"`
}

func readManifest(dir string) (*Manifest, error) {
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, err
	}
	return parseManifest(data)
}

func parseManifest(data []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("reading manifest.json: %w", err)
	}
	if m.Format != Format {
		return nil, fmt.Errorf("manifest.json: format %q, this tool reads %q", m.Format, Format)
	}
	return &m, nil
}

func writeManifest(dir string, m *Manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "manifest.json"), append(data, '\n'), 0o644)
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// pinImages rewrites compose.yaml so each service runs the image recorded in
// the manifest. current maps services to their image as written in the file.
func pinImages(path string, current, pinned map[string]string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	// Image lines are matched by their value: services sharing an image must
	// share its digest too.
	replace := map[string]string{}
	for service, image := range pinned {
		old, ok := current[service]
		if !ok {
			return fmt.Errorf("manifest.json names service %s, which compose.yaml does not have", service)
		}
		if prev, seen := replace[old]; seen && prev != image {
			return fmt.Errorf("compose.yaml: services sharing %s ran different images", old)
		}
		replace[old] = image
	}
	text := string(data)
	for old, image := range replace {
		if old == image {
			continue
		}
		line := regexp.MustCompile(`(?m)^(\s*image:\s*)["']?` + regexp.QuoteMeta(old) + `["']?[ \t]*$`)
		if !line.MatchString(text) {
			return fmt.Errorf("compose.yaml: no line \"image: %s\"", old)
		}
		text = line.ReplaceAllString(text, "${1}"+strings.ReplaceAll(image, "$", "$$"))
	}
	return os.WriteFile(path, []byte(text), 0o644)
}
