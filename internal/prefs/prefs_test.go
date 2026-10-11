package prefs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetGetKeepsOtherKeys(t *testing.T) {
	s := &Store{Root: t.TempDir(), BBSID: "Test BBS"}
	if got := s.Get("Bob", "theme"); got != "" {
		t.Fatalf("unset = %q", got)
	}
	if err := s.Set("Bob", "theme", "amber"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("bob", "future_key", "x"); err != nil {
		t.Fatal(err)
	}
	if got := s.Get("BOB", "theme"); got != "amber" {
		t.Fatalf("theme = %q", got)
	}
	if err := s.Set("Bob", "theme", ""); err != nil {
		t.Fatal(err)
	}
	if s.Get("Bob", "theme") != "" || s.Get("Bob", "future_key") != "x" {
		t.Fatal("clearing theme lost or kept the wrong key")
	}
	if _, err := os.Stat(filepath.Join(s.Root, "prefs", "test_bbs", "bob.ini")); err != nil {
		t.Fatal(err)
	}
}

func TestRejectsLineBreaks(t *testing.T) {
	s := &Store{Root: t.TempDir()}
	if err := s.Set("../../x", "theme", "a\nadmin = 1"); err == nil {
		t.Fatal("line break accepted")
	}
	if p := s.path("../../x"); !strings.HasPrefix(p, filepath.Join(s.Root, "prefs")) || strings.Contains(p, "..") {
		t.Fatalf("path escaped: %s", p)
	}
}
