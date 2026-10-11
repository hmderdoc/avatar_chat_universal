// Package web holds the bundled server's standalone chat page. It is the
// files in web/standalone plus, under shared/, the pieces it uses from the
// Synchronet page's build (web/synchronet/dist/webv4/root/avatarchat): the
// page-style stylesheet and the vendored ANSI editor with its font. The
// two pages wear the same styles and draw with the same editor, and the
// standalone one never carries a stale copy of either.
package web

import (
	"embed"
	"io/fs"
	"strings"
)

//go:embed standalone/index.html standalone/app.js standalone/art.js standalone/codec.js standalone/style.css standalone/cp437.png
//go:embed synchronet/dist/webv4/root/avatarchat/app.css
//go:embed synchronet/dist/webv4/root/avatarchat/ansi-editor.js
//go:embed synchronet/dist/webv4/root/avatarchat/ansi-editor-modal.js
//go:embed synchronet/dist/webv4/root/avatarchat/ansi-editor-modal.css
//go:embed synchronet/dist/webv4/root/avatarchat/fonts/CP437.F16
//go:embed synchronet/dist/webv4/root/avatarchat/fonts/LICENSE-moebius.txt
//go:embed synchronet/editor/src/editor.css
var files embed.FS

// SharedDir is where shared/ comes from, relative to this package.
const SharedDir = "synchronet/dist/webv4/root/avatarchat"

// Standalone is the page as the server serves it: index.html and friends
// at the top, the Synchronet build's shared pieces under shared/.
var Standalone fs.FS = Merge(mustSub(files, "standalone"), mustSub(files, SharedDir))

// Merge serves page at the top and shared under shared/.
func Merge(page, shared fs.FS) fs.FS { return merged{page: page, shared: shared} }

type merged struct{ page, shared fs.FS }

// editorCSS is the editor's own stylesheet, which its build inlines; the
// page links it as shared/editor.css (id me-editor-styles) so the editor
// doesn't inject a <style> the page's CSP would refuse.
const editorCSS = "synchronet/editor/src/editor.css"

func (m merged) Open(name string) (fs.File, error) {
	if name == "shared/editor.css" {
		return files.Open(editorCSS)
	}
	if rest := strings.TrimPrefix(name, "shared/"); rest != name {
		return m.shared.Open(rest)
	}
	return m.page.Open(name)
}

func mustSub(f fs.FS, dir string) fs.FS {
	s, err := fs.Sub(f, dir)
	if err != nil {
		panic(err)
	}
	return s
}
