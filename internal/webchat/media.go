package webchat

import (
	"bufio"
	"context"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/hmderdoc/avatar_chat_universal/internal/linkprev"
)

// /media?url= streams an audio or video file a card plays, so the page's
// players never load from another site (its CSP allows media only from
// its own origin). The same gate as /img applies: the URL has gone out to
// a browser here in chat traffic, or a card named it as its audio or video
// stream; public addresses only (inlineimg's guards, at dial time and on
// every redirect); FetchPerMinute per address. On top of that:
//
//   - only audio/* and video/* pass, or a download typed octet-stream (or
//     untyped) that its card or its first bytes say is media; HTML, SVG
//     and anything else are refused, and nosniff is always sent;
//   - a file over MediaProxyMaxMB is refused when its length is known and
//     cut there when it isn't;
//   - at most MediaStreams play through here at once;
//   - the body is streamed, not buffered. A Range from the player is passed
//     on; an upstream that refuses ranges with 416 is asked again without
//     one (inlineimg.Fetcher.Open).

// MediaOpener starts a guarded GET for /media; inlineimg.Fetcher is one.
type MediaOpener interface {
	Open(ctx context.Context, rawURL, rng string) (*http.Response, error)
}

// cardMedia remembers the type a card gave its audio or video stream, so
// a download served as octet-stream can be passed as what it is.
type cardMedia struct {
	mu    sync.Mutex
	types map[string]string
}

func (c *cardMedia) set(u, typ string) {
	if u == "" || len(u) > linkprev.MaxURLLength {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.types == nil || len(c.types) > 5000 {
		c.types = map[string]string{}
	}
	c.types[u] = typ
}

func (c *cardMedia) get(u string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.types[u]
}

// downloadTypes are content types a server gives a file it won't name.
var downloadTypes = map[string]bool{
	"": true, "application/octet-stream": true, "binary/octet-stream": true,
	"application/x-download": true, "application/download": true, "application/force-download": true,
}

// mediaType decides what an upstream reply may be passed as: its own
// audio/* or video/* type, or for a download the card's type or a sniff
// of its first bytes; "" refuses it.
func mediaType(header, card string, head []byte) string {
	ct, _, _ := mime.ParseMediaType(header)
	ct = strings.ToLower(ct)
	switch {
	case strings.HasPrefix(ct, "audio/"), strings.HasPrefix(ct, "video/"):
		return ct
	case !downloadTypes[ct]:
		return "" // text/html, image/svg+xml, application/json and the rest
	}
	if sniffed := linkprev.SniffMedia(head); sniffed != "" {
		return sniffed
	}
	// The card vouches for it only if the bytes aren't a page or markup.
	switch det := http.DetectContentType(head); {
	case strings.HasPrefix(det, "text/html"), strings.HasPrefix(det, "text/xml"), strings.Contains(string(head), "<svg"):
		return ""
	}
	if strings.HasPrefix(card, "audio/") || strings.HasPrefix(card, "video/") {
		return card
	}
	return ""
}

func (s *Server) handleMedia(w http.ResponseWriter, r *http.Request) {
	u := s.fetchable(w, r)
	if u == "" {
		return
	}
	if s.Streams == nil {
		http.Error(w, "media playback is off", http.StatusNotFound)
		return
	}
	select {
	case s.streamQ <- struct{}{}:
		defer func() { <-s.streamQ }()
	default:
		http.Error(w, "too many players open on this server; try again shortly", http.StatusServiceUnavailable)
		return
	}
	resp, err := s.Streams.Open(r.Context(), u, r.Header.Get("Range"))
	if err != nil {
		http.Error(w, "could not fetch the file", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		http.Error(w, "the file's server answered "+strconv.Itoa(resp.StatusCode), http.StatusBadGateway)
		return
	}
	max := int64(s.cfg.MediaProxyMaxMB) << 20
	if resp.ContentLength > max {
		http.Error(w, "file is too large to play here", http.StatusRequestEntityTooLarge)
		return
	}
	body := bufio.NewReaderSize(resp.Body, 4096)
	head, _ := body.Peek(512)
	typ := mediaType(resp.Header.Get("Content-Type"), s.cardMedia.get(u), head)
	if typ == "" {
		http.Error(w, "not an audio or video file", http.StatusUnsupportedMediaType)
		return
	}
	h := w.Header()
	h.Set("Content-Type", typ)
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Content-Disposition", "inline")
	h.Set("Cache-Control", "private, max-age=3600")
	if resp.StatusCode == http.StatusPartialContent {
		if cr := resp.Header.Get("Content-Range"); cr != "" {
			h.Set("Content-Range", cr)
		}
		h.Set("Accept-Ranges", "bytes")
	} else if resp.Header.Get("Accept-Ranges") == "bytes" {
		h.Set("Accept-Ranges", "bytes")
	}
	if resp.ContentLength >= 0 {
		h.Set("Content-Length", strconv.FormatInt(resp.ContentLength, 10))
	}
	w.WriteHeader(resp.StatusCode)
	if r.Method == http.MethodHead {
		return
	}
	// Streamed in chunks as they arrive, cut at the cap.
	buf := make([]byte, 32<<10)
	flusher, _ := w.(http.Flusher)
	src := io.LimitReader(body, max)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}
