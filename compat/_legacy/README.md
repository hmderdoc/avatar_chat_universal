# Legacy Windows compatibility (XP target)

This directory contains everything needed to build a Windows-XP-runnable
`windows/386` binary using Go 1.10. See `INSTALL.md` for sysop-facing
docs; this README is for maintainers.

## Why this exists

The mainline build uses Go 1.21+ features (`//go:embed`, `errors.Is`,
generics, modern `golang.org/x/sys/windows`) and so produces binaries
that won't load on Windows pre-10. Major BBS sysops still on XP can't
use those binaries.

Go 1.10.x is the last toolchain that emits XP-runnable Windows
binaries. The cliff between Go 1.10 and Go 1.11+ is significant: 1.10
is pre-modules, pre-`embed`, pre-`errors.Is`, pre-`io/fs`, pre-most
modern conveniences.

To bridge that cliff without forking the source, we:

1. Restrict ourselves to a Go-1.10-compatible feature subset across
   the codebase (so `go1.26 build` and `go1.10 build` both compile
   the same `.go` files).
2. Use build tags (`go1.16` / `!go1.16`) to gate the few places where
   we actually need different code paths — currently:
   - `internal/avatar/bundle_{embed,disk}.go` (embed vs disk-load
     for bundled avatar collections)
   - `internal/ui/splash_{embed,disk}.go` (same for splash artwork)
3. Provide a hand-rolled minimal `golang.org/x/sys/windows` shim
   (this directory's `golang.org/x/sys/windows/windows.go`) that
   wraps the ~11 Win32 calls the door uses, via Go 1.10's stdlib
   `syscall.Syscall*` against `kernel32.dll` and `ws2_32.dll`. The
   real x/sys/windows is too modern; 2018-era versions have their
   own bugs.

`compat/_legacy/` starts with an underscore so Go's `./...` package
walking ignores it — modern builds never see this code.

## Building

From the repo root:

```sh
make dist-windows-xp
```

Produces `dist/windows_386_xp/avatar_chat_universal.exe`. The script
does the GOPATH dance internally; no permanent changes to your
environment.

Prerequisites:

- Go 1.10.x at `~/.local/go1.10/` (download from `https://dl.google.com/go/go1.10.8.linux-amd64.tar.gz`)
- Network access at build time (the script clones `go-colorable`,
  `go-isatty`, and `golang.org/x/term` at known-good commits)

## Files

| Path | Purpose |
|---|---|
| `golang.org/x/sys/windows/windows.go` | The shim. ~210 lines wrapping kernel32 + ws2_32 procs the door uses. Mirrors x/sys/windows's public surface for the 11 symbols we touch. |
| `build-windows-xp.sh` | Sets up a temp GOPATH at `$TMPDIR/acu-legacy-gopath/`, clones deps, copies shim into place, runs `go1.10 build` with `GOOS=windows GOARCH=386`. |

## Maintaining the shim

If you add a new x/sys/windows symbol to the main project's
`socket_windows.go` or `tty_windows.go`, the legacy build will fail
with `undefined: windows.Foo`. Add the symbol to the shim, calling
through `syscall.Syscall*` against the appropriate DLL.

For new constants, just add them to the const block — they're
untyped so the caller dictates the type.

For new structs, mirror x/sys/windows's field layout exactly,
including padding — Win32 calls are sensitive to struct shape.
