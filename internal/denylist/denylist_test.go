package denylist

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func quiet() *log.Logger { return log.New(io.Discard, "", 0) }

func TestDenied(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deny.txt")
	body := "# comment\n203.0.113.7\n198.51.100.0/24  # a range\n\nnot-an-ip\n2001:db8::/32\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	l := New(path, quiet())
	if l.Len() != 3 {
		t.Fatalf("len = %d, want 3 (the bad line skipped)", l.Len())
	}
	cases := map[string]bool{
		"203.0.113.7":            true,
		"203.0.113.7:4455":       true,
		"203.0.113.8:1":          false,
		"198.51.100.200:80":      true,
		"[2001:db8::1]:10088":    true,
		"[2001:db9::1]:10088":    false,
		"[::ffff:203.0.113.7]:1": true, // v4-mapped
		"garbage":                false,
		"":                       false,
	}
	for addr, want := range cases {
		if got := l.Denied(addr); got != want {
			t.Errorf("Denied(%q) = %v, want %v", addr, got, want)
		}
	}
}

func TestReloadOnChangeAndMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deny.txt")
	clock := time.Unix(1000, 0)
	l := New(path, quiet())
	l.now = func() time.Time { return clock }
	if l.Denied("192.0.2.1") {
		t.Fatal("missing file should deny nobody")
	}
	if err := os.WriteFile(path, []byte("192.0.2.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(2 * time.Second)
	if !l.Denied("192.0.2.1:5") {
		t.Fatal("new file not picked up")
	}
	// Edit within the recheck window: not seen yet, then seen.
	if err := os.WriteFile(path, []byte("192.0.2.2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, clock.Add(time.Hour), clock.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if !l.Denied("192.0.2.1") {
		t.Fatal("re-read before the recheck interval")
	}
	clock = clock.Add(2 * time.Second)
	if l.Denied("192.0.2.1") || !l.Denied("192.0.2.2") {
		t.Fatal("edited file not reloaded")
	}
	os.Remove(path)
	clock = clock.Add(2 * time.Second)
	if l.Denied("192.0.2.2") {
		t.Fatal("deleted file should deny nobody")
	}
}

func TestNilList(t *testing.T) {
	var l *List
	if l.Denied("192.0.2.1") || l.Len() != 0 {
		t.Fatal("nil list must deny nothing")
	}
}
