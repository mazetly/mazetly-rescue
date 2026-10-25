// Package fakedocker puts a fake docker first on PATH, for the tests of the
// code that drives Docker. Each call is recorded; the first rule whose pattern
// appears in its arguments decides what it prints and how it exits, and a call
// no rule matches succeeds silently.
package fakedocker

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const script = `#!/bin/sh
d=%q
n=$(( $(cat "$d/n" 2>/dev/null || echo 0) + 1 ))
echo "$n" > "$d/n"
printf '%%s\0' "$@" > "$d/$n.args"
env -0 > "$d/$n.env"
cat > "$d/$n.stdin"
i=0
while [ -e "$d/rule$i.pattern" ]; do
	pattern=$(cat "$d/rule$i.pattern")
	case " $* " in
	*"$pattern"*)
		cat "$d/rule$i.stdout"
		cat "$d/rule$i.stderr" >&2
		exit "$(cat "$d/rule$i.code")"
		;;
	esac
	i=$((i + 1))
done
`

type Fake struct {
	t     testing.TB
	dir   string
	rules int
}

// Call is one run of the fake docker.
type Call struct {
	Args  []string
	Env   map[string]string
	Stdin string
}

// Line is the arguments joined by spaces, for matching in tests.
func (c Call) Line() string { return strings.Join(c.Args, " ") }

// Install puts the fake docker first on PATH for the rest of the test.
func Install(t testing.TB) *Fake {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(fmt.Sprintf(script, dir)), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return &Fake{t: t, dir: dir}
}

// Reply makes the calls whose arguments contain pattern print stdout and
// stderr, then exit with code. Rules are tried in the order they were added.
func (f *Fake) Reply(pattern, stdout, stderr string, code int) {
	f.t.Helper()
	base := filepath.Join(f.dir, "rule"+strconv.Itoa(f.rules))
	// The pattern goes last: the script only considers a rule once it exists.
	for _, file := range []struct{ ext, content string }{
		{".stdout", stdout}, {".stderr", stderr}, {".code", strconv.Itoa(code)}, {".pattern", pattern},
	} {
		if err := os.WriteFile(base+file.ext, []byte(file.content), 0o644); err != nil {
			f.t.Fatal(err)
		}
	}
	f.rules++
}

// Calls returns the calls made so far, in order.
func (f *Fake) Calls() []Call {
	f.t.Helper()
	var calls []Call
	for n := 1; ; n++ {
		prefix := filepath.Join(f.dir, strconv.Itoa(n))
		args, err := os.ReadFile(prefix + ".args")
		if os.IsNotExist(err) {
			return calls
		}
		if err != nil {
			f.t.Fatal(err)
		}
		env, err := os.ReadFile(prefix + ".env")
		if err != nil {
			f.t.Fatal(err)
		}
		stdin, err := os.ReadFile(prefix + ".stdin")
		if err != nil {
			f.t.Fatal(err)
		}
		c := Call{Args: split(args), Env: map[string]string{}, Stdin: string(stdin)}
		for _, kv := range split(env) {
			name, value, _ := strings.Cut(kv, "=")
			c.Env[name] = value
		}
		calls = append(calls, c)
	}
}

// Last returns the latest call, failing the test when there is none.
func (f *Fake) Last() Call {
	f.t.Helper()
	calls := f.Calls()
	if len(calls) == 0 {
		f.t.Fatal("docker was not called")
	}
	return calls[len(calls)-1]
}

func split(nulTerminated []byte) []string {
	var out []string
	for _, field := range bytes.Split(nulTerminated, []byte{0}) {
		out = append(out, string(field))
	}
	// The last field is what follows the final NUL: nothing.
	return out[:len(out)-1]
}
