package webchat

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/inlineimg"
)

// plainOpener reaches a test upstream directly (the address guards are
// inlineimg's, tested there) and retries a refused Range like Open does.
type plainOpener struct{}

func (plainOpener) Open(ctx context.Context, u, rng string) (*http.Response, error) {
	get := func(rng string) (*http.Response, error) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if rng != "" {
			req.Header.Set("Range", rng)
		}
		return http.DefaultClient.Do(req)
	}
	resp, err := get(rng)
	if err == nil && rng != "" && resp.StatusCode == http.StatusRequestedRangeNotSatisfiable {
		resp.Body.Close()
		return get("")
	}
	return resp, err
}

var id3 = append([]byte("ID3\x04\x00\x00\x00\x00\x00\x00"), make([]byte, 2000)...)

// mediaUpstream serves the shapes /media must tell apart.
func mediaUpstream(t *testing.T, release chan struct{}) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/song.mp3":
			w.Header().Set("Content-Type", "audio/mpeg")
			w.Header().Set("Accept-Ranges", "bytes")
			http.ServeContent(w, r, "song.mp3", time.Time{}, strings.NewReader(string(id3)))
		case "/download": // Synchronet's files.ssjs: octet-stream, refuses Range
			if r.Header.Get("Range") != "" {
				w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Disposition", `attachment; filename="Song.mp3"`)
			w.Write(id3)
		case "/blob": // octet-stream, bytes say nothing
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Write(make([]byte, 600))
		case "/blobhtml":
			w.Header().Set("Content-Type", "application/octet-stream")
			io.WriteString(w, "<!doctype html><html><script>x</script></html>")
		case "/page":
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, "<html></html>")
		case "/pic.svg":
			w.Header().Set("Content-Type", "image/svg+xml")
			io.WriteString(w, "<svg xmlns='http://www.w3.org/2000/svg'/>")
		case "/big.mp3":
			w.Header().Set("Content-Type", "audio/mpeg")
			w.Header().Set("Content-Length", "3145728")
			w.Write(make([]byte, 3<<20))
		case "/unsized.mp3":
			w.Header().Set("Content-Type", "audio/mpeg")
			w.(http.Flusher).Flush() // chunked: no length up front
			for i := 0; i < 3; i++ {
				w.Write(make([]byte, 1<<20))
			}
		case "/slow.mp3":
			w.Header().Set("Content-Type", "audio/mpeg")
			w.Write(id3)
			w.(http.Flusher).Flush()
			<-release
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func mediaFront(t *testing.T, setup func(*Config)) (*front, string, chan struct{}) {
	release := make(chan struct{})
	up := mediaUpstream(t, release)
	t.Cleanup(func() { close(release) })
	f := startFront(t, false, setup, func(s *Server) { s.Streams = plainOpener{} })
	return f, up.URL, release
}

func getMedia(t *testing.T, f *front, u, rng string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, f.http+"/media?url="+url.QueryEscape(u), nil)
	if rng != "" {
		req.Header.Set("Range", rng)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, body
}

