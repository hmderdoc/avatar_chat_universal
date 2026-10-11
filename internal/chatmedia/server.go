package chatmedia

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/denylist"
)

// Config is the media endpoint's settings. A zero Addr leaves it off.
type Config struct {
	Addr          string // listen address, e.g. ":10089"
	Dir           string // where files are stored
	URLBase       string // public base URL; "" = http://<request host>
	MaxImageMB    int    // per picture, before re-encoding
	MaxANSIMB     int    // per art file
	RetentionDays int    // files older than this are deleted
	MaxStoreMB    int    // total stored; uploads refused when full
	PerIPPerHour  int    // uploads per remote address per hour; 0 = unlimited
	DenyFile      string // server-wide deny list (chat and media); "" = none
}

// Defaults are the web chat's limits where it has one.
func Defaults() Config {
	return Config{
		Addr:          "",
		Dir:           "chatmedia",
		MaxImageMB:    20,
		MaxANSIMB:     1,
		RetentionDays: 7,
		MaxStoreMB:    500,
		PerIPPerHour:  30,
	}
}

// mediaPath is the only shape served: /chatmedia/<date>/<24 hex>.<ext>.
var mediaPath = regexp.MustCompile(`^/chatmedia/(\d{4}-\d{2}-\d{2})/([a-f0-9]{24})\.(png|jpg|gif|ans|bin)$`)

// contentTypes is fixed per extension; nothing is sniffed.
var contentTypes = map[string]string{
	"png": "image/png",
	"jpg": "image/jpeg",
	"gif": "image/gif",
	"ans": "text/plain; charset=IBM437",
	"bin": "application/octet-stream",
}

// Server stores uploads and serves them back.
type Server struct {
	cfg    Config
	Logger *log.Logger
	// Deny, when set, answers 403 to listed addresses before an upload is
	// read or a file is served. A nil list denies nobody.
	Deny *denylist.List
	now  func() time.Time

	mu     sync.Mutex
	used   int64                  // bytes stored
	recent map[string][]time.Time // remote address -> upload times in the last hour
}

// New returns a Server for cfg, creating the store directory and counting
// what is already in it.
func New(cfg Config) (*Server, error) {
	d := Defaults()
	if cfg.Dir == "" {
		cfg.Dir = d.Dir
	}
	if cfg.MaxImageMB <= 0 {
		cfg.MaxImageMB = d.MaxImageMB
	}
	if cfg.MaxANSIMB <= 0 {
		cfg.MaxANSIMB = d.MaxANSIMB
	}
	if cfg.RetentionDays <= 0 {
		cfg.RetentionDays = d.RetentionDays
	}
	if cfg.MaxStoreMB <= 0 {
		cfg.MaxStoreMB = d.MaxStoreMB
	}
	if err := os.MkdirAll(cfg.Dir, 0o755); err != nil {
		return nil, err
	}
	s := &Server{cfg: cfg, Logger: log.Default(), now: time.Now, recent: map[string][]time.Time{}}
	s.used = s.diskUsage()
	return s, nil
}

// Handler serves POST /upload and GET /chatmedia/...
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/upload", s.handleUpload)
	mux.HandleFunc("/chatmedia/", s.handleServe)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Deny.Denied(r.RemoteAddr) {
			s.Logger.Printf("media: refused %s (deny list)", r.RemoteAddr)
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// ListenAndServe runs the endpoint and the purge loop until ctx ends.
func (s *Server) ListenAndServe(ctx context.Context) error {
	hs := &http.Server{
		Addr:              s.cfg.Addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       5 * time.Minute,
		WriteTimeout:      5 * time.Minute,
	}
	go s.purgeLoop(ctx)
	go func() {
		<-ctx.Done()
		sh, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = hs.Shutdown(sh)
	}()
	s.Logger.Printf("media: listening on %s, storing in %s, keeping %d days", s.cfg.Addr, s.cfg.Dir, s.cfg.RetentionDays)
	if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func cors(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	h.Set("Access-Control-Allow-Headers", "Content-Type")
}

func safety(w http.ResponseWriter) {
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Referrer-Policy", "no-referrer")
}

type uploadReply struct {
	URL         string `json:"url,omitempty"`
	Type        string `json:"type,omitempty"`
	ExpiresDays int    `json:"expiresDays,omitempty"`
	Error       string `json:"error,omitempty"`
}

func reply(w http.ResponseWriter, code int, r uploadReply) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(r)
}

// handleUpload takes the raw file as the request body, with its original
// name in ?name= (ANSI art has no magic number, so the name says .ans or
// .bin). The answer is {"url", "type", "expiresDays"} or {"error"}.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	cors(w)
	safety(w)
	switch r.Method {
	case http.MethodOptions:
		w.WriteHeader(http.StatusNoContent)
		return
	case http.MethodPost:
	default:
		reply(w, http.StatusMethodNotAllowed, uploadReply{Error: "POST the file as the request body"})
		return
	}
	ip := remoteIP(r)
	if !s.allow(ip) {
		reply(w, http.StatusTooManyRequests, uploadReply{Error: "too many uploads; try again later"})
		return
	}
	name := r.URL.Query().Get("name")
	limit := int64(s.cfg.MaxImageMB) << 20
	if a := int64(s.cfg.MaxANSIMB) << 20; a > limit {
		limit = a
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		reply(w, http.StatusBadRequest, uploadReply{Error: "could not read the upload"})
		return
	}
	if int64(len(data)) > limit {
		reply(w, http.StatusRequestEntityTooLarge, uploadReply{Error: fmt.Sprintf("file is larger than %d MB", limit>>20)})
		return
	}
	ext, kind, out, err := Normalize(name, data)
	if err != nil {
		reply(w, http.StatusUnsupportedMediaType, uploadReply{Error: err.Error()})
		return
	}
	kindMax := int64(s.cfg.MaxImageMB) << 20
	if kind == KindANSI {
		kindMax = int64(s.cfg.MaxANSIMB) << 20
	}
	if int64(len(data)) > kindMax {
		reply(w, http.StatusRequestEntityTooLarge, uploadReply{Error: fmt.Sprintf("%s files are limited to %d MB", kind, kindMax>>20)})
		return
	}
	rel, err := s.store(ext, out)
	if err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, errStoreFull) {
			code = http.StatusInsufficientStorage
		}
		reply(w, code, uploadReply{Error: err.Error()})
		return
	}
	url := s.publicBase(r) + "/" + rel
	s.Logger.Printf("media: upload from %s name=%q %s %d bytes -> /%s", ip, name, ext, len(out), rel)
	reply(w, http.StatusOK, uploadReply{URL: url, Type: string(kind), ExpiresDays: s.cfg.RetentionDays})
}

