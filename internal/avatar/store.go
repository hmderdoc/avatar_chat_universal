package avatar

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/nickstyle"
)

// Record is the per-user persisted state. Mirrors the [avatar] section of
// Synchronet's user INI files (avatar_lib.js:109-119). Avatar may be nil
// when Disabled is set: "no avatar" is a settled choice, not a missing one.
type Record struct {
	Avatar   Avatar
	Disabled bool
	Created  time.Time
	Updated  time.Time
}

// Backend is where a user's avatar and handle style live. Store is the
// door's own private store (any BBS, standalone); SynchronetStore reads and
// writes the Synchronet user's real board-wide records.
//
// Read returns (nil, nil) when the user has made no choice yet. ReadStyle
// returns an empty style (never nil) when none is set.
type Backend interface {
	Read(username string) (*Record, error)
	Write(username string, rec *Record) error
	Disable(username string, disabled bool) error
	ReadStyle(username string) (*nickstyle.Style, error)
	WriteStyle(username string, style *nickstyle.Style) error
}

var _ Backend = (*Store)(nil)

// Store persists Records to disk under <Root>/users/<BBSID>/<username>.ini.
// Username is lowercased and filesystem-sanitized for the path; the BBSID
// namespacing prevents collisions when one binary serves multiple BBSes.
// The file holds an [avatar] section and, once a style is set, a [style]
// section; each write keeps the other section.
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
	rec, _, err := s.load(username)
	return rec, err
}

// ReadStyle returns the user's handle style from the [style] section.
func (s *Store) ReadStyle(username string) (*nickstyle.Style, error) {
	_, style, err := s.load(username)
	return style, err
}

// load parses both sections. A missing file is (nil, empty style, nil).
func (s *Store) load(username string) (*Record, *nickstyle.Style, error) {
	path := s.path(username)
	empty := &nickstyle.Style{Colors: []string{}}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, empty, nil
		}
		return nil, nil, fmt.Errorf("avatar: open %s: %v", path, err)
	}
	defer f.Close()

	rec := &Record{}
	var raw nickstyle.Style
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 256*1024)
	section := ""
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(strings.TrimSpace(line[1 : len(line)-1]))
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(line[:eq]))
		val := strings.TrimSpace(line[eq+1:])
		switch section {
		case "avatar":
			switch key {
			case "data":
				if val == "" {
					continue
				}
				a, err := FromBase64(val)
				if err != nil {
					return nil, nil, fmt.Errorf("avatar: %s: %v", path, err)
				}
				rec.Avatar = a
			case "disabled":
				rec.Disabled = parseBool(val)
			case "created":
				rec.Created = parseStamp(val)
			case "updated":
				rec.Updated = parseStamp(val)
			}
		case "style":
			switch key {
			case "colors":
				raw.Colors = strings.Split(val, ",")
			case "tag_text":
				raw.Tag.Text = val
			case "tag_fg":
				raw.Tag.Fg = val
			case "tag_bg":
				raw.Tag.Bg = val
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, fmt.Errorf("avatar: scan %s: %v", path, err)
	}
	style := nickstyle.Normalize(raw)
	if rec.Avatar == nil && !rec.Disabled {
		return nil, &style, nil
	}
	return rec, &style, nil
}

// Write atomically replaces the user's avatar record on disk, keeping any
// stored style. Created is kept from the existing record (or set now);
// Updated is always now. A nil Avatar is allowed only with Disabled.
func (s *Store) Write(username string, rec *Record) error {
	if rec.Avatar != nil || !rec.Disabled {
		if err := rec.Avatar.Validate(); err != nil {
			return err
		}
	}
	old, style, err := s.load(username)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if rec.Created.IsZero() {
		if old != nil && !old.Created.IsZero() {
			rec.Created = old.Created
		} else {
			rec.Created = now
		}
	}
	rec.Updated = now
	return s.save(username, rec, style)
}

// WriteStyle replaces the user's handle style, keeping the avatar record.
func (s *Store) WriteStyle(username string, style *nickstyle.Style) error {
	rec, _, err := s.load(username)
	if err != nil {
		return err
	}
	clean := nickstyle.Normalize(*style)
	return s.save(username, rec, &clean)
}

func (s *Store) save(username string, rec *Record, style *nickstyle.Style) error {
	path := s.path(username)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("avatar: mkdir: %v", err)
	}

	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("avatar: create %s: %v", tmp, err)
	}
	w := bufio.NewWriter(f)
	if rec != nil {
		fmt.Fprintln(w, "[avatar]")
		data := ""
		if rec.Avatar != nil {
			data = rec.Avatar.Base64()
		}
		fmt.Fprintf(w, "data     = %s\n", data)
		fmt.Fprintf(w, "disabled = %t\n", rec.Disabled)
		fmt.Fprintf(w, "created  = %s\n", rec.Created.UTC().Format(time.RFC3339))
		fmt.Fprintf(w, "updated  = %s\n", rec.Updated.UTC().Format(time.RFC3339))
	}
	if style != nil && !style.IsEmpty() {
		if rec != nil {
			fmt.Fprintln(w)
		}
		fmt.Fprintln(w, "[style]")
		fmt.Fprintf(w, "colors   = %s\n", strings.Join(style.Colors, ","))
		fmt.Fprintf(w, "tag_text = %s\n", style.Tag.Text)
		fmt.Fprintf(w, "tag_fg   = %s\n", style.Tag.Fg)
		fmt.Fprintf(w, "tag_bg   = %s\n", style.Tag.Bg)
	}
	if err := w.Flush(); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("avatar: flush: %v", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("avatar: close: %v", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("avatar: rename: %v", err)
	}
	return nil
}

// Disable toggles the user's avatar off without losing the bytes. With no
// stored record, disabling still records the choice (so the door stops
// asking); enabling with nothing stored is an error.
func (s *Store) Disable(username string, disabled bool) error {
	rec, err := s.Read(username)
	if err != nil {
		return err
	}
	if rec == nil {
		if !disabled {
			return fmt.Errorf("avatar: no record for %q", username)
		}
		rec = &Record{}
	}
	rec.Disabled = disabled
	return s.Write(username, rec)
}

// parseStamp reads the door's RFC 3339 timestamps and Synchronet's
// iniSetObject Date format ("Apr  9 2026 09:41:26", local time). Anything
// else is the zero time, never an error.
func parseStamp(val string) time.Time {
	if t, err := time.Parse(time.RFC3339, val); err == nil {
		return t
	}
	if t, err := time.ParseInLocation(SynchronetStampLayout, val, time.Local); err == nil {
		return t
	}
	return time.Time{}
}

// SynchronetStampLayout is how Synchronet's ini writer spells a JS Date:
// month, space-padded day, year, 24-hour time, local zone.
const SynchronetStampLayout = "Jan _2 2006 15:04:05"

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
