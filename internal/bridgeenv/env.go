// Package bridgeenv loads KEY=VALUE pairs from a .env file into the process
// environment so bridge secrets (bot tokens, access tokens) can live outside
// committed config. It is the shared implementation used by the Telegram,
// Matrix, and Slack bridges; the older Discord bridge carries its own copy.
package bridgeenv

import (
	"bufio"
	"os"
	"strings"
)

// LoadDotEnv reads the first existing file from paths and exports each KEY=VALUE
// pair into the process environment, without overwriting variables that are
// already set (an explicitly exported env var beats the .env file). It returns
// the path that was loaded and the number of variables set. A missing file is
// not an error.
func LoadDotEnv(paths ...string) (string, int, error) {
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		f, err := os.Open(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", 0, err
		}
		count, err := applyDotEnv(f)
		f.Close()
		if err != nil {
			return path, count, err
		}
		return path, count, nil
	}
	return "", 0, nil
}

func applyDotEnv(f *os.File) (int, error) {
	count := 0
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		if key == "" {
			continue
		}
		val := strings.TrimSpace(line[eq+1:])
		val = trimMatchingQuotes(val)
		if _, set := os.LookupEnv(key); set {
			continue
		}
		if err := os.Setenv(key, val); err != nil {
			return count, err
		}
		count++
	}
	return count, scanner.Err()
}

func trimMatchingQuotes(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
