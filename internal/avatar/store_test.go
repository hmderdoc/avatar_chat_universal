package avatar

import (
	"bytes"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Root: dir, BBSID: "TestBBS"}

	a := Avatar(validBytes())
	if err := s.Write("Alice", &Record{Avatar: a}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := s.Read("Alice")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got == nil {
		t.Fatal("Read returned nil record")
	}
	if !bytes.Equal(got.Avatar, a) {
		t.Error("avatar bytes did not round-trip")
	}
	if got.Disabled {
		t.Error("disabled should default false")
	}
	if got.Created.IsZero() || got.Updated.IsZero() {
		t.Error("timestamps should be set on write")
	}
}

func TestStoreCaseInsensitiveUsername(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Root: dir, BBSID: "x"}
	a := Avatar(validBytes())
	if err := s.Write("CASEY", &Record{Avatar: a}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Read("casey")
	if err != nil || got == nil {
		t.Fatalf("Read with different case failed: err=%v rec=%v", err, got)
	}
}

func TestStoreReadMissingReturnsNilNoError(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Root: dir, BBSID: "x"}
	got, err := s.Read("nobody")
	if err != nil {
		t.Errorf("read missing: err = %v, want nil", err)
	}
	if got != nil {
		t.Errorf("read missing: got record %+v, want nil", got)
	}
}

func TestStoreNamespacesByBBSID(t *testing.T) {
	dir := t.TempDir()
	s1 := &Store{Root: dir, BBSID: "BBSOne"}
	s2 := &Store{Root: dir, BBSID: "BBSTwo"}
	if err := s1.Write("alice", &Record{Avatar: Avatar(validBytes())}); err != nil {
		t.Fatal(err)
	}
	got, err := s2.Read("alice")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("BBSTwo should not see BBSOne's records, got %+v", got)
	}
}

func TestStoreSanitizesPathSegments(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Root: dir, BBSID: "../etc/passwd"}
	if err := s.Write("../../wat", &Record{Avatar: Avatar(validBytes())}); err != nil {
		t.Fatal(err)
	}
	// Verify every file written sits inside dir, and no path segment contains
	// `..` (i.e. sanitize stripped the traversal attempt).
	var files []string
	if err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			files = append(files, p)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no file written")
	}
	for _, p := range files {
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			t.Errorf("file %s not under %s", p, dir)
			continue
		}
		for _, seg := range strings.Split(rel, string(filepath.Separator)) {
			if seg == ".." || strings.Contains(seg, "..") {
				t.Errorf("path segment %q in %s contains traversal", seg, rel)
			}
		}
	}
}

func TestStoreDisableToggle(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Root: dir, BBSID: "x"}
	if err := s.Write("alice", &Record{Avatar: Avatar(validBytes())}); err != nil {
		t.Fatal(err)
	}
	if err := s.Disable("alice", true); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Read("alice")
	if !got.Disabled {
		t.Error("Disable(true) did not persist")
	}
}
