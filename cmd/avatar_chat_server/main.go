// avatar_chat_server is a drop-in self-hostable replacement for the
// futureland.today:10088 chat server used by avatar_chat_universal and the
// Synchronet JS avatar_chat door. Speaks the json-sock.js / json-client.js
// wire protocol so existing clients work unchanged — point the door's
// `host` and `port` config at this server's address and you're done.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/hmderdoc/avatar_chat_universal/internal/chatserver"
)

func main() {
	addr := flag.String("addr", ":10088", "TCP address to listen on (default :10088)")
	flag.Parse()

	srv := chatserver.New(*addr)
	srv.Logger = log.New(os.Stderr, "chatserver: ", log.LstdFlags|log.Lmicroseconds)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := srv.ListenAndServe(ctx); err != nil {
		log.Fatalf("server stopped: %v", err)
	}
}
