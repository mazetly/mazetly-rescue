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
  mazetly-rescue restore --target <dir> [--snapshot <id>]   restore and start the application
  mazetly-rescue check [--max-age <duration>]               check that your key opens your backups
  mazetly-rescue backup <instance-dir>                      back up a running instance
  mazetly-rescue init [--customer-key-file <file>]          create a repository with a customer key
  mazetly-rescue version

Every command but version takes --repo and --password-file, or reads
RESTIC_REPOSITORY, RESTIC_PASSWORD and RESTIC_PASSWORD_FILE. S3 credentials
come from AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY.
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
	repoFlag := fs.String("repo", os.Getenv("RESTIC_REPOSITORY"), "restic repository")
	passwordFile := fs.String("password-file", os.Getenv("RESTIC_PASSWORD_FILE"), "file holding the repository password")
	switch command {
	case "restore":
		target := fs.String("target", "", "directory to restore into (empty or absent)")
		snapshot := fs.String("snapshot", "latest", "snapshot to restore")
		fs.Parse(args)
		if *target == "" {
			return errors.New("restore needs --target")
		}
		repo, err := openRepo(*repoFlag, *passwordFile)
		if err != nil {
			return err
		}
		return rescue.Restore(repo, *target, *snapshot)
	case "check":
		maxAge := fs.Duration("max-age", 0, "fail when the latest backup is older (e.g. 26h)")
		fs.Parse(args)
		repo, err := openRepo(*repoFlag, *passwordFile)
		if err != nil {
			return err
		}
		return rescue.Check(repo, *maxAge)
	case "backup":
		fs.Parse(args)
		if fs.NArg() != 1 {
			return errors.New("backup needs the instance directory")
		}
		repo, err := openRepo(*repoFlag, *passwordFile)
		if err != nil {
			return err
		}
		return rescue.Backup(repo, fs.Arg(0))
	case "init":
		keyFile := fs.String("customer-key-file", "", "file holding the customer key (generated when absent)")
		fs.Parse(args)
		repo, err := openRepo(*repoFlag, *passwordFile)
		if err != nil {
			return err
		}
		return initRepo(repo, *keyFile)
	case "version", "--version", "-v":
		fmt.Println(version)
		return nil
	default:
		fmt.Fprintf(os.Stderr, usage, version)
		os.Exit(2)
		return nil
	}
}

func openRepo(location, passwordFile string) (restic.Repo, error) {
	if location == "" {
		return restic.Repo{}, errors.New(i18n.T("repo.missing"))
	}
	password := os.Getenv("RESTIC_PASSWORD")
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

func initRepo(repo restic.Repo, keyFile string) error {
	var key string
	if keyFile != "" {
		data, err := os.ReadFile(keyFile)
		if err != nil {
			return err
		}
		key = strings.TrimRight(string(data), "\r\n")
	} else {
		random := make([]byte, 20)
		if _, err := rand.Read(random); err != nil {
			return err
		}
		// The key is stored as shown, dashes included: what the customer reads
		// out is what opens the repository.
		key = group(strings.ToLower(base32.StdEncoding.EncodeToString(random)))
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
