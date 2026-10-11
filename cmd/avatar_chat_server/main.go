// avatar_chat_server is a drop-in self-hostable replacement for the
// futureland.today:10088 chat server used by avatar_chat_universal and the
// Synchronet JS avatar_chat door. Speaks the json-sock.js / json-client.js
// wire protocol so existing clients work unchanged — point the door's
// `host` and `port` config at this server's address and you're done.
//
// With -media-addr (or media_addr in -config) it also runs a small HTTP file
// drop for chat attachments: see MEDIA.md. -deny (deny_file) names a file of
// addresses to refuse on both. With -web-addr (web_addr, formerly ws_addr)
// it also serves the standalone web chat page and its WebSocket, and with
// -upstream (upstream) it hosts no chat itself but relays web users to
// another chat server: see WEB.md.
package main

import (
	"bufio"
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/hmderdoc/avatar_chat_universal/internal/chatmedia"
	"github.com/hmderdoc/avatar_chat_universal/internal/chatserver"
	"github.com/hmderdoc/avatar_chat_universal/internal/denylist"
	"github.com/hmderdoc/avatar_chat_universal/internal/inlineimg"
	"github.com/hmderdoc/avatar_chat_universal/internal/linkprev"
	"github.com/hmderdoc/avatar_chat_universal/internal/webchat"
	"github.com/hmderdoc/avatar_chat_universal/web/standalone"
)

