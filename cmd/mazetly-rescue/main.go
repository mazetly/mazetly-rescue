// Command mazetly-rescue backs up, restores and checks applications hosted by
// Mazetly, without Mazetly. See SPEC.md.
package main

import (
	"crypto/rand"
	"encoding/base32"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/mazetly/mazetly-rescue/internal/i18n"
	"github.com/mazetly/mazetly-rescue/internal/rescue"
	"github.com/mazetly/mazetly-rescue/internal/restic"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `mazetly-rescue %s

Usage:
  mazetly-rescue check [--max-age <duration>]       does my key open my backups, do they read back?
  mazetly-rescue verify [--snapshot <id>]           restore into a throwaway directory, start, remove
  mazetly-rescue restore --target <dir> [--snapshot <id>] [--yes]
                                                    restore and start the application
  mazetly-rescue backup <instance-dir>              back up a running instance
  mazetly-rescue init [--customer-key-file <file>]  create a repository with a customer key
  mazetly-rescue copy --to <repo> [--init [--customer-key-file <file>]]
                                                    copy the backups to another repository
  mazetly-rescue version

Every command but version takes --repo and --password-file, or reads
RESTIC_REPOSITORY, RESTIC_PASSWORD and RESTIC_PASSWORD_FILE. copy reads the
destination's password from --to-password-file or RESCUE_TO_PASSWORD. S3
credentials come from AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY.

restore and verify refuse a kit that reaches beyond its directory (privileged
containers, host mounts, host network...); --allow-unsafe accepts it.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, usage, version)
		os.Exit(2)
	}
	if err := run(os.Args[1], os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, i18n.T("error", err))
		if errors.Is(err, rescue.ErrTooOld) {
			os.Exit(3)
		}
		os.Exit(1)
	}
}

func run(command string, args []string) error {
	fs := flag.NewFlagSet(command, flag.ExitOnError)
	c := cli{
		fs:           fs,
		repo:         fs.String("repo", os.Getenv("RESTIC_REPOSITORY"), "restic repository"),
		passwordFile: fs.String("password-file", os.Getenv("RESTIC_PASSWORD_FILE"), "file holding the repository password"),
	}
	switch command {
	case "restore":
		return c.restore(args)
	case "verify":
		return c.verify(args)
	case "copy":
		return c.copy(args)
	case "check":
		return c.check(args)
	case "backup":
		return c.backup(args)
	case "init":
		return c.initialize(args)
	case "version", "--version", "-v":
		fmt.Println(version)
		return nil
	default:
		fmt.Fprintf(os.Stderr, usage, version)
		os.Exit(2)
		return nil
	}
}

// cli holds the flags every command but version shares. Each command adds its
// own flags to fs before parsing.
type cli struct {
	fs           *flag.FlagSet
	repo         *string
	passwordFile *string
}

// open opens the repository named by the flags, once they are parsed.
func (c cli) open() (restic.Repo, error) {
	return openRepo(*c.repo, *c.passwordFile, "RESTIC_PASSWORD")
}

func (c cli) restore(args []string) error {
	target := c.fs.String("target", "", "directory to restore into (empty or absent)")
	snapshot := c.fs.String("snapshot", "latest", "snapshot to restore")
	yes := c.fs.Bool("yes", false, "start without asking, after the review")
	allowUnsafe := c.fs.Bool("allow-unsafe", false, "accept a kit that reaches beyond its directory")
	c.fs.Parse(args)
	if *target == "" {
		return errors.New("restore needs --target")
	}
	repo, err := c.open()
	if err != nil {
		return err
	}
	return rescue.Restore(repo, *target, rescue.RestoreOptions{Snapshot: *snapshot, Yes: *yes, AllowUnsafe: *allowUnsafe})
}

func (c cli) verify(args []string) error {
	snapshot := c.fs.String("snapshot", "latest", "snapshot to verify")
	allowUnsafe := c.fs.Bool("allow-unsafe", false, "accept a kit that reaches beyond its directory")
	c.fs.Parse(args)
	repo, err := c.open()
	if err != nil {
		return err
	}
	return rescue.Verify(repo, *snapshot, *allowUnsafe)
}

func (c cli) copy(args []string) error {
	to := c.fs.String("to", "", "destination repository")
	toPasswordFile := c.fs.String("to-password-file", "", "file holding the destination's password")
	initDst := c.fs.Bool("init", false, "create the destination first")
	keyFile := c.fs.String("customer-key-file", "", "with --init: file holding the customer key (generated when absent)")
	c.fs.Parse(args)
	if *to == "" {
		return errors.New("copy needs --to")
	}
	src, err := c.open()
	if err != nil {
		return err
	}
	dst, err := openRepo(*to, *toPasswordFile, "RESCUE_TO_PASSWORD")
	if err != nil {
		return err
	}
	var key string
	if *initDst {
		if key, err = customerKey(*keyFile); err != nil {
			return err
		}
	}
	if err := rescue.Copy(src, dst, *initDst, key); err != nil {
		return err
	}
	if *initDst && *keyFile == "" {
		fmt.Printf("\n%s\n\n    %s\n\n", i18n.T("init.customer"), key)
	}
	return nil
}

func (c cli) check(args []string) error {
	maxAge := c.fs.Duration("max-age", 0, "fail when the latest backup is older (e.g. 26h)")
	c.fs.Parse(args)
	repo, err := c.open()
	if err != nil {
		return err
	}
	return rescue.Check(repo, *maxAge)
}

func (c cli) backup(args []string) error {
	c.fs.Parse(args)
	if c.fs.NArg() != 1 {
		return errors.New("backup needs the instance directory")
	}
	repo, err := c.open()
	if err != nil {
		return err
	}
	return rescue.Backup(repo, c.fs.Arg(0))
}

func (c cli) initialize(args []string) error {
	keyFile := c.fs.String("customer-key-file", "", "file holding the customer key (generated when absent)")
	c.fs.Parse(args)
	repo, err := c.open()
	if err != nil {
		return err
	}
	return initRepo(repo, *keyFile)
}

// openRepo takes the password from a file, then from the environment
// variable passwordEnv, then asks for it on a terminal.
func openRepo(location, passwordFile, passwordEnv string) (restic.Repo, error) {
	if location == "" {
		return restic.Repo{}, errors.New(i18n.T("repo.missing"))
	}
	password := os.Getenv(passwordEnv)
	switch {
	case passwordFile != "":
		data, err := os.ReadFile(passwordFile)
		if err != nil {
			return restic.Repo{}, err
		}
		password = strings.TrimRight(string(data), "\r\n")
	case password == "" && term.IsTerminal(int(os.Stdin.Fd())):
		fmt.Fprint(os.Stderr, i18n.T("password.prompt"))
		data, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return restic.Repo{}, err
		}
		password = string(data)
	}
	if password == "" {
		return restic.Repo{}, errors.New(i18n.T("password.missing"))
	}
	return restic.Repo{Location: location, Password: password}, nil
}

// customerKey reads the customer key from keyFile, or generates one.
func customerKey(keyFile string) (string, error) {
	if keyFile != "" {
		data, err := os.ReadFile(keyFile)
		if err != nil {
			return "", err
		}
		return strings.TrimRight(string(data), "\r\n"), nil
	}
	random := make([]byte, 20)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	// The key is stored as shown, dashes included: what the customer reads out
	// is what opens the repository.
	return group(strings.ToLower(base32.StdEncoding.EncodeToString(random))), nil
}

func initRepo(repo restic.Repo, keyFile string) error {
	key, err := customerKey(keyFile)
	if err != nil {
		return err
	}
	if err := repo.Init(); err != nil {
		return err
	}
	if err := repo.AddKey(key, "customer", "mazetly-rescue"); err != nil {
		return err
	}
	fmt.Println(i18n.T("init.done"))
	if keyFile == "" {
		fmt.Printf("\n%s\n\n    %s\n\n", i18n.T("init.customer"), key)
	}
	return nil
}

// group splits a key in blocks of four, easier to read out and type.
func group(key string) string {
	var blocks []string
	for len(key) > 4 {
		blocks = append(blocks, key[:4])
		key = key[4:]
	}
	return strings.Join(append(blocks, key), "-")
}
