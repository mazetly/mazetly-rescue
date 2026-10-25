package docker

import (
	"bytes"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/mazetly/mazetly-rescue/internal/fakedocker"
)

func TestCompose(t *testing.T) {
	cmd := Compose("/srv/app", "up", "-d")
	want := []string{"docker", "compose", "--project-directory", "/srv/app", "up", "-d"}
	if !slices.Equal(cmd.Args, want) {
		t.Errorf("args: %v", cmd.Args)
	}
}

func TestRunNamesTheCommandWithoutItsArguments(t *testing.T) {
	fake := fakedocker.Install(t)
	fake.Reply("down", "", "", 3)
	var out bytes.Buffer
	cmd := Compose("/srv/secret-path", "down")
	cmd.Stdout, cmd.Stderr = &out, &out
	err := Run(cmd)
	if err == nil {
		t.Fatal("a failure reported as success")
	}
	if msg := err.Error(); !strings.HasPrefix(msg, "docker compose down: ") || strings.Contains(msg, "secret-path") {
		t.Errorf("error %q", msg)
	}
	if err := Run(Compose("/srv/app", "up")); err != nil {
		t.Errorf("a success reported as %v", err)
	}
}

func TestOutput(t *testing.T) {
	fake := fakedocker.Install(t)
	fake.Reply("ps", "  abc123\n\n", "", 0)
	fake.Reply("inspect", "", "No such object: x\n", 1)
	fake.Reply("pull", "", "", 1)
	got, err := Output(Compose("/srv/app", "ps", "-q"))
	if err != nil || got != "abc123" {
		t.Errorf("got %q, %v", got, err)
	}
	_, err = Output(exec.Command("docker", "inspect", "x"))
	if err == nil || err.Error() != "docker inspect: No such object: x" {
		t.Errorf("stderr not reported: %v", err)
	}
	_, err = Output(exec.Command("docker", "pull", "x"))
	if err == nil || !strings.HasPrefix(err.Error(), "docker pull: exit status 1") {
		t.Errorf("a failure without stderr: %v", err)
	}
}

func TestAsRoot(t *testing.T) {
	fake := fakedocker.Install(t)
	if err := AsRoot([]string{"/srv/app:/kit"}, `rm -rf "/kit/$1"`, "db"); err != nil {
		t.Fatal(err)
	}
	want := []string{"run", "--rm", "-v", "/srv/app:/kit", "--entrypoint", "sh", ResticImage, "-c", `rm -rf "/kit/$1"`, "sh", "db"}
	if got := fake.Last().Args; !slices.Equal(got, want) {
		t.Errorf("args: %v", got)
	}
}

func TestDescribe(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"docker", "compose", "--project-directory", "/x", "exec", "-T", "db"}, "docker compose exec"},
		{[]string{"docker", "compose", "--project-directory", "/x"}, "docker compose "},
		{[]string{"docker", "run", "--rm", "img"}, "docker run"},
	} {
		if got := describe(&exec.Cmd{Path: "/usr/bin/docker", Args: c.args}); got != c.want {
			t.Errorf("%v: got %q, want %q", c.args, got, c.want)
		}
	}
	if got := describe(&exec.Cmd{Path: "/usr/bin/docker", Args: []string{"docker"}}); got != "/usr/bin/docker" {
		t.Errorf("bare command: %q", got)
	}
}