var errStoreFull = errors.New("the media store is full; try again after old files expire")

// store writes out under <dir>/<date>/<id>.<ext> and returns the path
// relative to the URL base ("chatmedia/<date>/<id>.<ext>").
func (s *Server) store(ext string, out []byte) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.used+int64(len(out)) > int64(s.cfg.MaxStoreMB)<<20 {
		return "", errStoreFull
	}
	var id [12]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	date := s.now().UTC().Format("2006-01-02")
	dir := filepath.Join(s.cfg.Dir, date)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	file := hex.EncodeToString(id[:]) + "." + ext
	tmp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return "", err
	}
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, file)); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	s.used += int64(len(out))
	return "chatmedia/" + date + "/" + file, nil
}

func (s *Server) publicBase(r *http.Request) string {
	if s.cfg.URLBase != "" {
		return strings.TrimRight(s.cfg.URLBase, "/")
	}
	// A page served from this same host (the web front end) sends its
	// Origin; its scheme says whether a TLS proxy sits in front.
	if o, err := url.Parse(r.Header.Get("Origin")); err == nil && strings.EqualFold(o.Host, r.Host) && (o.Scheme == "https" || o.Scheme == "http") {
		return o.Scheme + "://" + r.Host
	}
	return "http://" + r.Host
}

// handleServe serves stored files and nothing else.
func (s *Server) handleServe(w http.ResponseWriter, r *http.Request) {
	cors(w)
	safety(w)
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	m := mediaPath.FindStringSubmatch(r.URL.Path)
	if m == nil {
		http.NotFound(w, r)
		return
	}
	file := filepath.Join(s.cfg.Dir, m[1], m[2]+"."+m[3])
	f, err := os.Open(file)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", contentTypes[m[3]])
	h.Set("Cache-Control", "public, max-age=86400")
	if m[3] == "ans" || m[3] == "bin" {
		h.Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", m[2]+"."+m[3]))
	}
	http.ServeContent(w, r, "", st.ModTime(), f)
}

// allow records one upload from ip and reports whether it is within the
// hourly limit.
func (s *Server) allow(ip string) bool {
	if s.cfg.PerIPPerHour <= 0 {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cut := s.now().Add(-time.Hour)
	keep := s.recent[ip][:0]
	for _, t := range s.recent[ip] {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	if len(keep) >= s.cfg.PerIPPerHour {
		s.recent[ip] = keep
		return false
	}
	s.recent[ip] = append(keep, s.now())
	return true
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// PurgeLoop purges now and then hourly until ctx ends. ListenAndServe
// runs it; a caller that only mounts Handler on its own listener runs it
// itself.
func (s *Server) PurgeLoop(ctx context.Context) { s.purgeLoop(ctx) }

func (s *Server) purgeLoop(ctx context.Context) {
	s.Purge()
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Purge()
		}
	}
}

// Purge deletes stored files older than RetentionDays (and leftover temp
// files older than a day), removes emptied date folders, and recounts the
// store. It returns how many files it removed.
func (s *Server) Purge() int {
	cut := s.now().Add(-time.Duration(s.cfg.RetentionDays) * 24 * time.Hour)
	tmpCut := s.now().Add(-24 * time.Hour)
	removed := 0
	dates, _ := os.ReadDir(s.cfg.Dir)
	for _, d := range dates {
		if !d.IsDir() {
			continue
		}
		dir := filepath.Join(s.cfg.Dir, d.Name())
		files, _ := os.ReadDir(dir)
		left := 0
		for _, f := range files {
			info, err := f.Info()
			if err != nil {
				continue
			}
			old := info.ModTime().Before(cut)
			if strings.HasPrefix(f.Name(), ".upload-") {
				old = info.ModTime().Before(tmpCut)
			}
			if old && info.Mode().IsRegular() {
				if os.Remove(filepath.Join(dir, f.Name())) == nil {
					removed++
					continue
				}
			}
			left++
		}
		if left == 0 {
			_ = os.Remove(dir)
		}
	}
	s.mu.Lock()
	s.used = s.diskUsage()
	s.mu.Unlock()
	if removed > 0 {
		s.Logger.Printf("media: purged %d files older than %d days", removed, s.cfg.RetentionDays)
	}
	return removed
}

func (s *Server) diskUsage() int64 {
	var n int64
	_ = filepath.WalkDir(s.cfg.Dir, func(_ string, d os.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if info, e := d.Info(); e == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}
