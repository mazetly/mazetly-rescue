// Package compose reads a recipe: an application's compose.yaml as normalized
// by Docker Compose, with its x-mazetly fields.
package compose

import (
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// Engines are the database engines the tool can export and import.
var Engines = []string{"mariadb", "mysql", "postgres", "sqlite"}

// Phases are the moments a hook can run at.
var Phases = []string{"pre_backup", "post_backup", "post_restore", "post_upgrade"}

type Project struct {
	Dir      string
	App      string
	Version  *Version
	Services map[string]Service
}

// Version tells how to read the running application's version: the output of
// a command in a service, or a label of the service's image.
type Version struct {
	Service string `yaml:"service"`
	Run     string `yaml:"run"`
	Label   string `yaml:"label"`
}

type Service struct {
	Image   string   `yaml:"image"`
	Volumes []Volume `yaml:"volumes"`
	X       X        `yaml:"x-mazetly"`
}

type Volume struct {
	Type   string `yaml:"type"`
	Source string `yaml:"source"`
	Target string `yaml:"target"`
}

type X struct {
	Database *Database         `yaml:"database"`
	Hooks    map[string][]Hook `yaml:"hooks"`
	Upgrade  struct {
		RenewAnonVolumes bool `yaml:"renew_anon_volumes"`
	} `yaml:"upgrade"`
}

type Database struct {
	Engine string `yaml:"engine"`
	Path   string `yaml:"path"`
}

// Hook is a command run in its service, written either as a plain string or
// as {user, run}.
type Hook struct {
	User string `yaml:"user"`
	Run  string `yaml:"run"`
}

func (h *Hook) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		h.Run = unescape(n.Value)
		return nil
	}
	type plain Hook
	if err := n.Decode((*plain)(h)); err != nil {
		return err
	}
	h.Run = unescape(h.Run)
	return nil
}

// unescape turns Compose's $$ back into $. Compose interpolates x-* fields
// too, so a recipe writes $$ for a literal $, and `docker compose config`
// prints it still escaped.
func unescape(s string) string { return strings.ReplaceAll(s, "$$", "$") }

// Load normalizes dir's compose file with Docker Compose. Its JSON output
// drops x-* fields, its YAML output keeps them.
func Load(dir string) (*Project, error) {
	out, err := exec.Command("docker", "compose", "--project-directory", dir, "config").Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return nil, fmt.Errorf("docker compose config: %s", strings.TrimSpace(string(exit.Stderr)))
		}
		return nil, fmt.Errorf("docker compose config: %w", err)
	}
	return Parse(dir, out)
}

// Parse reads the output of `docker compose config`.
func Parse(dir string, config []byte) (*Project, error) {
	var raw struct {
		Services map[string]Service `yaml:"services"`
		X        struct {
			App     string   `yaml:"app"`
			Version *Version `yaml:"version"`
		} `yaml:"x-mazetly"`
	}
	if err := yaml.Unmarshal(config, &raw); err != nil {
		return nil, fmt.Errorf("reading the compose file: %w", err)
	}
	p := &Project{Dir: dir, App: raw.X.App, Version: raw.X.Version, Services: raw.Services}
	if p.Version != nil {
		p.Version.Run = unescape(p.Version.Run)
	}
	if p.App == "" {
		return nil, errors.New("the compose file has no x-mazetly.app")
	}
	for _, name := range p.ServiceNames() {
		s := p.Services[name]
		if db := s.X.Database; db != nil {
			if !slices.Contains(Engines, db.Engine) {
				return nil, fmt.Errorf("service %s: unknown database engine %q", name, db.Engine)
			}
			if db.Engine == "sqlite" {
				if _, err := p.HostPath(name, db.Path); err != nil {
					return nil, err
				}
			}
		}
		for phase := range s.X.Hooks {
			if !slices.Contains(Phases, phase) {
				return nil, fmt.Errorf("service %s: unknown hook phase %q", name, phase)
			}
		}
	}
	if v := p.Version; v != nil {
		if _, ok := p.Services[v.Service]; !ok {
			return nil, fmt.Errorf("x-mazetly.version: unknown service %q", v.Service)
		}
		if (v.Run == "") == (v.Label == "") {
			return nil, errors.New("x-mazetly.version needs either run or label")
		}
	}
	return p, nil
}

// ServiceNames returns the service names in a stable order: hooks, exports and
// imports follow it.
func (p *Project) ServiceNames() []string {
	names := make([]string, 0, len(p.Services))
	for name := range p.Services {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

type DB struct {
	Service string
	Engine  string
	Path    string
}

func (p *Project) Databases() []DB {
	var dbs []DB
	for _, name := range p.ServiceNames() {
		if db := p.Services[name].X.Database; db != nil {
			dbs = append(dbs, DB{Service: name, Engine: db.Engine, Path: db.Path})
		}
	}
	return dbs
}

// File is where a database's export lives in the kit, relative to it.
func (db DB) File() string {
	if db.Engine == "sqlite" {
		return "db/" + db.Service + ".sqlite"
	}
	return "db/" + db.Service + ".sql"
}

// HostPath maps a path inside a service to the host, through its bind mounts.
func (p *Project) HostPath(service, path string) (string, error) {
	for _, v := range p.Services[service].Volumes {
		if v.Type == "bind" && strings.HasPrefix(path, v.Target+"/") {
			return v.Source + strings.TrimPrefix(path, v.Target), nil
		}
	}
	return "", fmt.Errorf("service %s: %s is not under a bind mount", service, path)
}

type ServiceHook struct {
	Service string
	Hook
}

// Hooks returns the hooks of a phase, service by service.
func (p *Project) Hooks(phase string) []ServiceHook {
	var hooks []ServiceHook
	for _, name := range p.ServiceNames() {
		for _, h := range p.Services[name].X.Hooks[phase] {
			hooks = append(hooks, ServiceHook{Service: name, Hook: h})
		}
	}
	return hooks
}
