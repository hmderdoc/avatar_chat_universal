package webchat

import (
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/hmderdoc/avatar_chat_universal/internal/avatar"
	"github.com/hmderdoc/avatar_chat_universal/internal/inlineimg"
	webpage "github.com/hmderdoc/avatar_chat_universal/web"
)

func testAvatar(b byte) avatar.Avatar {
	a := make(avatar.Avatar, 120)
	for i := 0; i < 120; i += 2 {
		a[i], a[i+1] = b, 0x1e
	}
	return a
}

func TestAvatarLibrary(t *testing.T) {
	f := startFront(t, false, nil, func(s *Server) {
		s.Avatars = []*avatar.Collection{
			{Name: "Faces one", Title: "Faces", Avatars: []avatar.Avatar{testAvatar('A'), testAvatar('B')}},
			{Name: "Faces one", Avatars: []avatar.Avatar{testAvatar('C')}}, // same name: gets a suffix
			{Name: "empty"},
		}
	})
	resp, body := get(t, f.http+"/avatars/index.json")
	var index []collectionInfo
	if resp.StatusCode != 200 || json.Unmarshal([]byte(body), &index) != nil {
		t.Fatalf("index: %d %s", resp.StatusCode, body)
	}
	if len(index) != 2 || index[0].ID != "Faces-one" || index[0].Count != 2 || index[1].ID != "Faces-one-2" {
		t.Fatalf("index = %+v", index)
	}
	_, body = get(t, f.http+"/avatars/Faces-one.json")
	var c struct {
		Avatars []string `json:"avatars"`
	}
	if json.Unmarshal([]byte(body), &c) != nil || len(c.Avatars) != 2 {
		t.Fatalf("collection %s", body)
	}
	if b, _ := base64.StdEncoding.DecodeString(c.Avatars[1]); len(b) != 120 || b[0] != 'B' {
		t.Fatalf("avatar bytes %v", b)
	}
	for _, p := range []string{"/avatars/nope.json", "/avatars/empty.json", "/avatars/Faces-one.bin", "/avatars/../index.json"} {
		if resp, _ := get(t, f.http+p); resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: %d", p, resp.StatusCode)
		}
	}
	if resp, _ := postEmpty(t, f.http+"/avatars/index.json"); resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST index: %d", resp.StatusCode)
	}
	_, body = get(t, f.http+"/config.json")
	if !strings.Contains(body, `"avatars":true`) {
		t.Fatalf("config.json avatars: %s", body)
	}
}

func postEmpty(t *testing.T, u string) (*http.Response, error) {
	t.Helper()
	resp, err := http.Post(u, "text/plain", strings.NewReader(""))
	if err == nil {
		resp.Body.Close()
	}
	return resp, err
}

func TestConfigNSFWAndStyles(t *testing.T) {
	read := func(setup func(*Config)) pageConfig {
		t.Helper()
		f := startFront(t, false, setup, nil)
		_, body := get(t, f.http+"/config.json")
		var c pageConfig
		if err := json.Unmarshal([]byte(body), &c); err != nil {
			t.Fatal(err)
		}
		return c
	}
	c := read(nil)
	if strings.Join(c.NSFWKeywords, ",") != strings.Join(inlineimg.DefaultNSFWKeywords, ",") {
		t.Fatalf("default keywords %v", c.NSFWKeywords)
	}
	if len(c.Styles) != len(PageStyles) || c.DefaultStyle != "classic" || c.Avatars {
		t.Fatalf("defaults %+v", c)
	}
	c = read(func(cfg *Config) { cfg.NSFWSet, cfg.NSFWKeywords = true, []string{"spoiler"} })
	if strings.Join(c.NSFWKeywords, ",") != "spoiler" {
		t.Fatalf("sysop keywords %v", c.NSFWKeywords)
	}
	c = read(func(cfg *Config) { cfg.NSFWSet = true }) // nsfw_keywords = none
	if c.NSFWKeywords == nil || len(c.NSFWKeywords) != 0 {
		t.Fatalf("veil off: %v", c.NSFWKeywords)
	}
	c = read(func(cfg *Config) { cfg.Styles, cfg.DefaultStyle = []string{"cga", "win95", "bogus"}, "win95" })
	if len(c.Styles) != 2 || c.Styles[0].ID != "cga" || c.DefaultStyle != "win95" {
		t.Fatalf("offered %+v default %q", c.Styles, c.DefaultStyle)
	}
	c = read(func(cfg *Config) { cfg.Styles, cfg.DefaultStyle = []string{"amber"}, "cga" })
	if c.DefaultStyle != "amber" {
		t.Fatalf("unoffered default -> %q", c.DefaultStyle)
	}
	if got := offeredStyles([]string{"bogus"}); len(got) != len(PageStyles) {
		t.Fatalf("nothing valid offered -> %d", len(got))
	}
}

