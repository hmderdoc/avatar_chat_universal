package avatar

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/nickstyle"
)

// SynchronetStore reads and writes a Synchronet user's real, board-wide
// records, so the door never asks for an avatar the user already has and a
// change made in the door is their BBS avatar everywhere:
//
//   - avatar: the [avatar] section of <data>/user/NNNN.ini (avatar_lib.js
//     localuser_fname), edited in place with every other line kept;
//   - handle style: <data>/chat/nickstyle/NNNN.json (chat_style_lib.js),
//     shared with the shell and the website.
//
// The username argument is ignored: records are keyed by the user number
// from the drop file.
type SynchronetStore struct {
	DataDir string // Synchronet's data dir (main.ini [dir] data)
	UserNum int
}

var _ Backend = (*SynchronetStore)(nil)

func (s *SynchronetStore) iniPath() string {
	return filepath.Join(s.DataDir, "user", fmt.Sprintf("%04d.ini", s.UserNum))
}

func (s *SynchronetStore) stylePath() string {
	return filepath.Join(s.DataDir, "chat", "nickstyle", fmt.Sprintf("%04d.json", s.UserNum))
}

// Read parses the [avatar] section. No section, or empty data without
// disabled, is (nil, nil); an unparseable date is ignored.
func (s *SynchronetStore) Read(string) (*Record, error) {
	text, err := readText(s.iniPath())
	if err != nil {
		return nil, err
	}
	vals, ok := readINISection(text, "avatar")
	if !ok {
		return nil, nil
	}
	rec := &Record{
		Disabled: parseBool(vals["disabled"]),
		Created:  parseStamp(vals["created"]),
		Updated:  parseStamp(vals["updated"]),
	}
	if d := vals["data"]; d != "" {
		a, err := FromBase64(d)
		if err != nil {
			return nil, fmt.Errorf("avatar: %s: %v", s.iniPath(), err)
		}
		rec.Avatar = a
	}
	if rec.Avatar == nil && !rec.Disabled {
		return nil, nil
	}
	return rec, nil
}

// Write rewrites only data (when rec carries an avatar), disabled and
// updated, adds created if absent, and leaves export_count, last_exported
// and every other section alone. Times are local, in Synchronet's format,
// so the avatar exporter sees updated > last_exported and sends the change.
func (s *SynchronetStore) Write(_ string, rec *Record) error {
	if rec.Avatar != nil || !rec.Disabled {
		if err := rec.Avatar.Validate(); err != nil {
			return err
		}
	}
	path := s.iniPath()
	text, err := readText(path)
	if err != nil {
		return err
	}
	now := time.Now().Local().Format(SynchronetStampLayout)
	var sets []iniSet
	if rec.Avatar != nil {
		sets = append(sets, iniSet{key: "data", value: rec.Avatar.Base64()})
	}
	sets = append(sets,
		iniSet{key: "disabled", value: fmt.Sprintf("%t", rec.Disabled)},
		iniSet{key: "updated", value: now},
		iniSet{key: "created", value: now, onlyIfAbsent: true},
	)
	return writeFileAtomic(path, []byte(writeINISection(text, "avatar", sets)), 0600)
}

// Disable flips disabled on the stored record. With nothing stored,
// disabling records the choice; enabling is an error.
func (s *SynchronetStore) Disable(username string, disabled bool) error {
	rec, err := s.Read(username)
	if err != nil {
		return err
	}
	if rec == nil {
		if !disabled {
			return fmt.Errorf("avatar: no record for user #%d", s.UserNum)
		}
		rec = &Record{}
	}
	rec.Disabled = disabled
	return s.Write(username, rec)
}

// ReadStyle reads the shared nickstyle record (empty when none).
func (s *SynchronetStore) ReadStyle(string) (*nickstyle.Style, error) {
	empty := &nickstyle.Style{Colors: []string{}}
	b, err := os.ReadFile(s.stylePath())
	if err != nil {
		if os.IsNotExist(err) {
			return empty, nil
		}
		return nil, fmt.Errorf("avatar: read %s: %v", s.stylePath(), err)
	}
	var st nickstyle.Style
	if json.Unmarshal(b, &st) != nil {
		return empty, nil // chat_style_lib.js treats bad JSON as no record
	}
	return &st, nil
}

// WriteStyle stores the record the way chat_style_lib.js set() does:
// normalized, {"version":1,...} indented one space, temp file and rename;
// an empty style removes the file.
func (s *SynchronetStore) WriteStyle(_ string, style *nickstyle.Style) error {
	path := s.stylePath()
	clean := nickstyle.Normalize(*style)
	if clean.IsEmpty() {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("avatar: remove %s: %v", path, err)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("avatar: mkdir: %v", err)
	}
	rec := struct {
		Version int           `json:"version"`
		Colors  []string      `json:"colors"`
		Tag     nickstyle.Tag `json:"tag"`
	}{nickstyle.Version, clean.Colors, clean.Tag}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", " ")
	if err := enc.Encode(rec); err != nil {
		return err
	}
	// JSON.stringify writes no trailing newline.
	return writeFileAtomic(path, bytes.TrimRight(buf.Bytes(), "\n"), 0644)
}

// readText returns the file's contents, or "" when it does not exist.
func readText(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("avatar: read %s: %v", path, err)
	}
	return string(b), nil
}

// writeFileAtomic writes via a temp file in the same directory and renames
// it over path, keeping the existing file's mode (or using mode for a new
// file).
func writeFileAtomic(path string, data []byte, mode fs.FileMode) error {
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp*")
	if err != nil {
		return fmt.Errorf("avatar: create temp for %s: %v", path, err)
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("avatar: write %s: %v", tmp, err)
	}
	if err := f.Chmod(mode); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("avatar: chmod %s: %v", tmp, err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("avatar: close %s: %v", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("avatar: rename %s: %v", path, err)
	}
	return nil
}
