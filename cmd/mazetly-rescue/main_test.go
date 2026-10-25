package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mazetly/mazetly-rescue/internal/fakedocker"
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

func TestOpenRepo(t *testing.T) {
	t.Setenv("RESCUE_TEST_PASSWORD", "from-env")
	file := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(file, []byte("from-file\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := openRepo("s3:x", file, "RESCUE_TEST_PASSWORD")
	if err != nil || r.Password != "from-file" || r.Location != "s3:x" {
		t.Errorf("from a file: %+v, %v", r, err)
	}
	if r, err := openRepo("s3:x", "", "RESCUE_TEST_PASSWORD"); err != nil || r.Password != "from-env" {
		t.Errorf("from the environment: %+v, %v", r, err)
	}
	if _, err := openRepo("s3:x", filepath.Join(t.TempDir(), "missing"), "RESCUE_TEST_PASSWORD"); err == nil {
		t.Error("a missing password file accepted")
	}
	t.Setenv("RESCUE_TEST_PASSWORD", "")
	// go test gives no terminal on stdin: nothing to ask on.
	if _, err := openRepo("s3:x", "", "RESCUE_TEST_PASSWORD"); err == nil {
		t.Error("no password at all accepted")
	}
	if _, err := openRepo("", "", "RESCUE_TEST_PASSWORD"); err == nil {
		t.Error("no repository accepted")
	}
}

func TestCustomerKey(t *testing.T) {
	key, err := customerKey("")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[a-z2-7]{4}(-[a-z2-7]{4}){7}$`).MatchString(key) {
		t.Errorf("generated key %q", key)
	}
	if other, _ := customerKey(""); other == key {
		t.Error("two generated keys are equal")
	}
	file := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(file, []byte("chosen-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := customerKey(file); err != nil || got != "chosen-key" {
		t.Errorf("from a file: %q, %v", got, err)
	}
	if _, err := customerKey(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("a missing key file accepted")
	}
}

func TestGroup(t *testing.T) {
	for in, want := range map[string]string{"": "", "abc": "abc", "abcd": "abcd", "abcdefgh": "abcd-efgh", "abcdefghi": "abcd-efgh-i"} {
		if got := group(in); got != want {
			t.Errorf("group(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestInitAddsTheCustomerKey(t *testing.T) {
	fake := fakedocker.Install(t)
	keyFile := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(keyFile, []byte("customer-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(t.TempDir(), "repo")
	t.Setenv("RESTIC_PASSWORD", "mazetly")
	if err := run("init", []string{"--repo", repo, "--customer-key-file", keyFile}); err != nil {
		t.Fatal(err)
	}
	calls := fake.Calls()
	if len(calls) != 2 || !strings.Contains(calls[0].Line(), " init") || !strings.Contains(calls[1].Line(), " key add ") {
		t.Fatalf("calls: %+v", calls)
	}
	if calls[1].Stdin != "customer-key" {
		t.Errorf("customer key %q", calls[1].Stdin)
	}
	fake.Reply(" init", "", "config file already exists", 1)
	if err := run("init", []string{"--repo", repo}); err == nil {
		t.Error("a failed init reported as success")
	}
}

func TestCopyInitWithAGeneratedKey(t *testing.T) {
	fake := fakedocker.Install(t)
	fake.Reply(" snapshots ", `[{"id":"abc","time":"2026-10-25T04:00:00Z","paths":["/srv/app"]}]`, "", 0)
	t.Setenv("RESTIC_PASSWORD", "mazetly")
	t.Setenv("RESCUE_TO_PASSWORD", "own")
	err := run("copy", []string{"--repo", "s3:https://s3.example/m/app", "--to", filepath.Join(t.TempDir(), "copy"), "--init"})
	if err != nil {
		t.Fatal(err)
	}
	var added string
	for _, c := range fake.Calls() {
		if strings.Contains(c.Line(), " key add ") {
			added = c.Stdin
		}
	}
	if len(added) != 39 {
		t.Errorf("generated customer key %q", added)
	}
	t.Setenv("RESCUE_TO_PASSWORD", "")
	if err := run("copy", []string{"--repo", "s3:x", "--to", "s3:y"}); err == nil {
		t.Error("a destination without a password accepted")
	}
}
