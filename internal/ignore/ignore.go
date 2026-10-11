// Package ignore is the door's per-user ignore list. The user's own entries
// live in the door's prefs; on a system whose shell keeps its own list (the
// futureland shell and website share one JSON file), that list can be read
// too, so someone ignored there is hidden here. The door never writes the
// shared file.
//
// Matching follows the shell's people_lists.ts: handles compare trimmed,
// control characters dropped and lowercased; an entry applies everywhere
// (network "") or to the shell's own chat ("local"); a bridge-relayed line
// "<nick> text" is matched on the nick inside it too.
package ignore

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Max matches social_lib.js's cap on one user's list.
const Max = 200

// Entry is one ignored handle.
type Entry struct {
	Handle string `json:"handle"` // Key form
	Label  string `json:"label"`  // as the user typed it
	// HideChat false means "ignored" only for notifications: the line still
	// shows but never rings. Entries the door adds always hide.
	HideChat bool `json:"hideChat"`
	Shared   bool `json:"-"` // came from the shared file (read-only here)
}

// Key is the comparison form of a handle.
func Key(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) > 60 {
		s = s[:60]
	}
	return s
}

var relayed = regexp.MustCompile(`^\s*<([^<>\s]{1,40})>\s`)

// RelayedSpeaker is the real speaker of a bridge-relayed line, or "".
func RelayedSpeaker(text string) string {
	if m := relayed.FindStringSubmatch(text); m != nil {
		return m[1]
	}
	return ""
}

// List is one user's ignore list.
type List struct {
	// Load and Save read and write the door's own entries (the prefs value).
	Load func() string
	Save func(string) error

	// SharedPath, when set, is the shell's prefs JSON; SharedUser is the
	// "user-<n>" key in it.
	SharedPath string
	SharedUser string

	mu         sync.Mutex
	own        []Entry
	loaded     bool
	shared     []Entry
	sharedMod  time.Time
	sharedSize int64
	sharedSeen time.Time
}

// sharedEvery is how often the shared file's mtime is re-checked.
const sharedEvery = 5 * time.Second

func (l *List) loadOwn() {
	if l.loaded {
		return
	}
	l.loaded = true
	if l.Load == nil {
		return
	}
	var out []Entry
	if raw := strings.TrimSpace(l.Load()); raw != "" {
		_ = json.Unmarshal([]byte(raw), &out)
	}
	l.own = l.own[:0]
	for _, e := range out {
		if e.Handle = Key(e.Handle); e.Handle != "" {
			e.HideChat = true
			l.own = append(l.own, e)
		}
	}
}

// sharedFile is the part of the shell's prefs this reads.
type sharedFile struct {
	Users map[string]struct {
		Notifications struct {
			Ignored []json.RawMessage `json:"ignored"`
		} `json:"notifications"`
	} `json:"users"`
}

func (l *List) loadShared(now time.Time) {
	if l.SharedPath == "" || l.SharedUser == "" || now.Sub(l.sharedSeen) < sharedEvery && !l.sharedSeen.IsZero() {
		return
	}
	l.sharedSeen = now
	fi, err := os.Stat(l.SharedPath)
	if err != nil {
		l.shared = nil
		return
	}
	if fi.ModTime().Equal(l.sharedMod) && fi.Size() == l.sharedSize {
		return
	}
	body, err := os.ReadFile(l.SharedPath)
	if err != nil {
		return
	}
	var f sharedFile
	if json.Unmarshal(body, &f) != nil {
		return // mid-write or damaged: keep the last good copy
	}
	l.sharedMod, l.sharedSize = fi.ModTime(), fi.Size()
	l.shared = l.shared[:0]
	for _, raw := range f.Users[l.SharedUser].Notifications.Ignored {
		e, ok := parseShared(raw)
		if ok {
			l.shared = append(l.shared, e)
		}
	}
}

