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
	".f16":   "application/octet-stream", // the ANSI editor's VGA font
	".txt":   "text/plain; charset=utf-8",
}

// pageCSP keeps the page on its own origin: scripts, styles, sockets,
// pictures and players from here only ('self' covers ws:// and wss:// to
// this host), plus data: and blob: pictures the page draws itself.
const pageCSP = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data: blob:; " +
	"media-src 'self'; connect-src 'self'; font-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'"

func (s *Server) staticHandler() http.Handler {
	// A sysop's web_dir copy wins file by file; anything it leaves out
	// (the shared editor and styles, say) comes from the built-in page.
	files := s.Static
	if s.cfg.WebDir != "" {
		files = overlay{os.DirFS(s.cfg.WebDir), s.Static}
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
		ct, ok := staticTypes[strings.ToLower(path.Ext(name))]
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

// overlay reads from top, then from under when top lacks the file.
type overlay struct{ top, under fs.FS }

func (o overlay) Open(name string) (fs.File, error) {
	f, err := o.top.Open(name)
	if err == nil || o.under == nil {
		return f, err
	}
	return o.under.Open(name)
}