func TestMediaProxy(t *testing.T) {
	f, up, _ := mediaFront(t, func(c *Config) { c.MediaProxyMaxMB = 2 })
	seen := func(paths ...string) {
		for _, p := range paths {
			f.web.seen.add(up + p)
		}
	}

	// Only links that went out in chat (or a card named).
	if resp, _ := getMedia(t, f, up+"/song.mp3", ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("unseen URL: %d", resp.StatusCode)
	}
	seen("/song.mp3", "/download", "/blob", "/blobhtml", "/page", "/pic.svg", "/big.mp3", "/unsized.mp3")

	resp, body := getMedia(t, f, up+"/song.mp3", "")
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "audio/mpeg" || len(body) != len(id3) ||
		resp.Header.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("song: %d %v %d bytes", resp.StatusCode, resp.Header, len(body))
	}
	// A player's Range is passed through when the upstream takes ranges.
	resp, body = getMedia(t, f, up+"/song.mp3", "bytes=100-199")
	if resp.StatusCode != http.StatusPartialContent || len(body) != 100 || !strings.HasPrefix(resp.Header.Get("Content-Range"), "bytes 100-199/") {
		t.Fatalf("range: %d %q %d bytes", resp.StatusCode, resp.Header.Get("Content-Range"), len(body))
	}
	// A download link: 416 on the Range, retried whole, passed as audio by
	// its bytes.
	resp, body = getMedia(t, f, up+"/download", "bytes=0-")
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "audio/mpeg" || len(body) != len(id3) {
		t.Fatalf("download: %d %q %d bytes", resp.StatusCode, resp.Header.Get("Content-Type"), len(body))
	}
	// Octet-stream that says nothing: refused, unless a card typed it;
	// a card can't vouch for bytes that are a page.
	if resp, _ := getMedia(t, f, up+"/blob", ""); resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("blob: %d", resp.StatusCode)
	}
	f.web.cardMedia.set(up+"/blob", "audio/ogg")
	if resp, _ := getMedia(t, f, up+"/blob", ""); resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "audio/ogg" {
		t.Fatalf("blob with a card: %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	f.web.cardMedia.set(up+"/blobhtml", "audio/mpeg")
	for _, p := range []string{"/blobhtml", "/page", "/pic.svg"} {
		if resp, _ := getMedia(t, f, up+p, ""); resp.StatusCode != http.StatusUnsupportedMediaType {
			t.Fatalf("%s: %d, want 415", p, resp.StatusCode)
		}
	}
	// The size cap: refused when the length says so, cut when unknown.
	if resp, _ := getMedia(t, f, up+"/big.mp3", ""); resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("big: %d", resp.StatusCode)
	}
	if _, body := getMedia(t, f, up+"/unsized.mp3", ""); len(body) != 2<<20 {
		t.Fatalf("unsized: %d bytes, want the 2 MB cap", len(body))
	}
}

func TestMediaLimits(t *testing.T) {
	f, up, release := mediaFront(t, func(c *Config) { c.MediaStreams = 1; c.FetchPerMinute = 3 })
	f.web.seen.add(up + "/slow.mp3")
	f.web.seen.add(up + "/song.mp3")

	// One stream at a time: a second while the first plays is turned away.
	resp, err := http.Get(f.http + "/media?url=" + url.QueryEscape(up+"/slow.mp3"))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("first stream: %v %v", err, resp)
	}
	if r2, _ := getMedia(t, f, up+"/song.mp3", ""); r2.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("second stream: %d, want 503", r2.StatusCode)
	}
	release <- struct{}{}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	// The per-address rate limit (3 a minute; two used above).
	if r, _ := getMedia(t, f, up+"/song.mp3", ""); r.StatusCode != 200 {
		t.Fatalf("third: %d", r.StatusCode)
	}
	if r, _ := getMedia(t, f, up+"/song.mp3", ""); r.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("fourth: %d, want 429", r.StatusCode)
	}
}

// A card for a download link names its file as audio; /preview marks it
// playable, and /media then streams it.
func TestCardMakesMediaPlayable(t *testing.T) {
	f, up, _ := mediaFront(t, nil)
	link := up + "/download"
	f.web.Pages = mediaPage{url: link}
	f.web.seen.add(link + "?card")
	resp, err := http.Get(f.http + "/preview?url=" + url.QueryEscape(link+"?card"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"audio":"`+link+`"`) {
		t.Fatalf("card: %s", body)
	}
	if r, _ := getMedia(t, f, link, ""); r.StatusCode != 200 || r.Header.Get("Content-Type") != "audio/mpeg" {
		t.Fatalf("card's audio: %d %q", r.StatusCode, r.Header.Get("Content-Type"))
	}
}

// mediaPage answers a card fetch as a download: octet-stream named
// Song.mp3, final URL the file itself.
type mediaPage struct{ url string }

func (m mediaPage) FetchPage(_ context.Context, u string, _ int64) (*inlineimg.Page, error) {
	return &inlineimg.Page{Body: id3, FinalURL: m.url, ContentType: "application/octet-stream", FileName: "Song.mp3"}, nil
}
