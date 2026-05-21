package discordbridge

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotEnvSetsUnsetVarsAndPreservesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	body := "# comment\nexport FROM_ENV_FILE=\"hello\"\nALREADY_SET=fromfile\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ALREADY_SET", "fromenv")
	os.Unsetenv("FROM_ENV_FILE")
	t.Cleanup(func() { os.Unsetenv("FROM_ENV_FILE") })

	loaded, n, err := LoadDotEnv(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded != path || n != 1 {
		t.Fatalf("expected to load 1 var from %s, got path=%q n=%d", path, loaded, n)
	}
	if got := os.Getenv("FROM_ENV_FILE"); got != "hello" {
		t.Fatalf("FROM_ENV_FILE = %q, want hello", got)
	}
	if got := os.Getenv("ALREADY_SET"); got != "fromenv" {
		t.Fatalf("exported env should win over .env: ALREADY_SET = %q", got)
	}
}

func TestLoadConfigEnvTokenWinsOverIni(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bridge.ini")
	if err := os.WriteFile(path, []byte("[discord]\ntoken = ini-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DISCORD_BOT_TOKEN", "env-token")

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Discord.Token != "env-token" {
		t.Fatalf("env token should win, got %q", cfg.Discord.Token)
	}
}

func TestLoadConfigUsesIniTokenWhenNoEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bridge.ini")
	if err := os.WriteFile(path, []byte("[discord]\ntoken = ini-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	os.Unsetenv("DISCORD_BOT_TOKEN")

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Discord.Token != "ini-token" {
		t.Fatalf("ini token should apply when env unset, got %q", cfg.Discord.Token)
	}
}
