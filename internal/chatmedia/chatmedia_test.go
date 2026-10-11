package chatmedia

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"golang.org/x/image/bmp"
)

func testPic() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 8, 6))
	for y := 0; y < 6; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 30), uint8(y * 40), 200, 255})
		}
	}
	return img
}

func encPNG(t *testing.T) []byte {
	var b bytes.Buffer
	if err := png.Encode(&b, testPic()); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func encJPEG(t *testing.T) []byte {
	var b bytes.Buffer
	if err := jpeg.Encode(&b, testPic(), nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func encBMP(t *testing.T) []byte {
	var b bytes.Buffer
	if err := bmp.Encode(&b, testPic()); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func encGIF(t *testing.T, frames int) []byte {
	pal := color.Palette{color.Black, color.White}
	g := &gif.GIF{}
	for i := 0; i < frames; i++ {
		p := image.NewPaletted(image.Rect(0, 0, 4, 4), pal)
		p.SetColorIndex(i%4, 0, 1)
		g.Image = append(g.Image, p)
		g.Delay = append(g.Delay, 10)
	}
	var b bytes.Buffer
	if err := gif.EncodeAll(&b, g); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// webpLossless is a 1x1 lossless WebP.
var webpLossless = []byte{
	0x52, 0x49, 0x46, 0x46, 0x1a, 0x00, 0x00, 0x00, 0x57, 0x45, 0x42, 0x50,
	0x56, 0x50, 0x38, 0x4c, 0x0d, 0x00, 0x00, 0x00, 0x2f, 0x00, 0x00, 0x00,
	0x10, 0x07, 0x10, 0x11, 0x11, 0x88, 0x88, 0xfe, 0x07, 0x00,
}

func sauce(body []byte) []byte {
	rec := make([]byte, 128)
	copy(rec, "SAUCE00")
	return append(append(append([]byte{}, body...), 0x1a), rec...)
}

func TestNormalizeTypes(t *testing.T) {
	ansi := []byte("\x1b[1;31m\xdb\xdb\xb1\xb0 hello\x1b[0m\r\n")
	cases := []struct {
		name, file string
		data       []byte
		ext        string
		kind       Kind
	}{
		{"png", "a.png", encPNG(t), "png", KindImage},
		{"jpeg", "a.jpg", encJPEG(t), "jpg", KindImage},
		{"gif", "a.gif", encGIF(t, 1), "gif", KindImage},
		{"bmp becomes png", "a.bmp", encBMP(t), "png", KindImage},
		{"webp becomes png", "a.webp", webpLossless, "png", KindImage},
		{"png named .txt is still png", "notes.txt", encPNG(t), "png", KindImage},
		{"ansi", "art.ans", ansi, "ans", KindANSI},
		{"ansi with SAUCE kept", "art.ans", sauce(ansi), "ans", KindANSI},
		{"binarytext", "art.bin", bytes.Repeat([]byte{0xdb, 0x1f}, 80*3), "bin", KindANSI},
	}
	for _, c := range cases {
		ext, kind, out, err := Normalize(c.file, c.data)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if ext != c.ext || kind != c.kind {
			t.Errorf("%s: got %s/%s, want %s/%s", c.name, ext, kind, c.ext, c.kind)
		}
		if kind == KindANSI && !bytes.Equal(out, c.data) {
			t.Errorf("%s: art was altered", c.name)
		}
		if kind == KindImage {
			if _, _, err := image.Decode(bytes.NewReader(out)); err != nil {
				t.Errorf("%s: stored picture does not decode: %v", c.name, err)
			}
		}
	}
}

func TestNormalizeKeepsGIFAnimation(t *testing.T) {
	_, _, out, err := Normalize("a.gif", encGIF(t, 5))
	if err != nil {
		t.Fatal(err)
	}
	g, err := gif.DecodeAll(bytes.NewReader(out))
	if err != nil || len(g.Image) != 5 {
		t.Fatalf("frames = %d, err %v", len(g.Image), err)
	}
}

func TestNormalizeStripsTrailingPayload(t *testing.T) {
	// A polyglot: a real JPEG with a script appended after its end marker.
	data := append(encJPEG(t), []byte("<script>alert(1)</script>")...)
	_, _, out, err := Normalize("a.jpg", data)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte("<script>")) {
		t.Fatal("re-encoded picture still carries the appended payload")
	}
}

func TestNormalizeRefuses(t *testing.T) {
	cases := []struct {
		name, file string
		data       []byte
	}{
		{"html named .ans", "x.ans", []byte("<!DOCTYPE html><html><body>hi</body></html>")},
		{"svg named .png", "x.png", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>1</script></svg>`)},
		{"svg named .ans", "x.ans", []byte(`<?xml version="1.0"?><svg></svg>`)},
		{"html with leading space", "x.asc", []byte("  \n<html>")},
		{"text named .png", "x.png", []byte("hello world")},
		{"truncated png", "x.png", encPNG(t)[:20]},
		{"elf binary named .ans", "x.ans", append([]byte("\x7fELF"), make([]byte, 400)...)},
		{"bin of the wrong length", "x.bin", make([]byte, 161)},
		{"exe", "x.exe", []byte("MZ\x90\x00")},
		{"empty", "x.ans", nil},
	}
	for _, c := range cases {
		if _, _, _, err := Normalize(c.file, c.data); err == nil {
			t.Errorf("%s: accepted", c.name)
		}
	}
}

func newTestServer(t *testing.T, cfg Config) (*Server, *httptest.Server) {
	t.Helper()
	cfg.Dir = t.TempDir()
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	s.Logger.SetOutput(io.Discard)
	hs := httptest.NewServer(s.Handler())
	t.Cleanup(hs.Close)
	return s, hs
}

func post(t *testing.T, base, name string, data []byte) (int, uploadReply) {
	t.Helper()
	resp, err := http.Post(base+"/upload?name="+name, "application/octet-stream", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var r uploadReply
	_ = json.NewDecoder(resp.Body).Decode(&r)
	return resp.StatusCode, r
}

var urlShape = regexp.MustCompile(`/chatmedia/\d{4}-\d{2}-\d{2}/[a-f0-9]{24}\.(png|jpg|gif|ans|bin)$`)

func TestUploadAndServe(t *testing.T) {
	s, hs := newTestServer(t, Config{URLBase: "https://media.example.org/"})
	code, r := post(t, hs.URL, "cat.png", encPNG(t))
	if code != 200 || r.Error != "" {
		t.Fatalf("upload: %d %+v", code, r)
	}
	if !strings.HasPrefix(r.URL, "https://media.example.org/chatmedia/") || !urlShape.MatchString(r.URL) {
		t.Fatalf("url %q has the wrong shape", r.URL)
	}
	if r.ExpiresDays != 7 || r.Type != "image" {
		t.Fatalf("reply %+v", r)
	}
	path := strings.TrimPrefix(r.URL, "https://media.example.org")
	if _, err := os.Stat(filepath.Join(s.cfg.Dir, strings.TrimPrefix(path, "/chatmedia/"))); err != nil {
		t.Fatalf("not stored on disk: %v", err)
	}
	resp, err := http.Get(hs.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	h := resp.Header
	if resp.StatusCode != 200 || h.Get("Content-Type") != "image/png" || h.Get("X-Content-Type-Options") != "nosniff" ||
		!strings.Contains(h.Get("Content-Security-Policy"), "default-src 'none'") || h.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("serve: %d %v", resp.StatusCode, h)
	}
	if _, _, err := image.Decode(bytes.NewReader(body)); err != nil {
		t.Fatalf("served bytes: %v", err)
	}
}

func TestServeANSIAsAttachment(t *testing.T) {
	_, hs := newTestServer(t, Config{})
	_, r := post(t, hs.URL, "art.ans", []byte("\x1b[31m\xdb\xdb\r\n"))
	if r.Error != "" || !strings.HasPrefix(r.URL, hs.URL+"/chatmedia/") {
		t.Fatalf("%+v", r)
	}
	resp, err := http.Get(r.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("content type %q", ct)
	}
	if !strings.HasPrefix(resp.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("disposition %q", resp.Header.Get("Content-Disposition"))
	}
}

func TestServeOnlyTheLayout(t *testing.T) {
	s, hs := newTestServer(t, Config{})
	os.WriteFile(filepath.Join(s.cfg.Dir, "secret.txt"), []byte("x"), 0o644)
	os.MkdirAll(filepath.Join(s.cfg.Dir, "2026-01-01"), 0o755)
	os.WriteFile(filepath.Join(s.cfg.Dir, "2026-01-01", "page.html"), []byte("<html>"), 0o644)
	for _, p := range []string{
		"/chatmedia/secret.txt",
		"/chatmedia/2026-01-01/page.html",
		"/chatmedia/../secret.txt",
		"/chatmedia/2026-01-01/0123456789abcdef01234567.svg",
		"/chatmedia/2026-01-01/0123456789abcdef01234567.png", // well formed, missing
		"/",
	} {
		resp, err := http.Get(hs.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 404 {
			t.Errorf("%s: %d, want 404", p, resp.StatusCode)
		}
	}
}

func TestUploadLimits(t *testing.T) {
	_, hs := newTestServer(t, Config{MaxImageMB: 1, MaxANSIMB: 1})
	big := append(encPNG(t), make([]byte, 1<<20)...)
	if code, _ := post(t, hs.URL, "big.png", big); code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversize: %d", code)
	}
	if code, r := post(t, hs.URL, "x.html", []byte("<html>")); code != http.StatusUnsupportedMediaType || r.Error == "" {
		t.Errorf("html: %d %+v", code, r)
	}
	resp, err := http.Get(hs.URL + "/upload")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET /upload: %d", resp.StatusCode)
	}
}

func TestStoreFull(t *testing.T) {
	s, hs := newTestServer(t, Config{MaxStoreMB: 1})
	s.mu.Lock()
	s.used = 1<<20 - 10
	s.mu.Unlock()
	if code, r := post(t, hs.URL, "a.png", encPNG(t)); code != http.StatusInsufficientStorage || r.Error == "" {
		t.Fatalf("store full: %d %+v", code, r)
	}
}

func TestPerIPRateLimit(t *testing.T) {
	_, hs := newTestServer(t, Config{PerIPPerHour: 2})
	for i := 0; i < 2; i++ {
		if code, _ := post(t, hs.URL, "a.png", encPNG(t)); code != 200 {
			t.Fatalf("upload %d: %d", i, code)
		}
	}
	if code, _ := post(t, hs.URL, "a.png", encPNG(t)); code != http.StatusTooManyRequests {
		t.Fatalf("third upload: %d", code)
	}
}

func TestPurge(t *testing.T) {
	s, hs := newTestServer(t, Config{RetentionDays: 7})
	_, r := post(t, hs.URL, "a.png", encPNG(t))
	file := filepath.Join(s.cfg.Dir, strings.TrimPrefix(strings.TrimPrefix(r.URL, hs.URL), "/chatmedia/"))
	if n := s.Purge(); n != 0 {
		t.Fatalf("fresh file purged (%d)", n)
	}
	old := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(file, old, old); err != nil {
		t.Fatal(err)
	}
	if n := s.Purge(); n != 1 {
		t.Fatalf("purged %d, want 1", n)
	}
	if _, err := os.Stat(filepath.Dir(file)); !os.IsNotExist(err) {
		t.Fatal("emptied date folder left behind")
	}
	if s.used != 0 {
		t.Fatalf("store still counts %d bytes", s.used)
	}
}

func TestClientUpload(t *testing.T) {
	_, hs := newTestServer(t, Config{})
	url, days, err := Upload(context.Background(), hs.URL+"/upload", "cat.png", encPNG(t))
	if err != nil || !urlShape.MatchString(url) || days != 7 {
		t.Fatalf("Upload = %q, %d, %v", url, days, err)
	}
	if _, _, err := Upload(context.Background(), hs.URL+"/upload", "x.ans", []byte("<html>")); err == nil ||
		!strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("refusal not surfaced: %v", err)
	}
	if _, _, err := Upload(context.Background(), "ftp://x/upload", "a.png", nil); err == nil {
		t.Fatal("non-http upload_url accepted")
	}
}

func TestLoadINI(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.ini")
	os.WriteFile(p, []byte("; comment\n[other]\nmedia_addr = :1\n[media]\nmedia_addr = :10089 ; inline\nmedia_dir=/srv/m\nretention_days = 3\nuploads_per_hour=0\n"), 0o644)
	cfg := Defaults()
	if err := LoadINI(p, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != ":10089" || cfg.Dir != "/srv/m" || cfg.RetentionDays != 3 || cfg.PerIPPerHour != 0 {
		t.Fatalf("%+v", cfg)
	}
}
