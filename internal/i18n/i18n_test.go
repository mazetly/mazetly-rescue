package i18n

import (
	"strings"
	"testing"
)

// Both languages must take the same arguments, in the same order.
func TestTranslationsMatch(t *testing.T) {
	for key, m := range messages {
		if m[0] == "" || m[1] == "" {
			t.Errorf("%s: missing a language", key)
		}
		if verbs(m[0]) != verbs(m[1]) {
			t.Errorf("%s: %q and %q differ in their arguments", key, m[0], m[1])
		}
	}
}

func verbs(s string) string {
	var b strings.Builder
	for i := 0; i < len(s)-1; i++ {
		if s[i] == '%' {
			b.WriteByte(s[i+1])
			i++
		}
	}
	return b.String()
}

func TestT(t *testing.T) {
	defer func(was bool) { french = was }(french)
	french = false
	if got := T("backup.done", "matomo", "5.1"); got != "Backup of matomo 5.1 done." {
		t.Errorf("English: %q", got)
	}
	french = true
	if got := T("backup.done", "matomo", "5.1"); got != "Sauvegarde de matomo 5.1 terminée." {
		t.Errorf("French: %q", got)
	}
}

func TestTPanicsOnAnUnknownKey(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("no panic")
		}
	}()
	T("no.such.message")
}

func TestIsFrench(t *testing.T) {
	for _, c := range []struct {
		all, messages, lang string
		want                bool
	}{
		{"", "", "fr_FR.UTF-8", true},
		{"", "", "en_US.UTF-8", false},
		{"", "", "", false},
		{"", "fr_BE.UTF-8", "en_US.UTF-8", true},
		{"C", "fr_FR.UTF-8", "fr_FR.UTF-8", false},
	} {
		t.Setenv("LC_ALL", c.all)
		t.Setenv("LC_MESSAGES", c.messages)
		t.Setenv("LANG", c.lang)
		if got := isFrench(); got != c.want {
			t.Errorf("LC_ALL=%q LC_MESSAGES=%q LANG=%q: %v", c.all, c.messages, c.lang, got)
		}
	}
}
