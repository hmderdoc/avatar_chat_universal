package webchat

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"testing"

	"github.com/hmderdoc/avatar_chat_universal/internal/avatar"
	"github.com/hmderdoc/avatar_chat_universal/internal/chatmedia"
	"github.com/hmderdoc/avatar_chat_universal/internal/chatserver"
	"github.com/hmderdoc/avatar_chat_universal/internal/inlineimg"
	webpage "github.com/hmderdoc/avatar_chat_universal/web"
)

// TestE2EServe runs the real page against a local chat for browser tests,
// with /img and /preview answered from canned bytes instead of the network
// (the real fetcher only reaches public addresses). Off unless
// WEBCHAT_E2E_WEB (web listen address) and WEBCHAT_E2E_TCP (chat TCP
// address) are set; runs until interrupted:
//
//	WEBCHAT_E2E_WEB=127.0.0.1:18090 WEBCHAT_E2E_TCP=127.0.0.1:18088 \
//	  go test -run TestE2EServe -timeout 0 ./internal/webchat
func TestE2EServe(t *testing.T) {
	webAddr, tcpAddr := os.Getenv("WEBCHAT_E2E_WEB"), os.Getenv("WEBCHAT_E2E_TCP")
	if webAddr == "" || tcpAddr == "" {
		t.Skip("set WEBCHAT_E2E_WEB and WEBCHAT_E2E_TCP to run the browser-test server")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	logger := log.New(os.Stderr, "e2e: ", log.Lmicroseconds)
	cs := chatserver.New(tcpAddr)
	cs.Logger = logger
	go func() { _ = cs.ListenAndServe(ctx) }()

	mcfg := chatmedia.Defaults()
	mcfg.Dir = t.TempDir()
	media, err := chatmedia.New(mcfg)
	if err != nil {
		t.Fatal(err)
	}
	media.Logger = logger

	cfg := Defaults()
	cfg.Addr = webAddr
	cfg.SystemName = "e2ebbs"
	cfg.CreateRooms = true
	web := New(cfg)
	web.Logger = logger
	web.Chat = cs
	web.Media = media
	web.Static = webpage.Standalone
	if web.Avatars, err = avatar.LoadBundled(); err != nil {
		t.Fatal(err)
	}
	if dir := os.Getenv("WEBCHAT_E2E_DIR"); dir != "" {
		web.cfg.WebDir = dir // serve the working copy without rebuilding
	}
	canned := e2eCanned()
	web.Images = cannedImages(canned)
	web.Pages = cannedPages(canned)
	if err := web.ListenAndServe(ctx); err != nil {
		t.Fatal(err)
	}
}

// Canned remote content: https://pics.e2e.example/<name> answers with the
// bytes under that name; anything else 404s.
func e2eCanned() map[string][]byte {
	return map[string][]byte{
		"https://pics.e2e.example/cat.png":      pngBytes(),
		"https://pics.e2e.example/nude-cat.png": pngBytes(),
		"https://news.e2e.example/story": []byte(`<html><head><title>x</title>` +
			`<meta property="og:title" content="E2E headline">` +
			`<meta property="og:description" content="A card fetched by the server, not the browser.">` +
			`<meta property="og:site_name" content="E2E News">` +
			`<meta property="og:image" content="https://pics.e2e.example/cat.png"></head></html>`),
	}
}

type cannedImages map[string][]byte

func (c cannedImages) Fetch(_ context.Context, u string) ([]byte, error) {
	if b, ok := c[u]; ok {
		return b, nil
	}
	return nil, inlineimg.ErrBlocked
}

type cannedPages map[string][]byte

func (c cannedPages) FetchPage(_ context.Context, u string, _ int64) (*inlineimg.Page, error) {
	b, ok := c[u]
	if !ok || !strings.Contains(string(b), "<html") {
		return nil, http.ErrMissingFile
	}
	return &inlineimg.Page{Body: b, FinalURL: u, ContentType: "text/html"}, nil
}
