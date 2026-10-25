package rescue

import (
	"fmt"

	"github.com/mazetly/mazetly-rescue/internal/i18n"
	"github.com/mazetly/mazetly-rescue/internal/restic"
)

// Copy sends the kit snapshots of src that dst lacks. With init, dst is created
// first, with src's chunking parameters so that copies deduplicate, and
// customerKey becomes its customer key: the copy opens without Mazetly.
func Copy(src, dst restic.Repo, init bool, customerKey string) error {
	if init {
		step(i18n.T("copy.init"))
		if err := dst.InitFrom(src); err != nil {
			return err
		}
		if err := dst.AddKey(customerKey, "customer", "mazetly-rescue"); err != nil {
			return err
		}
	}
	step(i18n.T("copy.copy"))
	if err := dst.CopyFrom(src); err != nil {
		return err
	}
	s, err := pickSnapshot(dst, "")
	if err != nil {
		return err
	}
	fmt.Println(i18n.T("copy.done", s.Time.Local().Format("2006-01-02 15:04")))
	return nil
}
