package ignore

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func memList() (*List, *string) {
	var stored string
	return &List{
		Load: func() string { return stored },
		Save: func(v string) error { stored = v; return nil },
	}, &stored
}

func TestAddMatchRemove(t *testing.T) {
	l, stored := memList()
	if ok, err := l.Add("  Bob "); !ok || err != nil {
		t.Fatalf("Add = %v %v", ok, err)
	}
	if ok, _ := l.Add("BOB"); ok {
		t.Fatal("second add of the same handle")
	}
	if !l.Hidden("bob", "hi") || !l.Hidden("x", "<BoB> relayed") || l.Hidden("carol", "hi") {
		t.Fatal("matching wrong")
	}
	// Survives a reload from the stored value.
	l2 := &List{Load: func() string { return *stored }}
	if !l2.Hidden("Bob", "") {
		t.Fatalf("reload lost the entry: %q", *stored)
	}
	if removed, shared, _ := l.Remove("bob"); !removed || shared {
		t.Fatalf("Remove = %v %v", removed, shared)
	}
	if l.Hidden("bob", "") || *stored != "" {
		t.Fatalf("still hidden, stored %q", *stored)
	}
	if _, err := l.Add("   "); err == nil {
		t.Fatal("empty name accepted")
	}
}

func TestSharedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shell_prefs.json")
	// Byte 0xb0 is the kind of non-UTF-8 the real file carries.
	body := "{\"users\":{\"user-7\":{\"notifications\":{\"ignored\":[" +
		"{\"handle\":\"spammer\",\"label\":\"Spammer\",\"network\":\"\",\"hideChat\":true}," +
		"{\"handle\":\"mrcguy\",\"network\":\"mrc\",\"hideChat\":true}," +
		"{\"handle\":\"quiet\",\"network\":\"local\",\"hideChat\":false}," +
		"\"Legacy\"]}},\"user-8\":{\"x\":\"\xb0\"}}}"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	l, stored := memList()
	l.SharedPath, l.SharedUser = path, "user-7"
	if !l.Hidden("SPAMMER", "") || !l.Hidden("legacy", "") {
		t.Fatal("shared entries not applied")
	}
	if l.Hidden("mrcguy", "") {
		t.Fatal("an MRC-only entry applied to this chat")
	}
	if l.Hidden("quiet", "") {
		t.Fatal("a notify-only entry hid the line")
	}
	if e, ok := l.Match("quiet", ""); !ok || e.HideChat {
		t.Fatal("notify-only entry not matched for muting")
	}
	if removed, shared, _ := l.Remove("spammer"); removed || !shared {
		t.Fatalf("Remove of a shared entry = %v %v", removed, shared)
	}
	if ok, _ := l.Add("spammer"); ok || *stored != "" {
		t.Fatal("re-adding a shared entry wrote the door's list")
	}
	// The shell edits its file: picked up after the recheck interval.
	os.WriteFile(path, []byte(`{"users":{"user-7":{"notifications":{"ignored":[]}}}}`), 0o644)
	l.sharedSeen = time.Now().Add(-sharedEvery)
	if l.Hidden("spammer", "") {
		t.Fatal("stale shared list")
	}
	// A missing file just means no shared entries.
	l.SharedPath = filepath.Join(t.TempDir(), "gone.json")
	l.sharedSeen = time.Time{}
	if l.Hidden("legacy", "") {
		t.Fatal("entries from a missing file")
	}
}
