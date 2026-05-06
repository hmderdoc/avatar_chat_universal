package avatar

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Record is the per-user persisted state. Mirrors the [avatar] section of
// Synchronet's user INI files (avatar_lib.js:109-119).
type Record struct {
	Avatar   Avatar
	Disabled bool
	Created  time.Time
	Updated  time.Time
}

// Store persists Records to disk under <Root>/users/<BBSID>/<username>.ini.
// Username is lowercased and filesystem-sanitized for the path; the BBSID
// namespacing prevents collisions when one binary serves multiple BBSes.
type Store struct {
	Root  string
	BBSID string
}

func (s *Store) path(username string) string {
	return filepath.Join(s.Root, "users", sanitize(s.BBSID), sanitize(strings.ToLower(username))+".ini")
}

// Read returns the user's stored record. Returns (nil, nil) — not an error —
// when the user has no record yet (file missing). Returns an error only on
// I/O failure or invalid stored avatar bytes.
func (s *Store) Read(username string) (*Record, error) {
	path := s.path(username)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("avatar: open %s: %w", path, err)
	}
	defer f.Close()

	rec := &Record{}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 256*1024)
	inSection := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			inSection = strings.EqualFold(line, "[avatar]")
			continue
		}
		if !inSection {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		switch strings.ToLower(key) {
		case "data":
			if val == "" {
				continue
			}
			a, err := FromBase64(val)
			if err != nil {
				return nil, fmt.Errorf("avatar: %s: %w", path, err)
			}
			rec.Avatar = a
		case "disabled":
			rec.Disabled = parseBool(val)
		case "created":
			if t, err := time.Parse(time.RFC3339, val); err == nil {
				rec.Created = t
			}
		case "updated":
			if t, err := time.Parse(time.RFC3339, val); err == nil {
				rec.Updated = t
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("avatar: scan %s: %w", path, err)
	}
	if rec.Avatar == nil {
		return nil, nil
	}
	return rec, nil
}

// Write atomically replaces the user's record on disk. Created/Updated
// timestamps are filled in if zero.
func (s *Store) Write(username string, rec *Record) error {
	if err := rec.Avatar.Validate(); err != nil {
		return err
	}
	now := time.Now().UTC()
	if rec.Created.IsZero() {
		rec.Created = now
	}
	rec.Updated = now

	path := s.path(username)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("avatar: mkdir: %w", err)
	}

	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("avatar: create %s: %w", tmp, err)
	}
	w := bufio.NewWriter(f)
	fmt.Fprintln(w, "[avatar]")
	fmt.Fprintf(w, "data     = %s\n", rec.Avatar.Base64())
	fmt.Fprintf(w, "disabled = %t\n", rec.Disabled)
	fmt.Fprintf(w, "created  = %s\n", rec.Created.UTC().Format(time.RFC3339))
	fmt.Fprintf(w, "updated  = %s\n", rec.Updated.UTC().Format(time.RFC3339))
	if err := w.Flush(); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("avatar: flush: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("avatar: close: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("avatar: rename: %w", err)
	}
	return nil
}

// Disable toggles the user's avatar off without losing the bytes.
func (s *Store) Disable(username string, disabled bool) error {
	rec, err := s.Read(username)
	if err != nil {
		return err
	}
	if rec == nil {
		return fmt.Errorf("avatar: no record for %q", username)
	}
	rec.Disabled = disabled
	return s.Write(username, rec)
}

func parseBool(s string) bool {
	switch strings.ToLower(s) {
	case "true", "yes", "on", "1":
		return true
	}
	return false
}

// sanitize keeps a string safe as a single path segment. Anything outside
// [a-z0-9_-] becomes '_'. Dots are explicitly excluded so a malicious BBSID
// or username can't smuggle in `..` and traverse out of the data dir.
func sanitize(s string) string {
	if s == "" {
		return "_default"
	}
	out := make([]byte, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= '0' && r <= '9',
			r == '_', r == '-':
			out = append(out, byte(r))
		case r >= 'A' && r <= 'Z':
			out = append(out, byte(r+('a'-'A')))
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}
