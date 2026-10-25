package rescue

import (
	"fmt"
	"os"

	"github.com/mazetly/mazetly-rescue/internal/docker"
	"github.com/mazetly/mazetly-rescue/internal/i18n"
	"github.com/mazetly/mazetly-rescue/internal/restic"
)

// Verify restores a snapshot into a throwaway directory, starts it with no
// published port, checks that it runs the backed-up version, then removes it
// all. Where check proves the backup reads back, Verify proves the application
// comes back: a restore only exists once it has been tested.
func Verify(repo restic.Repo, snapshot string, allowUnsafe bool) (err error) {
	target, err := os.MkdirTemp("", "mazetly-rescue-verify-")
	if err != nil {
		return err
	}
	defer func() {
		step(i18n.T("verify.cleanup"))
		_ = docker.Run(docker.Compose(target, "down", "--volumes", "--remove-orphans"))
		if rmErr := docker.AsRoot([]string{target + ":/kit"}, `rm -rf /kit/* /kit/.[!.]*`); rmErr != nil && err == nil {
			err = rmErr
		}
		os.Remove(target)
	}()
	// Unattended by design, and the kit is still reviewed and audited.
	opts := RestoreOptions{Snapshot: snapshot, Yes: true, AllowUnsafe: allowUnsafe, Isolated: true}
	if err := Restore(repo, target, opts); err != nil {
		return err
	}
	fmt.Println(i18n.T("verify.ok"))
	return nil
}
