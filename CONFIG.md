# avatar_chat_universal — Configuration Reference

This is the canonical reference for `avatar_chat.ini`. The shipped
`avatar_chat.ini` has the same keys with inline comments; this document
groups them by feature area and adds context the inline comments don't
have room for.

Schema: classic INI-style `key = value` lines. No section headers
required (we ignore them if present). Lines beginning with `;` or `#` are
comments. Whitespace around `=` is tolerated. Most keys are optional —
defaults shown below.

---

## Chat server

| Key                    | Default            | Notes                                                                  |
| ---------------------- | ------------------ | ---------------------------------------------------------------------- |
| `host`                 | `futureland.today` | Chat server hostname or IP.                                            |
| `port`                 | `10088`            | Chat server TCP port.                                                  |
| `default_channel`      | `main`             | Channel the user joins on launch.                                      |
| `max_history`          | `200`              | Recent messages to slice from server's history at join.                |
| `poll_delay_ms`        | `25`               | Main-loop tick interval. Lower = more responsive + more CPU.           |
| `reconnect_delay_ms`   | `3000`             | First-attempt reconnect delay; doubles up to 30s on repeated failure.  |
| `input_max_length`     | `500`              | Cap on a single chat message length.                                   |

To self-host, run `avatar_chat_server -addr :10088` and point `host`/`port`
at it. The protocol is the same as `futureland.today:10088`, so the JS
`avatar_chat` door interoperates against either. See
[INSTALL.md](INSTALL.md#self-hosting-the-chat-server).

---

## BBS identity

| Key       | Default                                       | Notes                                                                                        |
| --------- | --------------------------------------------- | -------------------------------------------------------------------------------------------- |
| `bbs_id`  | drop-file BBSID, or `slug(SystemName-Sysop)`  | Stable short slug used to namespace per-user avatar persistence. Set explicitly to keep avatars across BBS renames. |
| `sysop`   | drop-file Sysop                               | Sysop name; used in avatar BBS-id fallback computation.                                      |

---

## Output charset

| Key              | Default | Notes                                                                                       |
| ---------------- | ------- | ------------------------------------------------------------------------------------------- |
| `output_charset` | `cp437` | `cp437` for SyncTERM / NetRunner / mtelnet / fTelnet / classic BBS clients (raw 8-bit). `utf8` for xterm / Terminal.app / iTerm / kitty / Windows Terminal — translates every cell byte through CP437→Unicode and emits UTF-8. |

If your client renders raw `0xDB` as a `?` or replacement character,
you're in UTF-8 mode and should set `output_charset = utf8`. If your
client is a real BBS terminal and `output_charset = utf8` makes glyphs
look like multi-character mojibake, set it back to `cp437`.

The `-charset` command-line flag overrides this per-launch.

---

## Screen size

| Key            | Default | Notes                                                                          |
| -------------- | ------- | ------------------------------------------------------------------------------ |
| `screen_cols`  | `0`     | 0 = auto-detect via terminal CPR probe; falls back to drop-file value or 80.   |
| `screen_rows`  | `0`     | Same for height; falls back to drop-file value or 24.                          |

The probe runs once at startup, after the splash clears. Mid-session
resizes don't trigger a relayout — reconnect if your terminal size
changes.

---

## BBS identity (avatar persistence)

Per-user avatar INIs are stored under
`<data-dir>/users/<bbs_id>/<username-lowercased>.ini`. Set `bbs_id` to a
stable slug so avatar files don't move when the BBS is renamed.

---

## Sysop-curated avatar collections

| Key                  | Default | Notes                                                                  |
| -------------------- | ------- | ---------------------------------------------------------------------- |
| `sysop_avatars_dir`  | empty   | Directory of `.bin` collection files. Each file is one collection in the avatar selector. See [AVATARS.md](AVATARS.md). |

---

## MOTD

| Key             | Default | Notes                                                                                        |
| --------------- | ------- | -------------------------------------------------------------------------------------------- |
| `motd_channel`  | `motd`  | Chat channel the door subscribes to for the rotating MOTD shown in the header bar.           |

Sysops can `WRITE` messages into `channels.<motd_channel>.messages` to
push announcements that show up in every door instance.

---

## Idle animations / screensaver

| Key                       | Default | Notes                                                                                         |
| ------------------------- | ------- | --------------------------------------------------------------------------------------------- |
| `idle_enabled`            | `true`  | Master switch.                                                                                |
| `idle_timeout_seconds`    | `180`   | How long without a keypress before the screensaver kicks in.                                  |
| `idle_switch_interval`    | `60`    | How often a procedural animation rotates to the next one.                                     |
| `idle_fps`                | `8`     | Default frame rate for animations that don't request their own. Sprite-based ones (avatars_float, comet_trails) request higher. |
| `idle_random`             | `true`  | `true` picks the next animation randomly; `false` follows `idle_sequence`.                    |
| `idle_sequence`           | empty   | Comma-separated explicit playback order. If empty, the full registry is used.                 |
| `idle_disable`            | empty   | Comma-separated names to skip entirely.                                                       |
| `idle_interleave_ansi`    | `true`  | Insert one piece of ANSI gallery art between every procedural animation. See [SCREENSAVER.md](SCREENSAVER.md). |
| `ansi_gallery_dir`        | empty   | Directory (recursively scanned) of `.ans`/`.bin` files for the `ansi_gallery` animation.      |

Available animation names — see [SCREENSAVER.md](SCREENSAVER.md) for the
full catalog. Quick list:

- BG: `tv_static`, `matrix_rain`, `life`, `starfield`, `fireflies`,
  `sine_wave`, `comet_trails`, `plasma`, `fireworks`, `aurora`,
  `fire_smoke`, `ocean_ripple`, `lissajous`, `lightning`, `tunnel`,
  `ansi_gallery`.
- FG: `avatars_float`, `figlet_message`.

---

## Figlet message animation

| Key                       | Default          | Notes                                                                                |
| ------------------------- | ---------------- | ------------------------------------------------------------------------------------ |
| `idle_figlet_messages`    | `Avatar Chat`    | Pipe-separated list. The animation picks one at random each tick cycle.               |
| `idle_figlet_colors`      | `true`           | Cycle through colors per character.                                                   |
| `idle_figlet_move`        | `true`           | Bounce the message around the screen vs. static centered.                            |

---

## Theme

| Key      | Default       | Notes                                                                                  |
| -------- | ------------- | -------------------------------------------------------------------------------------- |
| `theme`  | `futurewave`  | Theme name resolved to `themes/<name>.ini`. Anything the theme doesn't override falls through to the built-in palette. See [THEMING.md](THEMING.md). |

---

## Splash screen

| Key                       | Default       | Notes                                                                                |
| ------------------------- | ------------- | ------------------------------------------------------------------------------------ |
| `splash_path`             | `splash.ans`  | Path to a SAUCE-tagged `.ans` or `.bin` file shown on entry. Empty = no splash.       |
| `splash_timeout_seconds`  | `5`           | Auto-dismiss after N seconds (any keypress also dismisses).                           |

The splash is parsed (not byte-dumped) into a Frame, centered, and
strobed through R/G/B every 80ms while displayed. Charset auto-converts
based on `output_charset`.

---

## Chat slash commands

These aren't config keys, but are the runtime user-facing commands. See
also the on-screen action bars and `/help`.

| Command                       | What it does                                                                  |
| ----------------------------- | ----------------------------------------------------------------------------- |
| `/help` / `/?`                | Show available commands.                                                      |
| `/quit` / `/q` / `/exit` / `/bye` | Disconnect and exit the door.                                              |
| `/me <action>`                | Broadcast an action message ("alice waves").                                  |
| `/msg <user> <text>` / `/m`   | Send a private message.                                                       |
| `/r <text>` / `/reply`        | Reply to the last private sender.                                             |
| `/who`                        | Open the roster modal listing everyone in the channel.                        |
| `/channels`                   | List active channels.                                                         |
| `/join <channel>` / `/j`      | Switch to another channel.                                                    |
| `/part` / `/p`                | Leave the current channel and rejoin the default.                             |
| `/clear`                      | Clear the local transcript view.                                              |
| `/img`                        | Open the image (BITMAP) viewer modal for any inline images received.          |
| `/avatar`                     | Open the avatar manager (selector with Upload/Disable/Editor pills).          |
| `/avatar set <base64>`        | Power-user: set your avatar from a base64 120-byte payload.                   |
| `/avatar off`                 | Disable your avatar without losing it (re-enable by picking again).           |
| `/avatar pick`                | Open the selector directly.                                                   |

---

## Hotkeys

| Key             | What it does                                                                          |
| --------------- | ------------------------------------------------------------------------------------- |
| `Tab`           | Tab-complete the username at the cursor (cycles through matches on repeated presses). |
| `Esc`           | Exit the door.                                                                        |
| `PgUp` / `PgDn` | Scroll the transcript history.                                                        |
| Arrow Up / Down | Scroll through your message history while editing the input line.                     |

In modals (selector, image viewer, roster, editor): arrow keys navigate;
`Enter` confirms; `Esc` cancels. The avatar selector additionally responds
to `U` (Upload), `D` (Disable), `E` (Editor) — these are also visible as
pills along the bottom of the selector.

---

## Where the data goes

```
<data-dir>/                                     -- override with -data
├── users/
│   └── <bbs_id>/
│       └── <username>.ini                      -- per-user avatar
└── (currently nothing else; future: history caches, prefs)
```

`<bbs_id>` is the resolved BBS slug — config `bbs_id` if set, else
drop-file BBSID, else `slug(SystemName-Sysop)`.

`<username>` is lowercased. If your users include unusual characters,
they're passed through verbatim — the file system has to handle them.
