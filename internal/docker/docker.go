// Package docker runs the containers the tool relies on. Docker is the only
// thing a server needs: restic and SQLite run from pinned images.
package docker

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const (
	// ResticImage also provides a busybox shell for file operations as root.
	ResticImage = "restic/restic:0.18.0@sha256:4cf4a61ef9786f4de53e9de8c8f5c040f33830eb0a10bf3d614410ee2fcb6120"
	PythonImage = "python:3.13-alpine@sha256:bf12ecc0bea3f171d55145dd02c78b5eeb09f30ee2ac9cc2b4c9e4e8f5e30741"
)

// Compose returns a docker compose command for the project in dir.
func Compose(dir string, args ...string) *exec.Cmd {
	return exec.Command("docker", append([]string{"compose", "--project-directory", dir}, args...)...)
}

// Run runs cmd, showing its output, and returns an error naming it.
func Run(cmd *exec.Cmd) error {
	if cmd.Stdout == nil {
		cmd.Stdout = os.Stdout
	}
	if cmd.Stderr == nil {
		cmd.Stderr = os.Stderr
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", describe(cmd), err)
	}
	return nil
}

// Output runs cmd and returns its trimmed output.
func Output(cmd *exec.Cmd) (string, error) {
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && len(exit.Stderr) > 0 {
			return "", fmt.Errorf("%s: %s", describe(cmd), strings.TrimSpace(string(exit.Stderr)))
		}
		return "", fmt.Errorf("%s: %w", describe(cmd), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// AsRoot runs a shell script as root in a throwaway container, with host
// directories mounted as given ("host:container"). Extra arguments are
// available to the script as $1, $2...
func AsRoot(mounts []string, script string, args ...string) error {
	cmdArgs := []string{"run", "--rm"}
	for _, m := range mounts {
		cmdArgs = append(cmdArgs, "-v", m)
	}
	cmdArgs = append(cmdArgs, "--entrypoint", "sh", ResticImage, "-c", script, "sh")
	return Run(exec.Command("docker", append(cmdArgs, args...)...))
}

// describe names a command without its arguments, which may hold paths only
// meaningful to the tool.
func describe(cmd *exec.Cmd) string {
	if len(cmd.Args) > 2 && cmd.Args[1] == "compose" {
		return "docker compose " + firstCommand(cmd.Args[2:])
	}
	if len(cmd.Args) > 1 {
		return cmd.Args[0] + " " + cmd.Args[1]
	}
	return cmd.Path
}

func firstCommand(args []string) string {
	for i := 0; i < len(args); i++ {
		if args[i] == "--project-directory" {
			i++
			continue
		}
		return args[i]
	}
	return ""
}
