package inlineimg

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Fetcher downloads image links for display. A chat message is written by
// whoever is on the other end, so every fetch is treated as hostile:
//
//   - http and https only, on ports 80 and 443;
//   - every address the host resolves to must be public (no loopback,
//     private, link-local, CGNAT, unique-local, multicast or unspecified
//     ranges), and the connection goes to the address that was checked, so
//     a DNS answer can't change between the check and the dial;
//   - redirects are followed at most MaxRedirects times, each one checked
//     the same way;
//   - the body is capped at MaxBytes and the whole fetch at Timeout.
//
// Successful downloads are kept under CacheDir (named by the URL's SHA-256)
// and the directory is trimmed oldest-first to CacheMax bytes.
type Fetcher struct {
	MaxBytes     int64
	Timeout      time.Duration
	MaxRedirects int
	CacheDir     string // "" disables the disk cache
	CacheMax     int64
	UserAgent    string

	// allow decides which resolved addresses may be dialled; nil means
	// PublicAddr. Tests replace it to reach an httptest server.
	allow func(netip.Addr) bool
	// ports lists the ports a URL may name; nil means 80 and 443.
	ports map[string]bool

	once   sync.Once
	client *http.Client
}

// NewFetcher returns a Fetcher with the door's defaults: 5 MB, 10 s, three
// redirects, a 50 MB cache.
func NewFetcher(cacheDir string) *Fetcher {
	return &Fetcher{
		MaxBytes:     5 << 20,
		Timeout:      10 * time.Second,
		MaxRedirects: 3,
		CacheDir:     cacheDir,
		CacheMax:     50 << 20,
		UserAgent:    "avatar_chat_universal (+https://github.com/hmderdoc/avatar_chat_universal)",
	}
}

// ErrBlocked is returned for a URL or address the guards refuse.
var ErrBlocked = errors.New("inlineimg: address not allowed")

