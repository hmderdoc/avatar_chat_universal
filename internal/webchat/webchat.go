// Package webchat is the bundled server's web front end: one HTTP listener
// that serves the standalone chat page and everything the page needs, so a
// browser only ever talks to the origin it loaded the page from:
//
//	/              the page's static files (embedded, or Config.WebDir)
//	/chat          WebSocket carrying chat packets
//	/config.json   what the page needs to know (default channel, uploads,
//	               NSFW keywords, page styles)
//	/avatars/...   the avatar library, read only (avatars.go)
//	/preview?url=  a link's card, fetched here
//	/img?url=      a picture from a chat message, fetched here
//	/media?url=    a card's audio or video, streamed through (media.go)
//	/upload, /chatmedia/...  attachments, when media is configured
//
// Behind /chat the server either hosts the chat itself (Chat set: the
// local chatserver) or relays each browser to someone else's chat server
// over its TCP port (Config.Upstream). Either way every packet a browser
// sends passes a policy first (policy.go): only the operations the page
// uses, only public rooms, and the sender's nick and system stamped by the
// server so a web user can't speak as another board.
//
// /img and /preview fetch only URLs that have been seen in chat traffic
// sent to a browser (or a card's own picture), through inlineimg's
// address guards, with a per-address rate limit: they are not an open
// proxy for arbitrary URLs.
package webchat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/hmderdoc/avatar_chat_universal/internal/avatar"
	"github.com/hmderdoc/avatar_chat_universal/internal/chatmedia"
	"github.com/hmderdoc/avatar_chat_universal/internal/chatserver"
	"github.com/hmderdoc/avatar_chat_universal/internal/denylist"
	"github.com/hmderdoc/avatar_chat_universal/internal/inlineimg"
	"github.com/hmderdoc/avatar_chat_universal/internal/linkprev"
)

// Config is the front end's settings. Zero values take the defaults
// listed in Defaults.
type Config struct {
	Addr           string   // listen address, e.g. ":8080"
	Origins        []string // extra web origins or host names allowed on /chat; the page's own is always allowed
	WebDir         string   // serve the page from here instead of the built-in copy
	Upstream       string   // host:port of another chat server to relay to; "" = host the chat here
	SystemName     string   // the board name stamped on web users' messages and WHO entries
	DefaultChannel string   // the room the page opens in
	MaxConns       int      // browsers connected at once
	MaxPerIP       int      // browsers connected at once from one address
	CreateRooms    bool     // let web users register new public rooms
	MaxMessage     int      // longest message, in characters
	FetchPerMinute int      // /img plus /preview requests per address per minute
	ImageMaxMB     int      // largest picture /img passes through

	// NSFWKeywords veil pictures and link cards whose link or text has one
	// as a whole word, until clicked. With NSFWSet false the door's
	// default list applies; NSFWSet with no keywords turns the veil off.
	NSFWKeywords []string
	NSFWSet      bool
	Styles       []string // page style ids offered (web_styles); empty = all
	DefaultStyle string   // web_default_style

	// ClaimsFile keeps which browser holds each nickname (claims.go);
	// "" keeps claims in memory only, lost on restart. ClaimsPerIPDay caps
	// new names one address may claim per day.
	ClaimsFile     string
	ClaimsPerIPDay int

	// /media: the largest file streamed, and how many play at once.
	MediaProxyMaxMB int
	MediaStreams    int
}

// Defaults are the front end's built-in settings.
func Defaults() Config {
	return Config{
		SystemName:     "web",
		DefaultChannel: "main",
		MaxConns:       50,
		MaxPerIP:       5,
		MaxMessage:     1000,
		FetchPerMinute: 120,
		ImageMaxMB:     5,
		ClaimsPerIPDay: 5,

		MediaProxyMaxMB: 50,
		MediaStreams:    8,
	}
}

