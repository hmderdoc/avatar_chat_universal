// Package denylist is a sysop-maintained list of addresses the bundled chat
// server refuses: one IP address or CIDR range per line, # starts a comment.
// The file is re-read when its modification time or size changes, so an
// edit takes effect within a second without restarting anything.
//
//	# spammer from last week
//	203.0.113.7
//	198.51.100.0/24
//	2001:db8::/32
package denylist

import (
	"bufio"
	"log"
	"net"
	"net/netip"
	"os"
	"strings"
	"sync"
	"time"
)

// recheck is how often Denied may stat the file.
const recheck = time.Second

// List is safe for concurrent use. A nil *List denies nothing.
type List struct {
	path   string
	logger *log.Logger
	now    func() time.Time

	mu       sync.Mutex
	checked  time.Time
	mtime    time.Time
	size     int64
	missing  bool
	prefixes []netip.Prefix
}

// New returns a List backed by path. A missing file is an empty list, not an
// error, so a sysop can point at a file before creating it. logger may be nil.
func New(path string, logger *log.Logger) *List {
	if logger == nil {
		logger = log.Default()
	}
	l := &List{path: path, logger: logger, now: time.Now}
	l.mu.Lock()
	l.reloadLocked()
	l.mu.Unlock()
	return l
}

// Denied reports whether addr is on the list. addr may be "host", "host:port"
// or "[v6]:port", as net.Conn.RemoteAddr().String() and
// http.Request.RemoteAddr give it. Anything unparseable is allowed.
func (l *List) Denied(addr string) bool {
	if l == nil {
		return false
	}
	ip, ok := parseAddr(addr)
	if !ok {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if now := l.now(); now.Sub(l.checked) >= recheck {
		l.checked = now
		l.reloadLocked()
	}
	for _, p := range l.prefixes {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// Len is the number of entries loaded, for logs and tests.
func (l *List) Len() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.prefixes)
}

// reloadLocked re-reads the file when it changed since the last read.
func (l *List) reloadLocked() {
	st, err := os.Stat(l.path)
	if err != nil {
		if !l.missing {
			l.logger.Printf("deny list %s: %v (allowing everyone until it exists)", l.path, err)
		}
		l.missing, l.prefixes, l.mtime, l.size = true, nil, time.Time{}, 0
		return
	}
	if !l.missing && st.ModTime().Equal(l.mtime) && st.Size() == l.size {
		return
	}
	f, err := os.Open(l.path)
	if err != nil {
		l.logger.Printf("deny list %s: %v (keeping the previous list)", l.path, err)
		return
	}
	defer f.Close()
	var out []netip.Prefix
	sc := bufio.NewScanner(f)
	n := 0
	for sc.Scan() {
		n++
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		p, ok := parseEntry(line)
		if !ok {
			l.logger.Printf("deny list %s:%d: not an address or CIDR range: %q", l.path, n, line)
			continue
		}
		out = append(out, p)
	}
	l.missing, l.prefixes, l.mtime, l.size = false, out, st.ModTime(), st.Size()
	l.logger.Printf("deny list %s: %d entries", l.path, len(out))
}

func parseEntry(s string) (netip.Prefix, bool) {
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return netip.Prefix{}, false
		}
		return p.Masked(), true
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, false
	}
	a = a.Unmap()
	return netip.PrefixFrom(a, a.BitLen()), true
}

func parseAddr(s string) (netip.Addr, bool) {
	host := s
	if h, _, err := net.SplitHostPort(s); err == nil {
		host = h
	}
	if i := strings.IndexByte(host, '%'); i >= 0 {
		host = host[:i] // zone
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return a.Unmap(), true
}
