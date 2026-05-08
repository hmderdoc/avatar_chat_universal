#!/usr/bin/env bash
#
# build-windows-xp.sh -- cross-compile avatar_chat_universal for the
# legacy windows/386 (Windows XP) target using Go 1.10.x.
#
# Why this exists: Go 1.10 is the last toolchain that produces XP-
# runnable Windows binaries (Go 1.11+ requires Win7+). Go 1.10 is also
# pre-modules, so we have to set up a GOPATH layout, clone old-but-
# compatible versions of the deps, and put a hand-rolled minimal
# golang.org/x/sys/windows shim in place. This script does all of that
# in a temporary GOPATH so your real Go install isn't touched.
#
# Output: dist/windows_386_xp/avatar_chat_universal.exe
#
# Run from repo root.

set -euo pipefail

# --- Locate Go 1.10 toolchain ---------------------------------------
GO110_DEFAULT="${HOME}/.local/go1.10/bin/go"
GO110="${GO110:-$GO110_DEFAULT}"
if [[ ! -x "$GO110" ]]; then
    echo "build-windows-xp: Go 1.10 toolchain not found at $GO110" >&2
    echo >&2
    echo "Install with:" >&2
    echo "  curl -sSL -o /tmp/go1.10.8.tar.gz https://dl.google.com/go/go1.10.8.linux-amd64.tar.gz" >&2
    echo "  tar -xzf /tmp/go1.10.8.tar.gz -C ~/.local/" >&2
    echo "  mv ~/.local/go ~/.local/go1.10" >&2
    echo "  rm /tmp/go1.10.8.tar.gz" >&2
    exit 1
fi

# --- Resolve repo + paths -------------------------------------------
REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
SHIM_SRC="$REPO_ROOT/compat/_legacy/golang.org/x/sys/windows/windows.go"
OUT_DIR="$REPO_ROOT/dist/windows_386_xp"
GOPATH_DIR="${TMPDIR:-/tmp}/acu-legacy-gopath"

if [[ ! -f "$SHIM_SRC" ]]; then
    echo "build-windows-xp: shim source not found at $SHIM_SRC" >&2
    exit 1
fi

# --- Clean / set up temp GOPATH -------------------------------------
echo "build-windows-xp: setting up GOPATH at $GOPATH_DIR"
rm -rf "$GOPATH_DIR"
mkdir -p "$GOPATH_DIR/src/golang.org/x/sys/windows"
mkdir -p "$GOPATH_DIR/src/golang.org/x"
mkdir -p "$GOPATH_DIR/src/github.com/mattn"
mkdir -p "$GOPATH_DIR/src/github.com/hmderdoc"

# Place our shim. This is the file the legacy build picks up instead
# of the modern (and Go-1.10-incompatible) x/sys/windows.
cp "$SHIM_SRC" "$GOPATH_DIR/src/golang.org/x/sys/windows/windows.go"

# Symlink the project itself into the GOPATH layout. Go 1.10 finds
# imports by GOPATH src paths; the import path
# "github.com/hmderdoc/avatar_chat_universal" must resolve to a real
# directory there.
ln -snf "$REPO_ROOT" "$GOPATH_DIR/src/github.com/hmderdoc/avatar_chat_universal"

# --- Clone deps at known-good commits -------------------------------
# go-colorable + go-isatty: current HEAD compiles cleanly on Go 1.10
# (they intentionally keep stdlib usage conservative). Pinned to
# specific shas would be safer for reproducibility; for now we take
# whatever HEAD is.
echo "build-windows-xp: cloning go-colorable + go-isatty"
git clone --quiet --depth 1 https://github.com/mattn/go-colorable.git \
    "$GOPATH_DIR/src/github.com/mattn/go-colorable"
git clone --quiet --depth 1 https://github.com/mattn/go-isatty.git \
    "$GOPATH_DIR/src/github.com/mattn/go-isatty"

# golang.org/x/term: also conservative re: stdlib. tty_unix.go uses
# term.IsTerminal / MakeRaw / Restore; all stable since 2017.
echo "build-windows-xp: cloning golang.org/x/term"
git clone --quiet --depth 1 https://github.com/golang/term.git \
    "$GOPATH_DIR/src/golang.org/x/term"

# --- Make sure no vendor/ shadows the GOPATH layout -----------------
# Modern builds populate vendor/ via `go mod vendor`. Go 1.10 prefers
# vendor/ over GOPATH if it exists, and the modern vendor contents
# don't compile on 1.10 (that's the whole reason this script exists).
# We don't delete the project's vendor/ -- it might be in use for the
# modern build -- but we DO delete it from the symlinked path Go 1.10
# sees. Since we symlinked, deleting through the symlink would nuke
# the real one too. Instead, we use GOFLAGS=-mod=mod to bypass it.
# But Go 1.10 doesn't know -mod=mod. So: temporarily move it.
PROJECT_VENDOR="$REPO_ROOT/vendor"
VENDOR_PARKED=""
if [[ -d "$PROJECT_VENDOR" ]]; then
    VENDOR_PARKED="$REPO_ROOT/vendor.parked.$$"
    mv "$PROJECT_VENDOR" "$VENDOR_PARKED"
fi
restore_vendor() {
    if [[ -n "$VENDOR_PARKED" && -d "$VENDOR_PARKED" ]]; then
        mv "$VENDOR_PARKED" "$PROJECT_VENDOR"
    fi
}
trap restore_vendor EXIT

# --- Build -----------------------------------------------------------
mkdir -p "$OUT_DIR"
echo "build-windows-xp: cross-compiling windows/386 with Go 1.10"
GOPATH="$GOPATH_DIR" GOOS=windows GOARCH=386 \
    "$GO110" build \
        -o "$OUT_DIR/avatar_chat_universal.exe" \
        github.com/hmderdoc/avatar_chat_universal/cmd/avatar_chat_universal

# Bundled assets that the disk-load fallback expects next to the
# binary (legacy build doesn't have //go:embed). Modern dist target
# embeds these; XP build ships them as files.
mkdir -p "$OUT_DIR/assets/avatars"
cp "$REPO_ROOT/internal/avatar/assets/avatars/"*.bin "$OUT_DIR/assets/avatars/" 2>/dev/null || true
cp "$REPO_ROOT/splash.ans" "$OUT_DIR/" 2>/dev/null || true

# Top-level files (config, themes, docs) match what the modern dist
# target ships.
cp "$REPO_ROOT/avatar_chat.ini" "$OUT_DIR/"
mkdir -p "$OUT_DIR/themes"
cp "$REPO_ROOT/themes/"*.ini "$OUT_DIR/themes/"
cp "$REPO_ROOT/README.md" "$REPO_ROOT/INSTALL.md" "$REPO_ROOT/CONFIG.md" \
    "$REPO_ROOT/LICENSE" "$REPO_ROOT/CHANGELOG.md" "$OUT_DIR/" 2>/dev/null || true

# --- Tar it up ------------------------------------------------------
ARCHIVE="$REPO_ROOT/dist/avatar_chat_universal_windows_386_xp.tar.gz"
( cd "$REPO_ROOT/dist" && tar czf "$ARCHIVE" "windows_386_xp" )

echo
echo "build-windows-xp: done"
echo "  binary:  $OUT_DIR/avatar_chat_universal.exe"
echo "  archive: $ARCHIVE"
ls -la "$OUT_DIR/avatar_chat_universal.exe"
file "$OUT_DIR/avatar_chat_universal.exe"
