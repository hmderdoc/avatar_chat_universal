package webchat

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/inlineimg"
	"github.com/hmderdoc/avatar_chat_universal/internal/linkprev"
)

// /img and /preview fetch on the page's behalf, so the browser never
// contacts another site. They are not an open proxy: a URL is fetched only
// after it has appeared in chat traffic sent to a browser here (or as a
// card's picture), through inlineimg's guards (http and https on ports 80
// and 443, public addresses checked at dial time, redirects re-checked,
// byte cap), at most FetchPerMinute requests a minute per address and
// eight at a time overall.

// seenURLs remembers URLs that have gone out to a browser.
type seenURLs struct {
	max int
	ttl time.Duration
	now func() time.Time

	mu   sync.Mutex
	urls map[string]time.Time
}

func newSeenURLs(max int, ttl time.Duration) *seenURLs {
	return &seenURLs{max: max, ttl: ttl, now: time.Now, urls: map[string]time.Time{}}
}

func (s *seenURLs) add(u string) {
	if u == "" || len(u) > linkprev.MaxURLLength {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if len(s.urls) >= s.max {
		for k, t := range s.urls {
			if now.Sub(t) > s.ttl {
				delete(s.urls, k)
			}
		}
		for k := range s.urls { // still full: drop an arbitrary half
			if len(s.urls) < s.max/2 {
				break
			}
			delete(s.urls, k)
		}
	}
	s.urls[u] = now
}

func (s *seenURLs) has(u string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.urls[u]
	return ok && s.now().Sub(t) <= s.ttl
}

// scanPacket records the URLs in every string of a packet's data.
func (s *seenURLs) scanPacket(body []byte) {
	if !strings.Contains(string(body), "http") {
		return
	}
	var p struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(body, &p) != nil || len(p.Data) == 0 {
		return
	}
	var v interface{}
	if json.Unmarshal(p.Data, &v) != nil {
		return
	}
	s.walk(v, 0)
}

func (s *seenURLs) walk(v interface{}, depth int) {
	if depth > 6 {
		return
	}
	switch t := v.(type) {
	case string:
		if strings.Contains(t, "http") {
			for _, u := range inlineimg.FindURLs(t) {
				s.add(u)
			}
		}
	case []interface{}:
		for _, e := range t {
			s.walk(e, depth+1)
		}
	case map[string]interface{}:
		for _, e := range t {
			s.walk(e, depth+1)
		}
	}
}

// rateLimit counts requests per address in fixed windows.
type rateLimit struct {
	n      int
	window time.Duration
	now    func() time.Time

	mu   sync.Mutex
	hits map[string]*rateWindow
}

type rateWindow struct {
	start time.Time
	n     int
}

func newRateLimit(n int, window time.Duration) *rateLimit {
	return &rateLimit{n: n, window: window, now: time.Now, hits: map[string]*rateWindow{}}
}

func (r *rateLimit) allow(ip string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	if len(r.hits) > 10000 {
		for k, w := range r.hits {
			if now.Sub(w.start) > r.window {
				delete(r.hits, k)
			}
		}
	}
	w := r.hits[ip]
	if w == nil || now.Sub(w.start) > r.window {
		w = &rateWindow{start: now}
		r.hits[ip] = w
	}
	w.n++
	return w.n <= r.n
}

// fetchable is the common gate for /img and /preview: it answers the
// request itself and returns "" when the fetch must not happen.
func (s *Server) fetchable(w http.ResponseWriter, r *http.Request) string {
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return ""
	}
	u := r.URL.Query().Get("url")
	if u == "" || len(u) > linkprev.MaxURLLength {
		http.Error(w, "url= is required", http.StatusBadRequest)
		return ""
	}
	if !s.seen.has(u) {
		http.Error(w, "only links posted in this chat are fetched", http.StatusForbidden)
		return ""
	}
	if !s.fetches.allow(remoteIP(r)) {
		http.Error(w, "too many requests; slow down", http.StatusTooManyRequests)
		return ""
	}
	return u
}

// imageTypes are the only types /img passes, judged by the bytes.
var imageTypes = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true, "image/bmp": true,
}

func (s *Server) handleImage(w http.ResponseWriter, r *http.Request) {
	u := s.fetchable(w, r)
	if u == "" {
		return
	}
	if s.Images == nil {
		http.Error(w, "pictures are off", http.StatusNotFound)
		return
	}
	select {
	case s.fetchQ <- struct{}{}:
		defer func() { <-s.fetchQ }()
	case <-r.Context().Done():
		return
	}
	body, err := s.Images.Fetch(r.Context(), u)
	if err != nil {
		code := http.StatusBadGateway
		if errors.Is(err, inlineimg.ErrBlocked) {
			code = http.StatusForbidden
		}
		http.Error(w, "could not fetch the picture", code)
		return
	}
	if int64(len(body)) > int64(s.cfg.ImageMaxMB)<<20 {
		http.Error(w, "picture is too large", http.StatusRequestEntityTooLarge)
		return
	}
	ct := http.DetectContentType(body)
	if !imageTypes[ct] {
		http.Error(w, "not a picture", http.StatusUnsupportedMediaType)
		return
	}
	h := w.Header()
	h.Set("Content-Type", ct)
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Content-Disposition", "inline")
	h.Set("Cache-Control", "public, max-age=86400")
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(body)
}

func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request) {
	u := s.fetchable(w, r)
	if u == "" {
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if s.Pages == nil {
		_, _ = w.Write([]byte("null"))
		return
	}
	select {
	case s.fetchQ <- struct{}{}:
		defer func() { <-s.fetchQ }()
	case <-r.Context().Done():
		return
	}
	m, err := linkprev.Get(r.Context(), s.Pages, s.Previews, u)
	if err != nil || m == nil {
		w.Header().Set("Cache-Control", "public, max-age=600")
		_, _ = w.Write([]byte("null"))
		return
	}
	s.seen.add(m.Image) // the card's picture comes through /img
	// Its audio or video plays through /media, as the type it announced.
	if m.Audio != "" {
		s.seen.add(m.Audio)
		s.cardMedia.set(m.Audio, m.AudioType)
	}
	if m.Video != "" {
		s.seen.add(m.Video)
		typ := "video/mp4"
		if strings.Contains(strings.ToLower(m.Video), ".webm") {
			typ = "video/webm"
		}
		s.cardMedia.set(m.Video, typ)
	}
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_ = json.NewEncoder(w).Encode(m)
}
