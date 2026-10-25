package main

import (
	"strings"
	"testing"
)

// Each command checks its own arguments before touching a repository.
func TestRunChecksArguments(t *testing.T) {
	t.Setenv("RESTIC_REPOSITORY", "")
	t.Setenv("RESTIC_PASSWORD_FILE", "")
	cases := []struct {
		command string
		args    []string
		want    string
	}{
		{"restore", nil, "restore needs --target"},
		{"copy", nil, "copy needs --to"},
		{"backup", nil, "backup needs the instance directory"},
		{"backup", []string{"a", "b"}, "backup needs the instance directory"},
	}
	for _, c := range cases {
		err := run(c.command, c.args)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s %v: got %v, want %q", c.command, c.args, err, c.want)
		}
	}
}

// Without a repository, every command that needs one says so.
func TestRunNeedsARepository(t *testing.T) {
	t.Setenv("RESTIC_REPOSITORY", "")
	t.Setenv("RESTIC_PASSWORD_FILE", "")
	for _, c := range []struct {
		command string
		args    []string
	}{
		{"restore", []string{"--target", "/x"}},
		{"verify", nil},
		{"copy", []string{"--to", "/x"}},
		{"check", nil},
		{"backup", []string{"/x"}},
		{"init", nil},
	} {
		if err := run(c.command, c.args); err == nil {
			t.Errorf("%s: no error without a repository", c.command)
		}
	}
}

func TestRunVersion(t *testing.T) {
	if err := run("version", nil); err != nil {
		t.Fatal(err)
	}
}
