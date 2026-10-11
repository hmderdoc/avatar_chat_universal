#!/bin/sh
# Integration test: the built SSJS under jsexec against a throwaway chat
# server on a high localhost port. Never touches a live chat service or the
# BBS's data directory (harness.js points every path into a sandbox).
#
#   sh test/jsexec/run.sh [jsexec path]     (from web/synchronet, after npm run build)
set -e
JSEXEC=${1:-/sbbs/exec/jsexec}
HERE=$(cd "$(dirname "$0")" && pwd)
PKG=$(cd "$HERE/../.." && pwd)
REPO=$(cd "$PKG/../.." && pwd)
TMP=$(mktemp -d)
PORT=${AVATARCHAT_TEST_PORT:-18188}
trap 'kill $PID 2>/dev/null || true; rm -rf "$TMP"' EXIT

(cd "$REPO" && CGO_ENABLED=0 go build -trimpath -o "$TMP/avatar_chat_server" ./cmd/avatar_chat_server)
"$TMP/avatar_chat_server" -addr "127.0.0.1:$PORT" >"$TMP/server.log" 2>&1 &
PID=$!
sleep 1

SB="$TMP/sandbox"
mkdir -p "$SB/exec" "$SB/data/user" "$SB/webroot" "$SB/xtrn/avatar_chat_universal"
cat >"$SB/xtrn/avatar_chat_universal/avatar_chat.ini" <<INI
host = 127.0.0.1
port = $PORT
default_channel = main
motd_channel = motd
link_previews = true

[web]
web_uploads = on
web_upload_max_mb = 2
web_upload_retention_days = 7
INI
"$JSEXEC" -n "$HERE/harness.js" "$SB/" "$PKG/dist/webv4/"
