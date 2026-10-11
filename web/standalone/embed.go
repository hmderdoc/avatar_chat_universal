// Package standalone holds the standalone web chat page (index.html,
// app.js, codec.js, style.css, cp437.png), embedded into
// avatar_chat_server, which serves it at / on its web listener. A sysop
// can serve a modified copy instead with web_dir.
package standalone

import "embed"

// Files is the page.
//
//go:embed index.html app.js codec.js style.css cp437.png
var Files embed.FS
