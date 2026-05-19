package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/hmderdoc/avatar_chat_universal/internal/ircbridge"
)

func main() {
	configPath := flag.String("config", "irc_bridge.ini", "path to bridge config")
	flag.Parse()

	cfg, err := ircbridge.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	b := &ircbridge.Bridge{Config: cfg, Logger: log.Default()}
	if err := b.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
