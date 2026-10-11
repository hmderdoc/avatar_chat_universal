#!/bin/sh
# Browser check: mock.js stands in for the web server, check.js drives
# headless Chromium. Needs Playwright (PLAYWRIGHT=path to its package, or
# installed here) and Go (to encode a sample BITMAP with the door's encoder).
#
#   sh test/browser/run.sh [stock webv4 root]      (from web/synchronet, after npm run build)
set -e
HERE=$(cd "$(dirname "$0")" && pwd)
PKG=$(cd "$HERE/../.." && pwd)
REPO=$(cd "$PKG/../.." && pwd)
STOCK=${1:-/sbbs/webv4/root}
PORT=${AVATARCHAT_MOCK_PORT:-18190}
TMP=$(mktemp -d)
trap 'kill $PID 2>/dev/null || true; rm -rf "$TMP" "$REPO/web/synchronet/build/_gen"' EXIT

mkdir -p "$PKG/build/_gen"
cat >"$PKG/build/_gen/main.go" <<'GO'
package main

import (
	"fmt"

	"github.com/hmderdoc/avatar_chat_universal/internal/bitmap"
)

func main() {
	cells := make([]bitmap.Cell, 8*3)
	for i := range cells {
		cells[i] = bitmap.Cell{Char: byte('A' + i%26), FG: uint8(i % 16), BG: uint8((i / 3) % 8)}
	}
	fmt.Print(bitmap.Encode(8, 3, "Test BBS:Gopher", cells))
}
GO
(cd "$REPO" && go run -mod=vendor ./web/synchronet/build/_gen) >"$TMP/bitmap.txt"

GO_BITMAP_FILE="$TMP/bitmap.txt" node "$HERE/mock.js" "$PORT" "$STOCK" &
PID=$!
sleep 1
node "$HERE/check.js" "$PORT" "$TMP/shot.png"