// The style list is the Synchronet page's: same ids, labels and order.
func TestPageStylesMatchSynchronet(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "web", "synchronet", "src", "lib", "webprefs.ts"))
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`\{ id: '([a-z0-9-]+)', label: '([^']+)' \}`)
	var ts []PageStyle
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		ts = append(ts, PageStyle{m[1], m[2]})
	}
	if len(ts) != len(PageStyles) {
		t.Fatalf("webprefs.ts has %d styles, Go %d", len(ts), len(PageStyles))
	}
	for i := range ts {
		if ts[i] != PageStyles[i] {
			t.Errorf("style %d: ts %+v go %+v", i, ts[i], PageStyles[i])
		}
	}
	if !strings.Contains(string(src), "DEFAULT_PAGE_STYLE = '"+DefaultPageStyle+"'") {
		t.Error("default style differs")
	}
}

// The real page: its own files plus the Synchronet build's shared ones,
// each with a type, and a web_dir copy that only overrides what it has.
func TestBuiltInPageAndWebDir(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "style.css"), []byte("/* sysop */"), 0o644)
	f := startFront(t, false, func(c *Config) { c.WebDir = dir }, func(s *Server) { s.Static = webpage.Standalone })
	want := map[string]string{
		"/":                             "text/html; charset=utf-8",
		"/app.js":                       "text/javascript; charset=utf-8",
		"/art.js":                       "text/javascript; charset=utf-8",
		"/shared/app.css":               "text/css; charset=utf-8",
		"/shared/editor.css":            "text/css; charset=utf-8",
		"/shared/ansi-editor.js":        "text/javascript; charset=utf-8",
		"/shared/ansi-editor-modal.js":  "text/javascript; charset=utf-8",
		"/shared/ansi-editor-modal.css": "text/css; charset=utf-8",
		"/shared/fonts/CP437.F16":       "application/octet-stream",
	}
	for p, ct := range want {
		resp, _ := get(t, f.http+p)
		if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != ct {
			t.Errorf("%s: %d %q", p, resp.StatusCode, resp.Header.Get("Content-Type"))
		}
	}
	if _, body := get(t, f.http+"/style.css"); body != "/* sysop */" {
		t.Errorf("web_dir copy not served: %q", body)
	}
	if resp, _ := get(t, f.http+"/shared/app.js"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("the Synchronet page's own app.js is served: %d", resp.StatusCode)
	}
	resp, _ := get(t, f.http+"/")
	if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "media-src 'self'") {
		t.Errorf("CSP %q", resp.Header.Get("Content-Security-Policy"))
	}
	// Every file the page names is there.
	page, _ := fs.ReadFile(webpage.Standalone, "index.html")
	for _, m := range regexp.MustCompile(`(?:href|src)="([^"]+)"`).FindAllStringSubmatch(string(page), -1) {
		if _, err := fs.Stat(webpage.Standalone, m[1]); err != nil {
			t.Errorf("index.html names %s: %v", m[1], err)
		}
	}
}
