// Package restic drives restic from its official image, pinned by digest.
package restic

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mazetly/mazetly-rescue/internal/docker"
)

// remote lists the restic backends that are not a local path.
var remote = []string{"s3:", "sftp:", "rest:", "b2:", "azure:", "gs:", "swift:", "rclone:"}

// passEnv lists the prefixes of variables restic backends read, handed over
// to the container.
var passEnv = []string{"AWS_", "B2_", "AZURE_", "GOOGLE_", "OS_", "ST_", "RCLONE_"}

// kitTag marks the kit snapshots. kit/RESTORE.md has customers type it by
// hand: it cannot change.
const kitTag = "mazetly-kit"

// noLock is passed to every read: on Mazetly storage the customer's
// credentials are read-only, so restic could not write its lock.
const noLock = "--no-lock"

type Repo struct {
	Location string
	Password string
}

func (r Repo) local() bool {
	for _, prefix := range remote {
		if strings.HasPrefix(r.Location, prefix) {
			return false
		}
	}
	return true
}

// command builds `docker run restic/restic ...`. The password goes through the
// environment, never the command line.
func (r Repo) command(mounts []string, args ...string) (*exec.Cmd, error) {
	return r.commandFrom(nil, mounts, args...)
}

// commandFrom is command with a second repository, from, for init and copy:
// restic reads it from RESTIC_FROM_REPOSITORY and RESTIC_FROM_PASSWORD.
func (r Repo) commandFrom(from *Repo, mounts []string, args ...string) (*exec.Cmd, error) {
	cmdArgs := []string{"run", "--rm", "-i"}
	env := os.Environ()
	for _, repo := range []struct {
		r      *Repo
		prefix string
		mount  string
	}{{&r, "RESTIC_", "/repo"}, {from, "RESTIC_FROM_", "/from"}} {
		if repo.r == nil {
			continue
		}
		location := repo.r.Location
		if repo.r.local() {
			abs, err := filepath.Abs(location)
			if err != nil {
				return nil, err
			}
			cmdArgs = append(cmdArgs, "-v", abs+":"+repo.mount)
			location = repo.mount
		}
		cmdArgs = append(cmdArgs, "-e", repo.prefix+"PASSWORD", "-e", repo.prefix+"REPOSITORY")
		env = append(env, repo.prefix+"PASSWORD="+repo.r.Password, repo.prefix+"REPOSITORY="+location)
	}
	for _, m := range mounts {
		cmdArgs = append(cmdArgs, "-v", m)
	}
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		for _, prefix := range passEnv {
			if strings.HasPrefix(name, prefix) {
				cmdArgs = append(cmdArgs, "-e", name)
			}
		}
	}
	cmd := exec.Command("docker", append(append(cmdArgs, docker.ResticImage), args...)...)
	cmd.Env = env
	return cmd, nil
}

func (r Repo) run(mounts []string, args ...string) error {
	cmd, err := r.command(mounts, args...)
	if err != nil {
		return err
	}
	return docker.Run(cmd)
}

// Init creates the repository; the current password becomes its first key.
func (r Repo) Init() error {
	if r.local() {
		if err := os.MkdirAll(r.Location, 0o700); err != nil {
			return err
		}
	}
	return r.run(nil, "init")
}

// InitFrom creates the repository with the chunking parameters of from, so
// that copies between them deduplicate.
func (r Repo) InitFrom(from Repo) error {
	if r.local() {
		if err := os.MkdirAll(r.Location, 0o700); err != nil {
			return err
		}
	}
	cmd, err := r.commandFrom(&from, nil, "init", "--copy-chunker-params")
	if err != nil {
		return err
	}
	return docker.Run(cmd)
}

// CopyFrom copies the kit snapshots of from that this repository lacks.
func (r Repo) CopyFrom(from Repo) error {
	cmd, err := r.commandFrom(&from, nil, "copy", "--tag", kitTag)
	if err != nil {
		return err
	}
	return docker.Run(cmd)
}

// AddKey adds a key with its own password, labelled with user and host.
func (r Repo) AddKey(password, user, host string) error {
	cmd, err := r.command(nil, "key", "add", "--new-password-file", "/dev/stdin", "--user", user, "--host", host)
	if err != nil {
		return err
	}
	cmd.Stdin = strings.NewReader(password)
	return docker.Run(cmd)
}

// Backup backs dir up under its own absolute path, which a restore finds again
// in the snapshot.
func (r Repo) Backup(dir string, excludes []string) error {
	args := []string{"backup", "--tag", kitTag}
	for _, e := range excludes {
		args = append(args, "--exclude", e)
	}
	return r.run([]string{dir + ":" + dir + ":ro"}, append(args, dir)...)
}

type Snapshot struct {
	ID       string    `json:"id"`
	ShortID  string    `json:"short_id"`
	Time     time.Time `json:"time"`
	Hostname string    `json:"hostname"`
	Paths    []string  `json:"paths"`
}

// Snapshots lists the kit snapshots, oldest first.
func (r Repo) Snapshots() ([]Snapshot, error) {
	cmd, err := r.command(nil, "snapshots", noLock, "--json", "--tag", kitTag)
	if err != nil {
		return nil, err
	}
	out, err := docker.Output(cmd)
	if err != nil {
		return nil, err
	}
	var snapshots []Snapshot
	if err := json.Unmarshal([]byte(out), &snapshots); err != nil {
		return nil, fmt.Errorf("reading restic snapshots: %w", err)
	}
	return snapshots, nil
}

// Restore restores the kit of a snapshot into target, which must exist.
// restic runs as root, so files keep their owners.
func (r Repo) Restore(s Snapshot, target string) error {
	return r.run([]string{target + ":/restore"}, "restore", s.ID+":"+s.Paths[0], noLock, "--target", "/restore")
}

// Dump streams one file of a snapshot's kit to w.
func (r Repo) Dump(s Snapshot, file string, w io.Writer) error {
	cmd, err := r.command(nil, "dump", noLock, s.ID, s.Paths[0]+"/"+file)
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = w, &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("restic dump %s: %s", file, strings.TrimSpace(stderr.String()))
	}
	return nil
}
