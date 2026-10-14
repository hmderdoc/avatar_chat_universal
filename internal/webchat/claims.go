package webchat

import (
	"bufio"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Name claims: on this server a nickname belongs to the first browser that
// uses it. The page makes a random key once (kept in localStorage) and
// sends it with the name in a SOCKET CLAIM packet before anything else;
// the server keeps sha256(key) per lowercased name. Another browser with
// the same name and no key, or the wrong one, is refused, so it can never
// read that name's mailbox or its history.
//
// A name a door or bridge uses on the local chat server (or used lately:
// chatserver.Server.BBSName) is not given to a new browser, so a guest
// can't pass as a BBS user to the doors or receive their PMs. A browser
// already holding it keeps it.
//
// The file is one claim per line, "<sha256 hex> <name>", so a sysop frees
// a name by deleting its line (the server re-reads the file when it
// changes). A browser whose storage was cleared loses its name until then.

// claimResult is what a CLAIM gets.
type claimResult int

const (
	claimOK claimResult = iota
	claimTaken
	claimLimit    // this address made too many new claims today
	claimReserved // a door or bridge uses this name
	claimError
)

type claims struct {
	path   string
	perDay int // new claims per address per day
	// reserved, when set, reports a name no new claim may take.
	reserved func(name string) bool

	mu      sync.Mutex
	byName  map[string]string // lowercase name -> sha256 hex
	modTime time.Time
	size    int64
	newBy   map[string]int // ip -> new claims since day
	day     string
	now     func() time.Time
}

func newClaims(path string, perDay int, now func() time.Time) *claims {
	return &claims{path: path, perDay: perDay, byName: map[string]string{}, newBy: map[string]int{}, now: now}
}

func hashKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// reload re-reads the file when it changed (a sysop edit). Callers hold mu.
func (c *claims) reload() {
	if c.path == "" {
		return
	}
	fi, err := os.Stat(c.path)
	if err != nil {
		if os.IsNotExist(err) && !c.modTime.IsZero() {
			c.byName, c.modTime, c.size = map[string]string{}, time.Time{}, 0
		}
		return
	}
	if fi.ModTime().Equal(c.modTime) && fi.Size() == c.size {
		return
	}
	f, err := os.Open(c.path)
	if err != nil {
		return
	}
	defer f.Close()
	next := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		hash, name, ok := strings.Cut(line, " ")
		name = strings.ToLower(strings.TrimSpace(name))
		if !ok || len(hash) != 64 || name == "" {
			continue
		}
		next[name] = strings.ToLower(hash)
	}
	if sc.Err() != nil {
		return
	}
	c.byName, c.modTime, c.size = next, fi.ModTime(), fi.Size()
}

// claim checks name for key, claiming it when it's free.
func (c *claims) claim(name, key, ip string) claimResult {
	if len(key) < 32 || len(key) > 128 {
		return claimError
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reload()
	lower := strings.ToLower(name)
	want := hashKey(key)
	if have, ok := c.byName[lower]; ok {
		if subtle.ConstantTimeCompare([]byte(have), []byte(want)) == 1 {
			return claimOK
		}
		return claimTaken
	}
	if c.reserved != nil && c.reserved(name) {
		return claimReserved
	}
	if day := c.now().UTC().Format("2006-01-02"); day != c.day {
		c.day, c.newBy = day, map[string]int{}
	}
	if c.perDay > 0 && c.newBy[ip] >= c.perDay {
		return claimLimit
	}
	c.byName[lower] = want
	if err := c.save(); err != nil {
		delete(c.byName, lower)
		return claimError
	}
	c.newBy[ip]++
	return claimOK
}

// save writes the file atomically. Callers hold mu.
func (c *claims) save() error {
	if c.path == "" {
		return nil // in memory only (tests)
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("# web chat name claims: <sha256 of the browser's key> <name>. Delete a line to free that name.\n")
	for name, hash := range c.byName {
		b.WriteString(hash + " " + name + "\n")
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, c.path); err != nil {
		return err
	}
	if fi, err := os.Stat(c.path); err == nil {
		c.modTime, c.size = fi.ModTime(), fi.Size()
	}
	return nil
}