func (c *Config) fill() {
	d := Defaults()
	if c.SystemName == "" {
		c.SystemName = d.SystemName
	}
	if c.DefaultChannel == "" {
		c.DefaultChannel = d.DefaultChannel
	}
	if c.MaxConns <= 0 {
		c.MaxConns = d.MaxConns
	}
	if c.MaxPerIP <= 0 {
		c.MaxPerIP = d.MaxPerIP
	}
	if c.MaxMessage <= 0 {
		c.MaxMessage = d.MaxMessage
	}
	if c.FetchPerMinute <= 0 {
		c.FetchPerMinute = d.FetchPerMinute
	}
	if c.ImageMaxMB <= 0 {
		c.ImageMaxMB = d.ImageMaxMB
	}
	if c.ClaimsPerIPDay == 0 {
		c.ClaimsPerIPDay = d.ClaimsPerIPDay
	}
	if c.MediaProxyMaxMB <= 0 {
		c.MediaProxyMaxMB = d.MediaProxyMaxMB
	}
	if c.MediaStreams <= 0 {
		c.MediaStreams = d.MediaStreams
	}
}

// ImageFetcher is what /img needs from inlineimg.Fetcher.
type ImageFetcher interface {
	Fetch(ctx context.Context, rawURL string) ([]byte, error)
}

// Server is the front end. Set the exported fields before Handler or
// ListenAndServe.
type Server struct {
	cfg    Config
	Logger *log.Logger
	Deny   *denylist.List

	// Chat is the local chat server (when Config.Upstream is empty).
	Chat *chatserver.Server
	// Media, when set, is mounted at /upload and /chatmedia/. Its purge
	// loop is the caller's to run (ListenAndServe on its own port runs it).
	Media *chatmedia.Server
	// Static is the page's files; a file in Config.WebDir wins over it.
	Static fs.FS
	// Avatars is the library the page's Avatar modal offers, read only.
	Avatars []*avatar.Collection
	// Images and Pages fetch for /img and /preview; Previews caches cards.
	Images   ImageFetcher
	Pages    linkprev.PageFetcher
	Previews *linkprev.Cache
	// Streams opens the files /media plays; nil turns /media off.
	Streams MediaOpener

	rooms     *roomList
	claims    *claims
	seen      *seenURLs
	fetches   *rateLimit
	fetchQ    chan struct{} // in-flight /img and /preview fetches
	streamQ   chan struct{} // /media streams playing
	cardMedia cardMedia

	mu    sync.Mutex
	conns int
	perIP map[string]int

	pingEvery, pongWait time.Duration // keepalive; tests shorten them
	now                 func() time.Time
}

// New returns a front end for cfg.
func New(cfg Config) *Server {
	cfg.fill()
	s := &Server{
		cfg:       cfg,
		Logger:    log.Default(),
		seen:      newSeenURLs(10000, 24*time.Hour),
		fetches:   newRateLimit(cfg.FetchPerMinute, time.Minute),
		fetchQ:    make(chan struct{}, 8),
		streamQ:   make(chan struct{}, cfg.MediaStreams),
		perIP:     map[string]int{},
		pingEvery: 30 * time.Second,
		pongWait:  75 * time.Second,
		now:       time.Now,
	}
	s.rooms = &roomList{srv: s, ttl: 30 * time.Second}
	s.claims = newClaims(cfg.ClaimsFile, cfg.ClaimsPerIPDay, func() time.Time { return s.now() })
	return s
}

// Config returns the settings in use, defaults filled in.
func (s *Server) Config() Config { return s.cfg }

