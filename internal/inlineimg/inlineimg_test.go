package inlineimg

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hmderdoc/shadecell"
)

var update = flag.Bool("update", false, "rewrite golden files")

func TestFindImageURLs(t *testing.T) {
	got := FindImageURLs("look https://x.org/a.png, and (https://x.org/b.JPG?w=2) plus https://x.org/page.html " +
		"https://x.org/a.png again https://x.org/c.webp#f https://x.org/d.gif")
	want := []string{"https://x.org/a.png", "https://x.org/b.JPG?w=2", "https://x.org/c.webp#f"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	if FindImageURLs("ftp://x.org/a.png and https://x.org/png-not-image") != nil {
		t.Fatal("non-image links matched")
	}
	if got := FindImageURLs("|https://x.org/a.png|"); len(got) != 1 || got[0] != "https://x.org/a.png" {
		t.Fatalf("pipe-delimited: %q", got)
	}
}

func TestLooksNSFW(t *testing.T) {
	kw := DefaultNSFWKeywords
	for text, want := range map[string]bool{
		"https://x.org/beach-nude-01.jpg":  true,
		"https://x.org/NSFW/cat.png":       true,
		"https://x.org/sussex-castle.jpg":  false, // "sex" only as part of a word
		"https://x.org/a%20porn%20pic.png": true,
		"just a cat":                       false,
	} {
		if got := LooksNSFW(text, kw); got != want {
			t.Errorf("LooksNSFW(%q) = %v, want %v", text, got, want)
		}
	}
	if LooksNSFW("nude", nil) {
		t.Error("empty keyword list matched")
	}
}

func TestPublicAddr(t *testing.T) {
	for addr, want := range map[string]bool{
		"8.8.8.8": true, "2606:4700::1111": true,
		"127.0.0.1": false, "10.1.2.3": false, "172.16.0.1": false, "192.168.1.1": false,
		"169.254.169.254": false, "100.64.0.1": false, "0.0.0.0": false, "224.0.0.1": false,
		"::1": false, "fe80::1": false, "fd00::1": false, "::ffff:127.0.0.1": false, "255.255.255.255": false,
	} {
		if got := PublicAddr(netip.MustParseAddr(addr)); got != want {
			t.Errorf("PublicAddr(%s) = %v, want %v", addr, got, want)
		}
	}
}

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{uint8(x * 255 / w), uint8(y * 255 / h), 128, 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// testFetcher reaches only the given httptest server: its loopback address
// is allowed and its port is added, everything else keeps the real rules.
func testFetcher(t *testing.T, srv *httptest.Server) *Fetcher {
	t.Helper()
	f := NewFetcher(t.TempDir())
	u, _ := url.Parse(srv.URL)
	f.ports = map[string]bool{"80": true, "443": true, u.Port(): true}
	f.allow = func(ip netip.Addr) bool { return ip.IsLoopback() && ip.Is4() }
	return f
}

func TestFetchGuardsAndCache(t *testing.T) {
	body := pngBytes(t, 20, 10)
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/a.png":
			hits++
			w.Write(body)
		case "/big.png":
			w.Write(make([]byte, 2048))
		case "/hop.png":
			http.Redirect(w, r, "/a.png", http.StatusFound)
		case "/evil.png":
			http.Redirect(w, r, "http://[::1]:"+strings.Split(r.Host, ":")[1]+"/a.png", http.StatusFound)
		case "/loop.png":
			http.Redirect(w, r, "/loop.png", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	f := testFetcher(t, srv)
	ctx := context.Background()

	got, err := f.Fetch(ctx, srv.URL+"/a.png")
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("fetch: %v", err)
	}
	if _, err := f.Fetch(ctx, srv.URL+"/a.png"); err != nil || hits != 1 {
		t.Fatalf("second fetch should come from cache (hits=%d, err=%v)", hits, err)
	}
	if _, err := f.Fetch(ctx, srv.URL+"/hop.png"); err != nil {
		t.Fatalf("one redirect should be followed: %v", err)
	}
	if _, err := f.Fetch(ctx, srv.URL+"/evil.png"); !errors.Is(err, ErrBlocked) {
		t.Fatalf("redirect to a refused address: err = %v, want ErrBlocked", err)
	}
	if _, err := f.Fetch(ctx, srv.URL+"/loop.png"); err == nil {
		t.Fatal("redirect loop accepted")
	}
	f.MaxBytes = 1024
	if _, err := f.Fetch(ctx, srv.URL+"/big.png"); err == nil {
		t.Fatal("over-cap body accepted")
	}
	// The real rules, without the test hooks: loopback and odd ports refused.
	real := NewFetcher("")
	if _, err := real.Fetch(ctx, srv.URL+"/a.png"); !errors.Is(err, ErrBlocked) {
		t.Fatalf("loopback on a high port: err = %v, want ErrBlocked", err)
	}
	if _, err := real.Fetch(ctx, "http://127.0.0.1/a.png"); !errors.Is(err, ErrBlocked) {
		t.Fatalf("loopback on port 80: err = %v, want ErrBlocked", err)
	}
	if _, err := real.Fetch(ctx, "file:///etc/passwd.png"); !errors.Is(err, ErrBlocked) {
		t.Fatalf("file scheme: err = %v", err)
	}
}

func TestCacheTrim(t *testing.T) {
	f := NewFetcher(t.TempDir())
	f.CacheMax = 2500
	base := time.Now().Add(-time.Hour)
	for i := 0; i < 5; i++ {
		u := fmt.Sprintf("https://x.org/%d.png", i)
		f.store(u, make([]byte, 1000))
		// Distinct, increasing use times (file systems may store coarse ones).
		at := base.Add(time.Duration(i) * time.Minute)
		os.Chtimes(f.cachePath(u), at, at)
		f.trim()
	}
	ents, _ := os.ReadDir(f.CacheDir)
	if len(ents) != 2 {
		t.Fatalf("cache holds %d files, want 2 under a 2500 byte cap", len(ents))
	}
	if _, ok := f.cached("https://x.org/4.png"); !ok {
		t.Fatal("newest entry evicted")
	}
}

func TestDecodeRefusesPixelBombs(t *testing.T) {
	// A PNG header claiming 100000 x 100000 pixels.
	var b bytes.Buffer
	png.Encode(&b, image.NewGray(image.Rect(0, 0, 1, 1)))
	raw := b.Bytes()
	ihdr := bytes.Index(raw, []byte("IHDR")) + 4
	copy(raw[ihdr:], []byte{0, 1, 0x86, 0xA0, 0, 1, 0x86, 0xA0})
	crc := crc32.ChecksumIEEE(raw[ihdr-4 : ihdr+13])
	binary.BigEndian.PutUint32(raw[ihdr+13:], crc)
	if _, err := Decode(raw); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("err = %v, want too large", err)
	}
	if _, err := Decode([]byte("<html>")); err == nil {
		t.Fatal("html decoded")
	}
}

func TestFitCells(t *testing.T) {
	for _, c := range []struct{ w, h, mc, mr, cols, rows int }{
		{400, 200, 54, 10, 40, 10}, // wide: height-bound
		{1000, 100, 54, 10, 54, 3}, // panorama: width-bound
		{100, 1000, 54, 10, 2, 10}, // tall
		{16, 16, 54, 10, 16, 8},    // tiny icon: not blown up
	} {
		cols, rows := fitCells(c.w, c.h, c.mc, c.mr)
		if cols != c.cols || rows != c.rows {
			t.Errorf("fit %dx%d in %dx%d = %dx%d, want %dx%d", c.w, c.h, c.mc, c.mr, cols, rows, c.cols, c.rows)
		}
	}
}

// fixture draws the golden-test image: a hue sweep left to right, dark to
// light top to bottom, a white disc and a black bar, 160 x 80 pixels.
func fixture() image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, 160, 80))
	for y := 0; y < 80; y++ {
		for x := 0; x < 160; x++ {
			r, g, b := hue(float64(x) / 160)
			v := 0.25 + 0.75*float64(y)/79
			c := color.NRGBA{uint8(r * v * 255), uint8(g * v * 255), uint8(b * v * 255), 255}
			if dx, dy := x-120, y-40; dx*dx+dy*dy < 15*15 {
				c = color.NRGBA{255, 255, 255, 255}
			}
			if x >= 20 && x < 60 && y >= 30 && y < 50 {
				c = color.NRGBA{0, 0, 0, 255}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

func hue(h float64) (float64, float64, float64) {
	h *= 6
	x := 1 - abs(mod2(h)-1)
	switch int(h) {
	case 0:
		return 1, x, 0
	case 1:
		return x, 1, 0
	case 2:
		return 0, 1, x
	case 3:
		return 0, x, 1
	case 4:
		return x, 0, 1
	}
	return 1, 0, x
}

func mod2(v float64) float64 {
	for v >= 2 {
		v -= 2
	}
	return v
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func dumpCells(p *Picture) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%dx%d truecolor=%v\n", p.Cols, p.Rows, p.Truecolor)
	for y := 0; y < p.Rows; y++ {
		for x := 0; x < p.Cols; x++ {
			c := p.Cells[y*p.Cols+x]
			if c.True {
				fmt.Fprintf(&b, "%02x:%02x%02x%02x/%02x%02x%02x ", c.Char, c.Fg.R, c.Fg.G, c.Fg.B, c.Bg.R, c.Bg.G, c.Bg.B)
			} else {
				fmt.Fprintf(&b, "%02x:%02x ", c.Char, byte(c.Attr))
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test -update to create it)", err)
	}
	if string(want) != got {
		t.Errorf("%s differs from the golden file (go test -update rewrites it)", name)
	}
}

func TestRenderGolden(t *testing.T) {
	img := fixture()
	if *update {
		f, _ := os.Create(filepath.Join("testdata", "fixture.png"))
		png.Encode(f, img)
		f.Close()
	}
	q := shadecell.New()
	true24 := Render(img, q, Options{MaxCols: 40, MaxRows: 10, Truecolor: true})
	shade := Render(img, q, Options{MaxCols: 40, MaxRows: 10})
	if true24.Cols != 40 || true24.Rows != 10 || shade.Cols != 40 || shade.Rows != 10 {
		t.Fatalf("sizes %dx%d / %dx%d, want 40x10", true24.Cols, true24.Rows, shade.Cols, shade.Rows)
	}
	checkGolden(t, "fixture.truecolor.golden", dumpCells(true24))
	checkGolden(t, "fixture.16color.golden", dumpCells(shade))

	// 16-colour sanity independent of the golden bytes: the disc is white
	// (bright fg 15 or a full block of it) and the bar is black.
	disc := shade.Cells[5*40+30]
	if disc.Attr&0x0F != 15 && disc.Char != ' ' {
		t.Errorf("disc cell = %02x:%02x, want white", disc.Char, byte(disc.Attr))
	}
	bar := shade.Cells[5*40+8]
	if !(bar.Char == ' ' && bar.Attr>>4 == 0) && !(bar.Attr&0x0F == 0) {
		t.Errorf("bar cell = %02x:%02x, want black", bar.Char, byte(bar.Attr))
	}
	for _, c := range shade.Cells {
		if c.True || c.Attr>>4 > 7 {
			t.Fatalf("16-colour cell uses truecolor or a bright background: %+v", c)
		}
	}
}

func TestSixelOutput(t *testing.T) {
	pic := Render(fixture(), shadecell.New(), Options{MaxCols: 40, MaxRows: 10, Sixel: true, CellW: 8, CellH: 16})
	s := string(pic.Sixel)
	if !strings.HasPrefix(s, "\x1bP0;1;0q\"1;1;320;156") || !strings.HasSuffix(s, "\x1b\\") {
		t.Fatalf("sixel header/trailer wrong: %q ... %q", s[:min(40, len(s))], s[max(0, len(s)-4):])
	}
	// 156 px = 26 bands: 25 band separators.
	if n := strings.Count(s, "-"); n != 25 {
		t.Fatalf("band separators = %d, want 25", n)
	}
	for _, c := range []byte(s[2 : len(s)-2]) {
		if c < 0x20 || c > 0x7e {
			t.Fatalf("control byte %#x inside the sixel body", c)
		}
	}
	if len(pic.Sixel) > 64<<10 {
		t.Fatalf("sixel is %d bytes; expected well under 64 KB for 320x156", len(pic.Sixel))
	}
}