// parseShared reads one entry the way normalizePeople and social_lib do: a
// bare string is a handle; an object has handle/label, network, hideChat
// (default true for the ignore list).
func parseShared(raw json.RawMessage) (Entry, bool) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		k := Key(s)
		return Entry{Handle: k, Label: s, HideChat: true, Shared: true}, k != ""
	}
	var o struct {
		Handle   string `json:"handle"`
		Label    string `json:"label"`
		Network  string `json:"network"`
		HideChat *bool  `json:"hideChat"`
	}
	if json.Unmarshal(raw, &o) != nil {
		return Entry{}, false
	}
	if o.Network != "" && o.Network != "local" {
		return Entry{}, false // MRC, IRC and the like: not this chat
	}
	h := o.Handle
	if h == "" {
		h = o.Label
	}
	k := Key(h)
	label := o.Label
	if label == "" {
		label = h
	}
	return Entry{Handle: k, Label: label, HideChat: o.HideChat == nil || *o.HideChat, Shared: true}, k != ""
}

// Entries lists everything that applies, own entries first.
func (l *List) Entries() []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.loadOwn()
	l.loadShared(time.Now())
	out := append([]Entry(nil), l.own...)
	return append(out, l.shared...)
}

func (l *List) find(key string) (Entry, bool) {
	var hit Entry
	found := false
	for _, e := range l.own {
		if e.Handle == key {
			return e, true
		}
	}
	for _, e := range l.shared {
		if e.Handle == key {
			// A hiding entry wins over a notify-only one for the same handle.
			if !found || e.HideChat {
				hit, found = e, true
			}
		}
	}
	return hit, found
}

// Match is the entry that applies to a line from sender with body text.
func (l *List) Match(sender, text string) (Entry, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.loadOwn()
	l.loadShared(time.Now())
	if len(l.own) == 0 && len(l.shared) == 0 {
		return Entry{}, false
	}
	if k := Key(sender); k != "" {
		if e, ok := l.find(k); ok {
			return e, true
		}
	}
	if r := RelayedSpeaker(text); r != "" {
		return l.find(Key(r))
	}
	return Entry{}, false
}

// Hidden reports whether a line from sender should be left out.
func (l *List) Hidden(sender, text string) bool {
	e, ok := l.Match(sender, text)
	return ok && e.HideChat
}

// Add puts handle on the user's own list. It reports false when it was
// already there (own or shared, hiding).
func (l *List) Add(handle string) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.loadOwn()
	l.loadShared(time.Now())
	k := Key(handle)
	if k == "" {
		return false, errEmpty
	}
	if e, ok := l.find(k); ok && e.HideChat {
		return false, nil
	}
	if len(l.own) >= Max {
		return false, errFull
	}
	next := append(append([]Entry(nil), l.own...), Entry{Handle: k, Label: strings.TrimSpace(handle), HideChat: true})
	if err := l.save(next); err != nil {
		return false, err
	}
	l.own = next
	return true, nil
}

// Remove takes handle off the user's own list. shared reports that it is
// still ignored through the shared list, which the door can't change.
func (l *List) Remove(handle string) (removed, shared bool, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.loadOwn()
	l.loadShared(time.Now())
	k := Key(handle)
	var next []Entry
	for _, e := range l.own {
		if e.Handle == k {
			removed = true
			continue
		}
		next = append(next, e)
	}
	if removed {
		if err := l.save(next); err != nil {
			return false, false, err
		}
		l.own = next
	}
	for _, e := range l.shared {
		if e.Handle == k && e.HideChat {
			shared = true
		}
	}
	return removed, shared, nil
}

func (l *List) save(entries []Entry) error {
	if l.Save == nil {
		return nil
	}
	if len(entries) == 0 {
		return l.Save("")
	}
	body, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	return l.Save(string(body))
}

type ignoreError string

func (e ignoreError) Error() string { return string(e) }

const (
	errEmpty = ignoreError("no name given")
	errFull  = ignoreError("ignore list is full")
)
