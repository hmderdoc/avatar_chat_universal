package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTrailingComments(t *testing.T) {
	ini := "image_sixel = on        ; auto | on | off\n" +
		"image_cache_dir =         ; default: <data dir>/imagecache\n" +
		"host = example.com;port\n" +
		"tv_color = truecolor\t; 16 | truecolor\n"
	p := filepath.Join(t.TempDir(), "a.ini")
	if err := os.WriteFile(p, []byte(ini), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ImageSixel != "on" {
		t.Errorf("image_sixel = %q, want on", cfg.ImageSixel)
	}
	if cfg.Raw["image_cache_dir"] != "" {
		t.Errorf("image_cache_dir = %q, want empty", cfg.Raw["image_cache_dir"])
	}
	if cfg.Host != "example.com;port" {
		t.Errorf("host = %q, want the ; kept", cfg.Host)
	}
	if cfg.TVColor != "truecolor" {
		t.Errorf("tv_color = %q, want truecolor", cfg.TVColor)
	}
}
