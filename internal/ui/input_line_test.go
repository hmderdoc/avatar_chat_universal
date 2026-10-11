package ui

import (
	"testing"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
)

func tabLine(text string, nicks ...string) *InputLine {
	i := &InputLine{MaxLength: 500, historyIdx: -1}
	i.CompletionProvider = func() []string { return nicks }
	i.buf = []rune(text)
	return i
}

func TestTabCompletion(t *testing.T) {
	tab := ansi.Key{Type: ansi.KeyTab}
	for _, tc := range []struct {
		text, want string
	}{
		{"hi be", "hi Beta Tester"},
		{"hi Beta T", "hi Beta Tester"},    // a name with a space
		{"beta tester x", "beta tester x"}, // no match: unchanged
		{"hi ", "hi "},                     // nothing to complete
		{"", ""},
		{"Al", "Alice"},
		{"  al", "  Alice"},
	} {
		i := tabLine(tc.text, "Beta Tester", "Alice", "bob")
		i.HandleKey(tab)
		if got := string(i.buf); got != tc.want {
			t.Errorf("%q + Tab = %q, want %q", tc.text, got, tc.want)
		}
	}

	// Repeated Tab cycles; another key ends the cycle.
	i := tabLine("hi b", "Bob", "Beta Tester")
	i.HandleKey(tab)
	if got := string(i.buf); got != "hi Beta Tester" {
		t.Fatalf("first Tab = %q", got)
	}
	i.HandleKey(tab)
	if got := string(i.buf); got != "hi Bob" {
		t.Fatalf("second Tab = %q", got)
	}
	i.HandleKey(ansi.Key{Type: ansi.KeyChar, Rune: ' '})
	if got := string(i.buf); got != "hi Bob " {
		t.Fatalf("after space = %q", got)
	}
}
