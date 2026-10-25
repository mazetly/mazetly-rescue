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
