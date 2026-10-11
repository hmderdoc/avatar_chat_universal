// avatar_chat_server is a drop-in self-hostable replacement for the
// futureland.today:10088 chat server used by avatar_chat_universal and the
// Synchronet JS avatar_chat door. Speaks the json-sock.js / json-client.js
// wire protocol so existing clients work unchanged — point the door's
// `host` and `port` config at this server's address and you're done.
//
// With -media-addr (or media_addr in -config) it also runs a small HTTP file
// drop for chat attachments: see MEDIA.md. -deny (deny_file) names a file of
// addresses to refuse on both. With -ws-addr (ws_addr) it also takes the
// same packets over WebSocket, for browser clients.
package main

import (
	"bufio"
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/hmderdoc/avatar_chat_universal/internal/chatmedia"
	"github.com/hmderdoc/avatar_chat_universal/internal/chatserver"
	"github.com/hmderdoc/avatar_chat_universal/internal/denylist"
)

func main() {
	addr := flag.String("addr", ":10088", "TCP address to listen on (default :10088)")
	configPath := flag.String("config", "", "optional INI file with the media_* and ws_* keys (see MEDIA.md, WEB.md)")
	wsAddr := flag.String("ws-addr", "", "WebSocket address for browser clients, e.g. :10090 (off when empty)")
	wsOrigins := flag.String("ws-origins", "", "comma-separated web origins or host names allowed to connect over WebSocket (default any)")
	mcfg := chatmedia.Defaults()
	flag.StringVar(&mcfg.Addr, "media-addr", "", "HTTP address for chat attachments, e.g. :10089 (off when empty)")
	flag.StringVar(&mcfg.Dir, "media-dir", mcfg.Dir, "directory for stored attachments")
	flag.StringVar(&mcfg.URLBase, "media-url-base", "", "public base URL of the media endpoint (default http://<request host>)")
	flag.IntVar(&mcfg.MaxImageMB, "max-image-mb", mcfg.MaxImageMB, "largest picture accepted, MB")
	flag.IntVar(&mcfg.MaxANSIMB, "max-ansi-mb", mcfg.MaxANSIMB, "largest ANSI art file accepted, MB")
	flag.IntVar(&mcfg.RetentionDays, "retention-days", mcfg.RetentionDays, "days an attachment is kept")
	flag.IntVar(&mcfg.MaxStoreMB, "max-store-mb", mcfg.MaxStoreMB, "total attachment storage, MB; uploads are refused when full")
	flag.IntVar(&mcfg.PerIPPerHour, "uploads-per-hour", mcfg.PerIPPerHour, "uploads allowed per remote address per hour (0 = unlimited)")
	flag.StringVar(&mcfg.DenyFile, "deny", "", "file of IP addresses and CIDR ranges to refuse, chat and media alike (re-read when it changes)")
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
		fileWSAddr, fileWSOrigins, err := loadWSINI(*configPath)
		if err != nil {
			log.Fatalf("config: %v", err)
		}
		pick("ws-addr", wsAddr, fileWSAddr)
		pick("ws-origins", wsOrigins, fileWSOrigins)
	}

	srv := chatserver.New(*addr)
	srv.Logger = log.New(os.Stderr, "chatserver: ", log.LstdFlags|log.Lmicroseconds)
	var deny *denylist.List
	if mcfg.DenyFile != "" {
		deny = denylist.New(mcfg.DenyFile, srv.Logger)
	}
	srv.Deny = deny
	srv.WSAddr = *wsAddr
	for _, o := range strings.Split(*wsOrigins, ",") {
		if o = strings.TrimSpace(o); o != "" {
			srv.WSOrigins = append(srv.WSOrigins, o)
		}
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if mcfg.Addr != "" {
		media, err := chatmedia.New(mcfg)
		if err != nil {
			log.Fatalf("media: %v", err)
		}
		media.Logger = srv.Logger
		media.Deny = deny
		go func() {
			if err := media.ListenAndServe(ctx); err != nil {
				log.Fatalf("media endpoint stopped: %v", err)
			}
		}()
	}

	if srv.WSAddr != "" {
		go func() {
			if err := srv.ListenAndServeWS(ctx); err != nil {
				log.Fatalf("websocket stopped: %v", err)
			}
		}()
	}

	if err := srv.ListenAndServe(ctx); err != nil {
		log.Fatalf("server stopped: %v", err)
	}
}

// loadWSINI reads ws_addr and ws_origins from the top of the INI or its
// [websocket] section.
func loadWSINI(path string) (addr, origins string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer f.Close()
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
		if section != "" && section != "websocket" {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if i := strings.IndexAny(v, ";#"); i >= 0 {
			v = v[:i]
		}
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "ws_addr":
			addr = strings.TrimSpace(v)
		case "ws_origins":
			origins = strings.TrimSpace(v)
		}
	}
	return addr, origins, sc.Err()
}
