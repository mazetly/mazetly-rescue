package rescue

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/mazetly/mazetly-rescue/internal/i18n"
	"github.com/mazetly/mazetly-rescue/internal/restic"
)

// ErrTooOld reports a readable repository whose latest backup is too old.
var ErrTooOld = errors.New("latest backup too old")

// Check proves, with the key given, that the repository opens and that the
// latest backup's database exports read back intact. maxAge 0 skips the age
// check.
func Check(repo restic.Repo, maxAge time.Duration) error {
	s, err := pickSnapshot(repo, "")
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := repo.Dump(s, "manifest.json", &buf); err != nil {
		return err
	}
	m, err := parseManifest(buf.Bytes())
	if err != nil {
		return err
	}
	age := time.Since(s.Time).Round(time.Minute)
	fmt.Println(i18n.T("check.latest", s.Time.Local().Format("2006-01-02 15:04"), age, m.App, m.AppVersion, s.Hostname))
	for _, e := range m.Databases {
		h := sha256.New()
		if err := repo.Dump(s, e.File, h); err != nil {
			return err
		}
		if hex.EncodeToString(h.Sum(nil)) != e.SHA256 {
			return errors.New(i18n.T("check.mismatch", e.File))
		}
		fmt.Println(i18n.T("check.db", e.File))
	}
	if maxAge > 0 && age > maxAge {
		fmt.Println(i18n.T("check.old", maxAge))
		return ErrTooOld
	}
	fmt.Println(i18n.T("check.ok"))
	return nil
}
