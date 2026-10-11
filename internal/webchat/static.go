package webchat

import (
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
)

// staticTypes are the only files / serves, with fixed types.
var staticTypes = map[string]string{
	".html":  "text/html; charset=utf-8",
	".js":    "text/javascript; charset=utf-8",
	".css":   "text/css; charset=utf-8",
	".png":   "image/png",
	".ico":   "image/x-icon",
	".woff2": "font/woff2",
}

// pageCSP keeps the page on its own origin: scripts, styles, sockets and
// pictures from here only ('self' covers ws:// and wss:// to this host),
// plus data: and blob: pictures the page draws itself.
const pageCSP = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data: blob:; " +
	"connect-src 'self'; font-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'"

func (s *Server) staticHandler() http.Handler {
	files := s.Static
	if s.cfg.WebDir != "" {
		files = os.DirFS(s.cfg.WebDir)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		ct, ok := staticTypes[path.Ext(name)]
		if !ok || files == nil || strings.HasPrefix(path.Base(name), ".") {
			http.NotFound(w, r)
			return
		}
		body, err := fs.ReadFile(files, name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		h := w.Header()
		h.Set("Content-Type", ct)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-cache")
		if path.Ext(name) == ".html" {
			h.Set("Content-Security-Policy", pageCSP)
			h.Set("X-Frame-Options", "SAMEORIGIN")
		}
		if r.Method == http.MethodHead {
			return
		}
		_, _ = w.Write(body)
	})
}
