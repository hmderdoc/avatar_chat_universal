package chatmedia

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// LoadINI reads media keys from an INI file into cfg: media_addr, media_dir,
// media_url_base, max_image_mb, max_ansi_mb, retention_days, max_store_mb,
// uploads_per_hour, deny_file. Keys may sit at the top or under [media]; other
// sections are ignored. Lines starting with ; or # are comments.
func LoadINI(path string, cfg *Config) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	section := ""
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == ';' || line[0] == '#' {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(strings.TrimSpace(line[1 : len(line)-1]))
			continue
		}
		if section != "" && section != "media" {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.ToLower(strings.TrimSpace(k))
		if i := strings.IndexAny(v, ";#"); i >= 0 {
			v = v[:i]
		}
		v = strings.TrimSpace(v)
		num := func(dst *int) error {
			x, err := strconv.Atoi(v)
			if err != nil {
				return fmt.Errorf("%s:%d: %s wants a number", path, n, k)
			}
			*dst = x
			return nil
		}
		var e error
		switch k {
		case "media_addr":
			cfg.Addr = v
		case "media_dir":
			cfg.Dir = v
		case "media_url_base":
			cfg.URLBase = v
		case "max_image_mb":
			e = num(&cfg.MaxImageMB)
		case "max_ansi_mb":
			e = num(&cfg.MaxANSIMB)
		case "retention_days":
			e = num(&cfg.RetentionDays)
		case "max_store_mb":
			e = num(&cfg.MaxStoreMB)
		case "deny_file":
			cfg.DenyFile = v
		case "uploads_per_hour":
			e = num(&cfg.PerIPPerHour)
		}
		if e != nil {
			return e
		}
	}
	return sc.Err()
}
