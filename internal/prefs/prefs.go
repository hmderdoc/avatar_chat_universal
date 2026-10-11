// Package prefs keeps a user's door-only preferences (their theme, for
// now) in <Root>/prefs/<BBSID>/<username>.ini. These are settings of this
// door, not of the BBS, so they stay out of the avatar store even in
// Synchronet mode, where that store writes the board's own user records.
//
// The file is flat key = value; unknown keys are kept on rewrite so a
// newer door's settings survive an older one.
package prefs

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Store reads and writes one file per user.
type Store struct {
	Root  string
	BBSID string
}

func (s *Store) path(username string) string {
	return filepath.Join(s.Root, "prefs", sanitize(s.BBSID), sanitize(strings.ToLower(username))+".ini")
}

// Get returns the user's value for key, or "" when unset or unreadable.
func (s *Store) Get(username, key string) string {
	m, _ := s.load(username)
	return m[strings.ToLower(key)]
}

// Set stores key = value for the user; an empty value removes the key.
func (s *Store) Set(username, key, value string) error {
	if strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("prefs: value has a line break")
	}
	m, err := s.load(username)
	if err != nil {
		return err
	}
	key = strings.ToLower(strings.TrimSpace(key))
	if value == "" {
		delete(m, key)
	} else {
		m[key] = value
	}
	return s.save(username, m)
}

func (s *Store) load(username string) (map[string]string, error) {
	m := map[string]string{}
	f, err := os.Open(s.path(username))
	if err != nil {
		if os.IsNotExist(err) {
			return m, nil
		}
		return m, fmt.Errorf("prefs: %v", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == ';' || line[0] == '#' || line[0] == '[' {
			continue
		}
		if eq := strings.IndexByte(line, '='); eq > 0 {
			m[strings.ToLower(strings.TrimSpace(line[:eq]))] = strings.TrimSpace(line[eq+1:])
		}
	}
	return m, sc.Err()
}

func (s *Store) save(username string, m map[string]string) error {
	path := s.path(username)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("prefs: mkdir: %v", err)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("[prefs]\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "%s = %s\n", k, m[k])
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("prefs: %v", err)
	}
	return os.Rename(tmp, path)
}

// sanitize matches the avatar store's file naming.
func sanitize(s string) string {
	if s == "" {
		return "_default"
	}
	out := make([]byte, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
			out = append(out, byte(r))
		case r >= 'A' && r <= 'Z':
			out = append(out, byte(r+('a'-'A')))
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}
