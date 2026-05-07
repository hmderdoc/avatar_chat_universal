# avatar_chat_universal — Sysop Install Guide

This is the full installation reference. For a one-page overview see
[README.md](README.md); for individual feature docs see
[CONFIG.md](CONFIG.md), [THEMING.md](THEMING.md), [AVATARS.md](AVATARS.md),
and [SCREENSAVER.md](SCREENSAVER.md).

The door is one static native binary plus an `avatar_chat.ini`. No shared
libraries. Drop both into a directory your BBS can launch from, configure
the BBS to call it with a drop file, done.

---

## Building

You need Go 1.22 or newer.

### Host platform

```sh
make build
```

Produces `avatar_chat_universal` and `avatar_chat_server` for your current
GOOS/GOARCH.

### All supported platforms

```sh
make dist
```

Produces `dist/<goos>_<goarch>/` directories each containing the binary,
`avatar_chat.ini`, and INSTALL.md, along with a tarball of each. Targets:

| Target            | Typical use                                          |
| ----------------- | ---------------------------------------------------- |
| `linux_amd64`     | Most Linux BBS hosts                                 |
| `linux_arm64`     | Raspberry Pi 4/5, AWS Graviton, Apple Silicon Linux  |
| `linux_386`       | Old 32-bit Linux installs                            |
| `windows_amd64`   | Windows BBS (Synchronet for Windows, Mystic Windows) |
| `windows_386`     | Older 32-bit Windows BBS hosts                       |
| `darwin_amd64`    | Intel macOS (sysop's local dev / homebrew BBS)       |
| `darwin_arm64`    | Apple Silicon macOS                                  |

Binaries are built with `-trimpath -ldflags='-s -w'` and `CGO_ENABLED=0`,
so they're fully static and ship clean across distros.

DOS / OS/2 / Amiga: out of scope for the Go port. Use the JS `avatar_chat`
door if your BBS runs there.

---

## Synchronet

### 1. Install the binary

Put `avatar_chat_universal` and `avatar_chat.ini` in a directory under
`/sbbs/xtrn/`, e.g. `/sbbs/xtrn/avatar_chat_universal/`. On Windows it's
the same idea, just `avatar_chat_universal.exe` under `c:\sbbs\xtrn\`.

### 2. Configure SCFG → External Programs → Online Programs (Doors)

Add a new entry:

| Field                       | Value                                          |
| --------------------------- | ---------------------------------------------- |
| Name                        | `Avatar Chat Universal`                        |
| Internal Code               | `AVCHATU`                                      |
| Start-up Directory          | `../xtrn/avatar_chat_universal/`               |
| Command Line                | `avatar_chat_universal -dropfile %f`           |
| Multiple Concurrent Users   | `Yes`                                          |
| Native (32-bit) Executable  | `Yes`                                          |
| I/O Method                  | `Socket` (recommended)                         |
| BBS Drop File Type          | `DOOR32.SYS`                                   |
| Place Drop File In          | `Node Directory`                               |

> **Critical:** no `?` prefix on the command line. `?` tells Synchronet to
> run the file as a JavaScript module
> ([xtrn.cpp:285-289](../../repo/src/sbbs3/xtrn.cpp#L285-L289)); we want the
> native path. `*` is the Baja prefix and is also wrong here. Native is
> selected by `Native Executable: Yes`.

`%f` expands to the absolute path of the DOOR32.SYS file Synchronet writes
for the session.

### 3. I/O method — pick `Socket`

| Mode       | When                                                                                          |
| ---------- | --------------------------------------------------------------------------------------------- |
| `Socket`   | **Use this for chat.** Synchronet hands the door the user's TCP socket FD via DOOR32.SYS line 2; the door reads/writes the socket directly. Single keypresses (Esc, arrows) reach the door immediately. The door auto-detects this from DOOR32 comm type 2 (Telnet) or 3 (Raw). |
| `Standard` | Synchronet pipes the user's terminal through the door's stdin/stdout. Synchronet line-buffers stdin: bare Esc and individual arrow keys don't reach the door until the user also presses Enter. Synchronet also does its own echo, which double-prints typed characters over our frame buffer. **Avoid for chat.** |

### 4. Reload SCFG and test

`bbs.menu('xtrn_sec')` or whatever menu hosts the door. The door inherits
its avatar/data path from `-data` (default: `<bin dir>/data`); make sure
that directory is writable by the user Synchronet runs as.

---

## Mystic

### 1. Install the binary

Put the binary somewhere stable, e.g. `/mystic/doors/avatar_chat_universal/`.

### 2. Run `mutil`

`Edit door menu` → add a new entry:

| Field      | Value                                                                            |
| ---------- | -------------------------------------------------------------------------------- |
| Name       | `Avatar Chat Universal`                                                          |
| Cmd        | `/mystic/doors/avatar_chat_universal/avatar_chat_universal -dropfile %PDOOR32.SYS` |
| Type       | `Shell`                                                                          |
| Drop File  | `DOOR32.SYS`                                                                     |
| OS         | `Same as Mystic`                                                                 |

`%P` is Mystic's per-user temp directory (e.g. `/mystic/temp2/` for
node 2). Concatenate the dropfile name onto it as shown — Mystic
substitutes `%P` and the door receives the full path
`/mystic/temp2/DOOR32.SYS`. Older docs that used `*F` were wrong for
Mystic; that placeholder belongs to other BBS softwares.

If the door fails with `socket fd N not usable in this process` after
the user is dropped into it, your Mystic build isn't inheriting the
user socket fd to the child. Add `-io stdio` to the Cmd line — the
door will read/write through stdin/stdout (which Mystic always wires
up) instead of the fd in DOOR32.SYS. See
[Troubleshooting](#troubleshooting).

### 3. Standalone fallback

If the door32.sys path is troublesome on your Mystic build, you can
also run the door without a drop file:

```
/mystic/doors/avatar_chat_universal/avatar_chat_universal \
    -bbs "Your BBS Name" -charset cp437 -user %U
```

The `-bbs` flag overrides the displayed origin label, `-user %U` pulls
in the Mystic-substituted username, and the door uses stdio I/O.
Avatars don't persist across users in this mode unless you also set
`bbs_id =` in `avatar_chat.ini` (place it next to the binary, not at
the Mystic root, so it's found regardless of Mystic's CWD).

### 4. Add to a theme menu

Edit `theme/<theme>/doors.mnu` and add a menu entry pointing at the door
you just registered.

---

## ENiGMA½

ENiGMA½ supports DOOR32.SYS doors via the `abracadabra` module.

### 1. Install the binary

`/enigma-bbs/doors/avatar_chat_universal/avatar_chat_universal`.

### 2. Add a door to your menu config (`config/menu.hjson`):

```hjson
avatarChatDoor: {
    desc: Avatar Chat Universal
    module: abracadabra
    config: {
        name: avatarChat
        dropFileType: DOOR32
        cmd: /enigma-bbs/doors/avatar_chat_universal/avatar_chat_universal
        args: [
            "-dropfile",
            "{dropFile}"
        ]
        nodeMax: 32
        encoding: cp437
        io: socket
    }
}
```

Adjust `nodeMax` to your node count. `encoding: cp437` is correct for the
SyncTERM/NetRunner crowd; users on browser-based clients may want
`encoding: utf8` (also set `output_charset = utf8` in `avatar_chat.ini` —
see [CONFIG.md](CONFIG.md)).

Wire that into a parent menu's `submenu`/`prompt` block per the standard
ENiGMA½ menu pattern.

---

## Generic BBS softwares

If your BBS just runs DOS-era doors via a DOOR32.SYS drop file in the node
directory, the door should work as long as:

1. Your BBS hands the door the path of the drop file as `%f` (or whatever
   substitution token your BBS uses) on the command line.
2. The platform native binary matches what your BBS host runs.
3. I/O is socket (DOOR32 comm type 2 or 3) or stdio (CommType 6). The
   door auto-detects from DOOR32 line 2.

DOOR.SYS (the older 52-line format) is also supported as a fallback if
your BBS doesn't emit DOOR32. Pass it the same way: `-dropfile %f`.

---

## Standalone (no BBS)

The door doubles as a regular CLI program: connect from your terminal,
chat, exit. Useful for testing, sysop personal use, or running the door
as a private chat client against your own server.

```sh
./avatar_chat_universal -user alice
```

What happens with no `-dropfile`:

- Username comes from `-user`, `$USER`, or "guest" (in that order).
- BBS / system name comes from `-bbs`, `$HOSTNAME`, or "standalone".
- The local TTY is put into raw mode via `golang.org/x/term`; restored on
  exit even on panic / Ctrl-C.
- `host`/`port` from `avatar_chat.ini` apply normally — defaults to
  `futureland.today:10088`. Point at `127.0.0.1:10088` (or wherever) if
  you're running the bundled server.

This mode is also handy for development: spin up the server locally,
launch the door, exercise the UI without touching Synchronet or any other
BBS.

---

## Self-hosting the chat server

The bundled server speaks the same line-delimited JSON-RPC protocol as
`futureland.today:10088`, so any avatar_chat door (this one OR the JS one)
can point at it.

```sh
./avatar_chat_server -addr :10088 &
```

Then in `avatar_chat.ini`:

```ini
host = localhost
port = 10088
```

Users on either door, both pointed at your server, share channels.

State is in-memory only for now; restart the server and history is gone.
Persist-to-disk is on the roadmap.

---

## Verifying the install

A short checklist after first launch:

1. **Door runs at all** — telnet/SSH into the BBS, launch the door from
   its menu, see a splash followed by the chat UI. If the screen is
   blank or the door exits immediately, check Synchronet/Mystic logs.
2. **Single keystrokes work** — type a couple chars and a `/`. If
   characters don't appear until you press Enter, you're in line-buffered
   mode (Synchronet's `Standard` I/O Method). Switch to `Socket`.
3. **CP437 art renders** — type `/avatar` and you should see a grid of
   colored 10×6 sprites. If they're garbled high-byte sequences, your
   client is in UTF-8 mode and you need either `output_charset = utf8`
   in the ini, or to launch with `-charset cp437`.
4. **Chat round-trips** — type `hello` and Enter. If it goes through, the
   chat server is reachable. If you get `connection lost, reconnecting...`
   the host/port in the ini is wrong, the public server is down, or your
   firewall blocks outbound TCP to port 10088.
5. **Avatars persist** — pick an avatar, exit, re-launch. You should keep
   the same one. If not, the `-data` directory isn't writable by the
   user the BBS runs as.
6. **Idle works** — sit at the chat for `idle_timeout_seconds` (default
   180s; lower it for testing). The transcript area should swap to a
   procedural animation. See [SCREENSAVER.md](SCREENSAVER.md).
7. **Node frees on disconnect** — telnet in, launch the door, kill your
   telnet session abruptly. Verify the BBS reports the node is free
   within a few seconds.

---

## Command-line flags

| Flag                      | Default                       | Notes                                                                                   |
| ------------------------- | ----------------------------- | --------------------------------------------------------------------------------------- |
| `-dropfile <path>`        | (empty → standalone mode)     | DOOR32.SYS or DOOR.SYS path. Required for door mode; omit for standalone.               |
| `-io stdio\|socket`       | auto                          | Override I/O mode. Auto picks `socket` for telnet/raw comm types in DOOR32, else `stdio`.|
| `-config <path>`          | `<cwd>/avatar_chat.ini` if present, else `<bin dir>/avatar_chat.ini` | Door config (chat host/port, theme, splash, etc.). See [CONFIG.md](CONFIG.md). The binary-dir fallback matters for BBSes (notably Mystic) that set CWD to the BBS root before exec. |
| `-data <path>`            | `<bin dir>/data`              | Per-user persistence directory.                                                         |
| `-sysop-avatars <path>`   | (config value)                | Override `sysop_avatars_dir`.                                                           |
| `-charset cp437\|utf8`    | (config value)                | Override `output_charset`.                                                              |
| `-cols <n>`               | auto                          | Force terminal width. 0 = autodetect via CPR probe + drop-file fallback to 80.          |
| `-rows <n>`               | auto                          | Same for height.                                                                        |
| `-select`                 | `false`                       | Always show the avatar selector at launch, even if the user already picked one.         |
| `-user <name>`            | (`$USER` or "guest")          | **Standalone mode only.** Username if no drop file.                                     |
| `-bbs <name>`             | (`$HOSTNAME` or "standalone") | **Standalone mode only.** System name if no drop file.                                  |

---

## Troubleshooting

| Symptom                                                 | Likely cause                                                                                         |
| ------------------------------------------------------- | ---------------------------------------------------------------------------------------------------- |
| "drop file is required" on launch                       | Missing `-dropfile %f` on the command line.                                                          |
| Door exits "load config" with a path that doesn't exist | `-config` resolves to `<cwd>/avatar_chat.ini` first, then `<bin dir>/avatar_chat.ini`. Pass `-config /full/path/avatar_chat.ini` if your BBS launches from a directory neither contains. |
| Stderr says "config file not found at … using built-in defaults"; `bbs_id`/`sysop` etc. ignored | `.ini` lookup couldn't find the file at either default location. Drop `avatar_chat.ini` next to the binary, or pass `-config <path>` explicitly. |
| `socket fd N not usable in this process` / `bad file descriptor` at startup | Your BBS isn't inheriting the user socket fd to the door. Add `-io stdio` to the launch command — the door will use stdin/stdout instead. Common with some Linux Mystic builds. |
| Origin in chat shows `standalone-local` instead of your BBS name | You're in standalone mode (no `-dropfile`) and didn't set `-bbs` or `bbs_id`. Either pass `-bbs "Your BBS Name"` on the command line, or set `bbs_id = yourbbs` in `avatar_chat.ini`. |
| All glyphs are mojibake                                 | Charset mismatch. See [CONFIG.md](CONFIG.md) `output_charset`.                                       |
| Esc key takes ~75ms to dismiss menus                    | The input pump waits to disambiguate Esc-alone from CSI sequences. Working as designed.              |
| Splash garbled / warped                                 | The splash file is CP437 art and your terminal is UTF-8 (or vice versa). The ini's `output_charset` controls this. |
| Mid-session resize doesn't relayout                     | The size probe runs once at startup, not continuously. Reconnect after resizing.                     |
| `/avatar` upload hangs after Esc-in-picker              | Indicates an old pre-2026-05 build; the canonical 5×CAN cancel sequence is now sent. Rebuild.        |

---

## Reading the source

- `cmd/avatar_chat_universal/main.go` — entry point, flag parsing, drop
  file dispatch, standalone-mode synthesis.
- `cmd/avatar_chat_server/main.go` — the self-hostable chat server.
- `internal/dropfile/` — DOOR32.SYS / DOOR.SYS parsers.
- `internal/termio/` — stdio + socket-FD connection types.
- `internal/ansi/` — Frame buffer, compositor, charset translation, ANSI
  art loader, attribute encoding.
- `internal/chat/` — JSON-RPC client + protocol types.
- `internal/chatserver/` — server side of the same protocol.
- `internal/avatar/` — avatar format / validation / store / selector /
  editor / sysop-collection loader.
- `internal/upload/` — Zmodem receiver.
- `internal/idle/` — every idle animation, the registry, and the ANSI
  gallery screensaver.
- `internal/ui/` — App, Header, ActionBar, InputLine, Transcript,
  RosterModal, ImageViewer, splash.
- `internal/theme/` — Theme struct, INI loader, default palette
  ("futurewave").
- `internal/config/` — `avatar_chat.ini` parser.
