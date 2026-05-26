package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/hmderdoc/avatar_chat_universal/internal/bridgeenv"
	"github.com/hmderdoc/avatar_chat_universal/internal/telegrambridge"
)

func main() {
	configPath := flag.String("config", "telegram_bridge.ini", "path to bridge config")
	envPath := flag.String("env", ".env", "path to .env file (loaded before config)")
	flag.Parse()

	if path, n, err := bridgeenv.LoadDotEnv(*envPath, filepath.Join(filepath.Dir(*configPath), ".env")); err != nil {
		log.Printf("load .env: %v", err)
	} else if path != "" {
		log.Printf("loaded %d var(s) from %s", n, path)
	}

	cfg, err := telegrambridge.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	b := &telegrambridge.Bridge{Config: cfg, Logger: log.Default()}
	if err := b.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