// Fetch returns the body at rawURL, from the cache when present.
func (f *Fetcher) Fetch(ctx context.Context, rawURL string) ([]byte, error) {
	if err := f.checkURL(rawURL); err != nil {
		return nil, err
	}
	if b, ok := f.cached(rawURL); ok {
		return b, nil
	}
	ctx, cancel := context.WithTimeout(ctx, f.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	if f.UserAgent != "" {
		req.Header.Set("User-Agent", f.UserAgent)
	}
	req.Header.Set("Accept", "image/*")
	resp, err := f.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("inlineimg: %s: HTTP %d", rawURL, resp.StatusCode)
	}
	if resp.ContentLength > f.MaxBytes {
		return nil, fmt.Errorf("inlineimg: %s: %d bytes is over the %d byte cap", rawURL, resp.ContentLength, f.MaxBytes)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, f.MaxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > f.MaxBytes {
		return nil, fmt.Errorf("inlineimg: %s: over the %d byte cap", rawURL, f.MaxBytes)
	}
	f.store(rawURL, body)
	return body, nil
}

// Page is an HTML page fetched for a link preview.
type Page struct {
	Body        []byte // at most the maxBytes asked for, cut silently
	FinalURL    string // after redirects
	ContentType string // lower case, parameters included
}

// pageRange asks for the first 256 KB, as the web's link-preview.js does;
// a server that ignores Range is cut off by the read cap instead.
const pageRange = "bytes=0-262143"

// FetchPage fetches an HTML page through the same guards as Fetch (scheme,
// ports, public addresses at dial time, each redirect re-checked). Unlike
// Fetch it accepts a partial (206) reply, keeps only the first maxBytes of
// the body instead of failing, reports the final URL and content type, and
// never touches the image cache.
func (f *Fetcher) FetchPage(ctx context.Context, rawURL string, maxBytes int64) (*Page, error) {
	if err := f.checkURL(rawURL); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, f.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	if f.UserAgent != "" {
		req.Header.Set("User-Agent", f.UserAgent)
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9,*/*;q=0.5")
	req.Header.Set("Range", pageRange)
	resp, err := f.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return nil, fmt.Errorf("inlineimg: %s: HTTP %d", rawURL, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes))
	if err != nil && len(body) == 0 {
		return nil, err
	}
	final := rawURL
	if resp.Request != nil && resp.Request.URL != nil {
		final = resp.Request.URL.String()
	}
	return &Page{Body: body, FinalURL: final, ContentType: strings.ToLower(resp.Header.Get("Content-Type"))}, nil
}

// checkURL applies the scheme and port rules (the address rule runs at dial
// time, where the resolved IP is known).
func (f *Fetcher) checkURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%w: scheme %q", ErrBlocked, u.Scheme)
	}
	if u.User != nil {
		return fmt.Errorf("%w: credentials in URL", ErrBlocked)
	}
	port := u.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[u.Scheme]
	}
	ports := f.ports
	if ports == nil {
		ports = map[string]bool{"80": true, "443": true}
	}
	if !ports[port] {
		return fmt.Errorf("%w: port %s", ErrBlocked, port)
	}
	return nil
}

// httpClient builds the guarded client once; Fetch runs on several
// goroutines at a time.
func (f *Fetcher) httpClient() *http.Client {
	f.once.Do(f.buildClient)
	return f.client
}

func (f *Fetcher) buildClient() {
	allow := f.allow
	if allow == nil {
		allow = PublicAddr
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		Proxy: nil, // never route through an environment proxy: the check is on the real peer
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil {
				return nil, err
			}
			var pick netip.Addr
			for _, ip := range ips {
				ip = ip.Unmap()
				if !allow(ip) {
					return nil, fmt.Errorf("%w: %s resolves to %s", ErrBlocked, host, ip)
				}
				if !pick.IsValid() {
					pick = ip
				}
			}
			if !pick.IsValid() {
				return nil, fmt.Errorf("inlineimg: %s: no address", host)
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(pick.String(), port))
		},
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 8 * time.Second,
		MaxIdleConns:          4,
		IdleConnTimeout:       30 * time.Second,
	}
	f.client = &http.Client{
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > f.MaxRedirects {
				return fmt.Errorf("inlineimg: more than %d redirects", f.MaxRedirects)
			}
			return f.checkURL(req.URL.String())
		},
	}
}

// blockedPrefixes are ranges IsPrivate / IsLoopback and friends don't
// cover but that still aren't the public internet.
var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),       // "this network"
	netip.MustParsePrefix("100.64.0.0/10"),   // carrier-grade NAT
	netip.MustParsePrefix("192.0.0.0/24"),    // IETF protocol assignments
	netip.MustParsePrefix("198.18.0.0/15"),   // benchmarking
	netip.MustParsePrefix("192.0.2.0/24"),    // documentation (TEST-NET-1)
	netip.MustParsePrefix("198.51.100.0/24"), // documentation (TEST-NET-2)
	netip.MustParsePrefix("203.0.113.0/24"),  // documentation (TEST-NET-3)
	netip.MustParsePrefix("240.0.0.0/4"),     // reserved, incl. broadcast
	netip.MustParsePrefix("64:ff9b:1::/48"),  // local-use NAT64
	netip.MustParsePrefix("2001:db8::/32"),   // documentation
	netip.MustParsePrefix("fec0::/10"),       // old site-local
	netip.MustParsePrefix("100::/64"),        // discard-only
}

// PublicAddr reports whether ip is an ordinary public unicast address.
func PublicAddr(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() {
		return false
	}
	for _, p := range blockedPrefixes {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

func (f *Fetcher) cachePath(rawURL string) string {
	sum := sha256.Sum256([]byte(rawURL))
	return filepath.Join(f.CacheDir, hex.EncodeToString(sum[:16])+".img")
}

func (f *Fetcher) cached(rawURL string) ([]byte, bool) {
	if f.CacheDir == "" {
		return nil, false
	}
	p := f.cachePath(rawURL)
	b, err := os.ReadFile(p)
	if err != nil || int64(len(b)) > f.MaxBytes {
		return nil, false
	}
	now := time.Now()
	_ = os.Chtimes(p, now, now) // recently used = kept longest
	return b, true
}

func (f *Fetcher) store(rawURL string, body []byte) {
	if f.CacheDir == "" {
		return
	}
	if err := os.MkdirAll(f.CacheDir, 0o755); err != nil {
		return
	}
	p := f.cachePath(rawURL)
	tmp := p + ".tmp" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return
	}
	if err := os.Rename(tmp, p); err != nil {
		os.Remove(tmp)
		return
	}
	f.trim()
}

// trim deletes the least recently used cache files until the directory is
// under CacheMax.
func (f *Fetcher) trim() { TrimDir(f.CacheDir, ".img", f.CacheMax) }

// TrimDir deletes the least recently modified files ending in ext from dir
// until those files add up to at most max bytes. Other files are left alone.
func TrimDir(dir, ext string, max int64) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type file struct {
		path string
		size int64
		mod  time.Time
	}
	var files []file
	var total int64
	for _, e := range ents {
		if e.IsDir() || filepath.Ext(e.Name()) != ext {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, file{filepath.Join(dir, e.Name()), info.Size(), info.ModTime()})
		total += info.Size()
	}
	if total <= max {
		return
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })
	for _, fl := range files {
		if total <= max {
			break
		}
		if os.Remove(fl.path) == nil {
			total -= fl.size
		}
	}
}
