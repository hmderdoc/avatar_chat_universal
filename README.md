# avatar_chat_universal

A standalone, cross-platform chat door for BBSes — and also a regular CLI app if
you don't have a BBS handy. Works against the existing `futureland.today:10088`
chat server (so users on this door and the Synchronet JS `avatar_chat` door share
channels) and ships its own self-hostable server for offline / private fleets.

## Why

The original `avatar_chat` is a JavaScript door that only runs inside Synchronet.
This is a from-scratch Go port that runs as a regular native binary, so it
slots into Mystic, ENiGMA½, or any BBS that emits a DOOR32.SYS / DOOR.SYS
drop file — and stays interoperable with the JS door's chat protocol.

Single static binary per platform. No runtime dependencies.

## Features

- 10×6 CP437 avatars: pick from bundled / sysop collections, upload your own
  via Zmodem, or draw one in the in-door **pixel editor** (BETA).
- Per-message **avatar gutter** matching the JS door's bubble layout, with
  relative timestamps ("5m ago", "2h ago") and BBS hostname.
- **Idle screensaver** with 15+ procedural animations plus an **ANSI gallery**
  that scrolls SAUCE-tagged `.ans`/`.bin` art from a sysop-managed directory.
- Sysop **theming** via `themes/<name>.ini` — color palette + screensaver
  profile per theme.
- Optional **splash screen** with an RGB strobe effect on entry.
- **Self-hostable** companion server speaks the same JSON-RPC protocol as the
  public futureland server.
- Optional **IRC and Discord bridge** daemons link external rooms to one Avatar
  Chat channel.
- Runs as a **standalone CLI app** without a drop file (`./avatar_chat_universal -user alice`)
  for testing or shell-only use.

## Quick start

### Pre-built binaries

Grab the tarball matching your platform from the
[GitHub Releases](../../releases) page:

| Filename                                          | Platform                                      |
| ------------------------------------------------- | --------------------------------------------- |
| `avatar_chat_universal_linux_amd64.tar.gz`        | 64-bit Linux (most BBS hosts)                 |
| `avatar_chat_universal_linux_arm64.tar.gz`        | Raspberry Pi 4/5, Graviton                    |
| `avatar_chat_universal_linux_386.tar.gz`          | 32-bit Linux                                  |
| `avatar_chat_universal_windows_amd64.tar.gz`      | 64-bit Windows                                |
| `avatar_chat_universal_windows_386.tar.gz`        | 32-bit Windows                                |
| `avatar_chat_universal_darwin_amd64.tar.gz`       | Intel macOS                                   |
| `avatar_chat_universal_darwin_arm64.tar.gz`       | Apple Silicon macOS                           |

Each tarball contains the door binary, the server binary, bridge binaries, the
default `avatar_chat.ini` / bridge INI files, the `themes/futurewave.ini`, and
the docs.

### Build from source

```sh
# Build the door + server + bridge daemons for your host platform.
make build

# Drop into a Synchronet door slot (see INSTALL.md for the full SCFG fields):
#   Command Line: avatar_chat_universal -dropfile %f
#   I/O Method:   Socket
#   Native:       Yes
#   Drop File:    DOOR32.SYS

# Or run standalone, against the public chat server:
./avatar_chat_universal -user alice

# Or run the bundled chat server alongside for an isolated deployment:
./avatar_chat_server -addr :10088 &
./avatar_chat_universal -user alice            # uses host=futureland by default; edit avatar_chat.ini to point at localhost

# Or run the IRC bridge as a foreground process / supervised daemon:
./avatar_chat_irc_bridge -config irc_bridge.ini

# Or run the Discord bridge:
cp discord_bridge.ini.example discord_bridge.ini
DISCORD_BOT_TOKEN=... ./avatar_chat_discord_bridge -config discord_bridge.ini
```

To run a bridge as a boot-started, auto-restarting daemon under systemd, see
[Running a bridge as a systemd service](INSTALL.md#running-a-bridge-as-a-systemd-service).
A ready-to-install unit ships at
[`avatar-chat-discord-bridge.service`](avatar-chat-discord-bridge.service).

### IRC bridge

`avatar_chat_irc_bridge` connects one IRC channel to one Avatar Chat channel.
IRC has no avatar or ANSI/bitmap image payload support, so avatars are omitted
and `[BITMAP|...]` payloads are controlled by `bitmap_mode` in `irc_bridge.ini`:
`filter`, `announce`, or `dump`. Messages that originated from a bridge carry
an internal origin marker so the same bridge does not echo them back.

### Discord bridge

`avatar_chat_discord_bridge` connects one Discord channel to one Avatar Chat
channel. Avatar payloads and `[BITMAP|...]` images are rendered to PNG
attachments when enabled in `discord_bridge.ini`; Discord attachments are
bridged back to Avatar Chat as URLs. The bot needs access to the configured
channel and Discord's Message Content intent enabled for Discord-to-Avatar Chat
messages. Keep the runtime `discord_bridge.ini` local; the tracked
`discord_bridge.ini.example` contains the setup and bot invite steps.

## Documentation

- [INSTALL.md](INSTALL.md) — sysop install guide for Synchronet, Mystic, ENiGMA½,
  generic BBS softwares, and standalone use. Cross-platform build instructions.
- [CONFIG.md](CONFIG.md) — every `avatar_chat.ini` key with semantics and
  defaults.
- [THEMING.md](THEMING.md) — how to ship a custom color palette + screensaver
  profile via `themes/<name>.ini`.
- [BRIDGES.md](BRIDGES.md) — bridge architecture, origin rules, and media
  mapping for Discord/Telegram/Matrix/Slack-style adapters.
- [AVATARS.md](AVATARS.md) — avatar format, validation rules, sysop-managed
  collection directory, the in-door selector / upload / editor flows.
- [SCREENSAVER.md](SCREENSAVER.md) — idle animations, ANSI gallery setup,
  idle-interleave behavior.

## Status

This door is in active development. The core chat experience is solid; some
features (avatar editor, image sending) are explicitly marked BETA in-app.
File issues / PRs welcome.

## License

MIT — see [LICENSE](LICENSE). Bundle it, ship it, fork it; just keep the
copyright notice in derivative works.