// Handler serves every route.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/chat", s.handleChat)
	mux.HandleFunc("/config.json", s.handleConfig)
	mux.HandleFunc("/img", s.handleImage)
	mux.HandleFunc("/preview", s.handlePreview)
	mux.HandleFunc("/media", s.handleMedia)
	mux.HandleFunc("/avatars/", s.handleAvatars)
	if s.Media != nil {
		mh := s.Media.Handler()
		mux.Handle("/upload", mh)
		mux.Handle("/chatmedia/", mh)
	}
	static := s.staticHandler()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" && websocket.IsWebSocketUpgrade(r) {
			s.handleChat(w, r) // the old ws_addr listener took upgrades at /
			return
		}
		static.ServeHTTP(w, r)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Deny.Denied(r.RemoteAddr) {
			s.Logger.Printf("web: refused %s (deny list)", r.RemoteAddr)
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// ListenAndServe serves until ctx ends.
func (s *Server) ListenAndServe(ctx context.Context) error {
	if s.cfg.Upstream == "" && s.Chat == nil {
		return errors.New("webchat: no chat server and no upstream")
	}
	lc := net.ListenConfig{}
	l, err := lc.Listen(ctx, "tcp", s.cfg.Addr)
	if err != nil {
		return fmt.Errorf("webchat: listen %s: %v", s.cfg.Addr, err)
	}
	mode := "hosting the chat here"
	if s.cfg.Upstream != "" {
		mode = "relaying to " + s.cfg.Upstream
	}
	s.Logger.Printf("web: page and /chat on %s, %s", l.Addr(), mode)
	hs := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		// Requests (and so /chat sockets) end with ctx.
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	go func() {
		<-ctx.Done()
		_ = hs.Close()
	}()
	if err := hs.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("webchat: %v", err)
	}
	return nil
}

// pageConfig is /config.json.
type pageConfig struct {
	DefaultChannel string `json:"defaultChannel"`
	SystemName     string `json:"systemName"`
	MaxMessage     int    `json:"maxMessage"`
	CreateRooms    bool   `json:"createRooms"`
	Uploads        bool   `json:"uploads"`
	Mode           string `json:"mode"` // "local" or "relay"

	NSFWKeywords []string    `json:"nsfwKeywords"`
	Styles       []PageStyle `json:"styles"`
	DefaultStyle string      `json:"defaultStyle"`
	Avatars      bool        `json:"avatars"` // the library has something to pick
}

// nsfwKeywords is the veil's list: the sysop's, or the door's default.
func (c Config) nsfwKeywords() []string {
	if c.NSFWSet {
		if c.NSFWKeywords == nil {
			return []string{}
		}
		return c.NSFWKeywords
	}
	return inlineimg.DefaultNSFWKeywords
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	mode := "local"
	if s.cfg.Upstream != "" {
		mode = "relay"
	}
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	styles := offeredStyles(s.cfg.Styles)
	index, _ := s.avatarIndex()
	_ = json.NewEncoder(w).Encode(pageConfig{
		DefaultChannel: s.cfg.DefaultChannel,
		SystemName:     s.cfg.SystemName,
		MaxMessage:     s.cfg.MaxMessage,
		CreateRooms:    s.cfg.CreateRooms,
		Uploads:        s.Media != nil,
		Mode:           mode,
		NSFWKeywords:   s.cfg.nsfwKeywords(),
		Styles:         styles,
		DefaultStyle:   defaultStyle(s.cfg.DefaultStyle, styles),
		Avatars:        len(index) > 0,
	})
}

// remoteIP is the socket's address without the port. X-Forwarded-For is
// not trusted: behind a reverse proxy every browser is the proxy.
func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
}

// acquire takes a connection slot for ip; release gives it back.
func (s *Server) acquire(ip string) (release func(), why string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conns >= s.cfg.MaxConns {
		return nil, "the web chat is full; try again later"
	}
	if s.perIP[ip] >= s.cfg.MaxPerIP {
		return nil, "too many chat windows open from your address"
	}
	s.conns++
	s.perIP[ip]++
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			s.conns--
			if s.perIP[ip]--; s.perIP[ip] <= 0 {
				delete(s.perIP, ip)
			}
			s.mu.Unlock()
		})
	}, ""
}