func main() {
	addr := flag.String("addr", ":10088", "TCP address to listen on (default :10088)")
	configPath := flag.String("config", "", "optional INI file with the media_* and web keys (see MEDIA.md, WEB.md)")
	wc := webchat.Defaults()
	webAddr := flag.String("web-addr", "", "HTTP address for the web chat page and its WebSocket, e.g. :8080 (off when empty)")
	flag.StringVar(webAddr, "ws-addr", "", "old name for -web-addr")
	origins := flag.String("ws-origins", "", "comma-separated extra web origins or host names allowed on /chat (the page's own host always is)")
	flag.StringVar(&wc.WebDir, "web-dir", "", "serve the web page from this directory instead of the built-in copy")
	flag.StringVar(&wc.Upstream, "upstream", "", "host:port of another chat server to relay web users to; this server then hosts no chat itself")
	flag.StringVar(&wc.SystemName, "system-name", wc.SystemName, "board name stamped on web users' messages")
	flag.StringVar(&wc.DefaultChannel, "default-channel", wc.DefaultChannel, "room the web page opens in")
	flag.IntVar(&wc.MaxConns, "web-max-conns", wc.MaxConns, "web users connected at once")
	flag.IntVar(&wc.MaxPerIP, "web-max-per-ip", wc.MaxPerIP, "web users connected at once from one address")
	flag.BoolVar(&wc.CreateRooms, "web-create-rooms", false, "let web users register new public rooms")
	flag.IntVar(&wc.FetchPerMinute, "fetch-per-minute", wc.FetchPerMinute, "picture and link-card fetches per address per minute")
	flag.IntVar(&wc.ImageMaxMB, "image-max-mb", wc.ImageMaxMB, "largest picture the web page's /img passes through, MB")
	webCache := flag.String("web-cache-dir", "webcache", "where fetched pictures and link cards are cached (\"\" for none)")
	mcfg := chatmedia.Defaults()
	flag.StringVar(&mcfg.Addr, "media-addr", "", "HTTP address for chat attachments, e.g. :10089, or \"web\" for the web listener only (off when empty)")
	flag.StringVar(&mcfg.Dir, "media-dir", mcfg.Dir, "directory for stored attachments")
	flag.StringVar(&mcfg.URLBase, "media-url-base", "", "public base URL of the media endpoint (default the request's own host)")
	flag.IntVar(&mcfg.MaxImageMB, "max-image-mb", mcfg.MaxImageMB, "largest picture accepted, MB")
	flag.IntVar(&mcfg.MaxANSIMB, "max-ansi-mb", mcfg.MaxANSIMB, "largest ANSI art file accepted, MB")
	flag.IntVar(&mcfg.RetentionDays, "retention-days", mcfg.RetentionDays, "days an attachment is kept")
	flag.IntVar(&mcfg.MaxStoreMB, "max-store-mb", mcfg.MaxStoreMB, "total attachment storage, MB; uploads are refused when full")
	flag.IntVar(&mcfg.PerIPPerHour, "uploads-per-hour", mcfg.PerIPPerHour, "uploads allowed per remote address per hour (0 = unlimited)")
	flag.StringVar(&mcfg.DenyFile, "deny", "", "file of IP addresses and CIDR ranges to refuse, chat, web and media alike (re-read when it changes)")
	flag.Parse()

	// The INI fills in what the command line left at its default; an
	// explicit flag always wins.
	if *configPath != "" {
		fileCfg := chatmedia.Defaults()
		if err := chatmedia.LoadINI(*configPath, &fileCfg); err != nil {
			log.Fatalf("config: %v", err)
		}
		set := map[string]bool{}
		flag.Visit(func(f *flag.Flag) { set[f.Name] = true })
		pick := func(name string, flagVal *string, fileVal string) {
			if !set[name] {
				*flagVal = fileVal
			}
		}
		pickInt := func(name string, flagVal *int, fileVal int) {
			if !set[name] {
				*flagVal = fileVal
			}
		}
		pick("media-addr", &mcfg.Addr, fileCfg.Addr)
		pick("media-dir", &mcfg.Dir, fileCfg.Dir)
		pick("media-url-base", &mcfg.URLBase, fileCfg.URLBase)
		pickInt("max-image-mb", &mcfg.MaxImageMB, fileCfg.MaxImageMB)
		pickInt("max-ansi-mb", &mcfg.MaxANSIMB, fileCfg.MaxANSIMB)
		pickInt("retention-days", &mcfg.RetentionDays, fileCfg.RetentionDays)
		pickInt("max-store-mb", &mcfg.MaxStoreMB, fileCfg.MaxStoreMB)
		pickInt("uploads-per-hour", &mcfg.PerIPPerHour, fileCfg.PerIPPerHour)
		pick("deny", &mcfg.DenyFile, fileCfg.DenyFile)

		keys, err := loadWebINI(*configPath)
		if err != nil {
			log.Fatalf("config: %v", err)
		}
		str := func(flagName string, dst *string, iniKeys ...string) {
			if set[flagName] || (flagName == "web-addr" && set["ws-addr"]) {
				return
			}
			for _, k := range iniKeys {
				if v, ok := keys[k]; ok {
					*dst = v
				}
			}
		}
		num := func(flagName string, dst *int, iniKey string) {
			if v, ok := keys[iniKey]; ok && !set[flagName] {
				n, err := strconv.Atoi(v)
				if err != nil {
					log.Fatalf("config: %s = %q is not a number", iniKey, v)
				}
				*dst = n
			}
		}
		str("web-addr", webAddr, "ws_addr", "web_addr")
		str("ws-origins", origins, "ws_origins", "web_origins")
		str("web-dir", &wc.WebDir, "web_dir")
		str("upstream", &wc.Upstream, "upstream")
		str("system-name", &wc.SystemName, "system_name")
		str("default-channel", &wc.DefaultChannel, "default_channel")
		str("web-cache-dir", webCache, "web_cache_dir")
		num("web-max-conns", &wc.MaxConns, "web_max_conns")
		num("web-max-per-ip", &wc.MaxPerIP, "web_max_per_ip")
		num("fetch-per-minute", &wc.FetchPerMinute, "fetch_per_minute")
		num("image-max-mb", &wc.ImageMaxMB, "image_max_mb")
		if v, ok := keys["web_create_rooms"]; ok && !set["web-create-rooms"] {
			wc.CreateRooms = iniBool(v)
		}
	}
	wc.Addr = *webAddr
	for _, o := range strings.Split(*origins, ",") {
		if o = strings.TrimSpace(o); o != "" {
			wc.Origins = append(wc.Origins, o)
		}
	}

	logger := log.New(os.Stderr, "chatserver: ", log.LstdFlags|log.Lmicroseconds)
	var deny *denylist.List
	if mcfg.DenyFile != "" {
		deny = denylist.New(mcfg.DenyFile, logger)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if wc.Upstream != "" && wc.Addr == "" {
		log.Fatalf("upstream = %s relays web users, so it needs web_addr (-web-addr) too", wc.Upstream)
	}
	mediaOnWebOnly := mcfg.Addr == "web" || (mcfg.Addr != "" && mcfg.Addr == wc.Addr)
	if mediaOnWebOnly && wc.Addr == "" {
		log.Fatalf("media_addr = web needs web_addr (-web-addr)")
	}

	var media *chatmedia.Server
	if mcfg.Addr != "" {
		var err error
		if media, err = chatmedia.New(mcfg); err != nil {
			log.Fatalf("media: %v", err)
		}
		media.Logger = logger
		media.Deny = deny
		if mediaOnWebOnly {
			go media.PurgeLoop(ctx)
		} else {
			go func() {
				if err := media.ListenAndServe(ctx); err != nil {
					log.Fatalf("media endpoint stopped: %v", err)
				}
			}()
		}
	}

	var srv *chatserver.Server
	if wc.Upstream == "" {
		srv = chatserver.New(*addr)
		srv.Logger = logger
		srv.Deny = deny
	}

	if wc.Addr != "" {
		web := webchat.New(wc)
		web.Logger = logger
		web.Deny = deny
		web.Chat = srv
		web.Static = standalone.Files
		web.Media = media
		web.Images, web.Pages, web.Previews = webFetchers(*webCache, web.Config().ImageMaxMB)
		run := func() {
			if err := web.ListenAndServe(ctx); err != nil {
				log.Fatalf("web listener stopped: %v", err)
			}
		}
		if srv == nil {
			run() // relay only: no TCP chat here
			return
		}
		go run()
	}

	if err := srv.ListenAndServe(ctx); err != nil {
		log.Fatalf("server stopped: %v", err)
	}
}

// webFetchers builds the web page's picture and link-card fetchers, both
// behind inlineimg's address guards, cached under dir ("" = no cache).
func webFetchers(dir string, imageMaxMB int) (*inlineimg.Fetcher, *inlineimg.Fetcher, *linkprev.Cache) {
	imgDir, cardDir := "", ""
	if dir != "" {
		imgDir, cardDir = filepath.Join(dir, "images"), filepath.Join(dir, "linkpreviews")
	}
	img := inlineimg.NewFetcher(imgDir)
	img.MaxBytes = int64(imageMaxMB) << 20
	pages := inlineimg.NewFetcher("")
	return img, pages, &linkprev.Cache{Dir: cardDir}
}

func iniBool(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "on", "yes", "true":
		return true
	}
	return false
}

// loadWebINI reads the web listener's keys from the top of the INI or its
// [web] or [websocket] section, lower-cased.
func loadWebINI(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]string{}
	section := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == ';' || line[0] == '#' {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(strings.TrimSpace(line[1 : len(line)-1]))
			continue
		}
		if section != "" && section != "websocket" && section != "web" {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if i := strings.IndexAny(v, ";#"); i >= 0 {
			v = v[:i]
		}
		out[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
	}
	return out, sc.Err()
}
