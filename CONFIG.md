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

With the door's own store, per-user avatar INIs are stored under
`<data-dir>/users/<bbs_id>/<username-lowercased>.ini`. Set `bbs_id` to a
stable slug so avatar files don't move when the BBS is renamed.

---

## Avatar storage

| Key                   | Default | Notes |
| --------------------- | ------- | ----- |
| `avatar_store`        | `auto`  | `auto`: on Synchronet (DOOR32.SYS from Synchronet with a user number, data dir found) use the caller's real Synchronet avatar and handle style; anywhere else use the door's own store. Falls back to the door's store with a one-line log message if detection fails. `synchronet`: always use Synchronet's records; failing to find them is a startup error. `local`: always use the door's own store. |
| `synchronet_data_dir` | (empty) | Synchronet's data dir. Empty means detect it from `$SBBSCTRL/main.ini` `[dir] data`, then `$SBBSCTRL/../data/`. The `-sbbs-data` flag overrides this. |

In Synchronet mode the avatar is the `[avatar]` section of
`<data>/user/NNNN.ini` and the handle style is
`<data>/chat/nickstyle/NNNN.json`, both shared with the rest of the BBS. See
[AVATARS.md](AVATARS.md#storage).

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

| Key                       | Default | Notes                                                                              |
| ------------------------- | ------- | ---------------------------------------------------------------------------------- |
| `splash_timeout_seconds`  | `5`     | Auto-dismiss after N seconds (any keypress also dismisses). Set to `0` to skip.    |

The bundled splash artwork is compiled into the binary. It's parsed (not
byte-dumped) into a Frame, centered, and strobed through R/G/B every 80ms
while displayed. Charset auto-converts based on `output_charset`.

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
| `/channels` / `/list`         | Room list: your joined channels with unread counts, then every public room the server registers, with user counts. Enter joins or switches, `L` leaves. |
| `/join <channel>` / `/j`      | Switch to another channel. The one you leave stays joined: its new messages count as unread (top-bar `/channels [N]`). |
| `/part` / `/p`                | Leave the current channel and go back to the one you were in before; parting the last one exits the door. |
| `/clear`                      | Clear the local transcript view.                                              |
| `/img`                        | Full-screen viewer: BITMAP art first, then pictures from image links (zoomed to the screen). |
| `/nsfw`                       | Show (or veil again) pictures the NSFW keyword rule hid, for this session.    |
| `/avatar`                     | Open the avatar manager (selector with Upload/Disable/Editor pills).          |
| `/avatar set <base64>`        | Power-user: set your avatar from a base64 120-byte payload.                   |
| `/avatar off`                 | Disable your avatar without losing it (re-enable by picking again).           |
| `/avatar pick`                | Open the selector directly.                                                   |
| `/settings` / `/prefs` / `/options` | Settings panel: avatar (pick, draw, upload, disable) and handle style (a colour per letter, presets, a tag). On Synchronet these are the user's BBS-wide records. |

---

## Inline images

Image links in chat (`.png`, `.jpg`/`.jpeg`, `.gif` first frame, `.webp`,
`.bmp`, recognised by the extension, up to 3 per message) show as pictures
inside the bubble, at most 10 rows tall and as wide as the bubble. The
renderer follows the terminal, using the same probe as `tv_color = auto`:

1. **Sixel** when the terminal says it draws it: SyncTERM 1.207+ reporting
   pixel operations, or an xterm-family DA1 listing sixel. The encoder is
   built in; no `img2sixel` or ImageMagick needed.
2. **24-bit half blocks** when the terminal is truecolor.
3. **16 colours** otherwise (NetRunner, the Linux console, anything that
   answers nothing): each cell is the shade glyph (`░ ▒ ▓`), half block or
   space with the 16-colour pair nearest its two pixels (shadecell, the
   same matcher as the shell and the TV lounge).

A link shows `[loading image...]` while it downloads. Downloads run in the
background with these guards: http/https only, ports 80 and 443, every
address the host resolves to must be public (no loopback, private,
link-local, CGNAT or similar ranges), re-checked on each of at most 3
redirects, a size cap and a 10 s timeout. No environment proxy is used.

Pictures whose link or line contains an NSFW keyword (whole word; the
shell's list by default) are not downloaded; a shaded veil shows instead
until the user types `/nsfw`.

| Key               | Default             | Meaning |
| ----------------- | ------------------- | ------- |
| `images`          | `true`              | Turn inline pictures on or off. |
| `image_sixel`     | `auto`              | `auto` asks the terminal; `on` / `off` force it. |
| `image_max_mb`    | `5`                 | Largest download, in MB. |
| `image_cache_dir` | `<data>/imagecache` | Where downloads are kept between sessions. |
| `image_cache_mb`  | `50`                | Cache size cap, in MB; oldest-used files go first. |
| `nsfw_keywords`   | the shell's list    | Comma-separated words that veil a picture; `none` turns the veil off. |

---

## Link previews

The first plain link in a message (one that is not an image, video, audio
or ANSI file, or a chat attachment) gets a preview card under the text, as
on the futureland website: a bar down the left edge, a small thumbnail
(12 by 4 cells at most) from the page's `og:image`, then the site name, the
title (2 lines at most) and the description (2 lines at most). The card
reads OpenGraph tags first, then twitter-card tags, then `<title>` and
`<meta name="description">`. Text is shown as plain ASCII: entities are
decoded and accented letters and typographic punctuation become their ASCII
look-alikes. YouTube and Vimeo links get a card too; the door has no video
player for them.

A link shows `[loading preview...]` while it is fetched; a page that cannot
be fetched, is not HTML or says nothing about itself gets no card. The rules
are the website's (`lib/link-preview.js`): the same guards as inline images
(http/https, ports 80 and 443, public addresses only, re-checked on each of
at most 3 redirects), the first 256 KB requested, the first 192 KB read, an
8 s limit. The thumbnail is drawn like any inline picture (24-bit or
16-colour shade cells; never sixel) and kept in the picture cache.

Cards are cached in `linkpreviews/` beside the picture cache, so a link
posted again is not refetched: for `link_preview_ttl_hours` when it worked,
one hour when it did not. Setting `image_cache_mb = 0` turns the card cache
off too.

A card whose link, line, title, description or site name contains an NSFW
keyword shows only the site name and "NSFW link - /nsfw to show"; `/nsfw`
reveals cards and pictures together.

| Key                      | Default | Meaning |
| ------------------------ | ------- | ------- |
| `link_previews`          | `true`  | Turn preview cards on or off. |
| `link_preview_ttl_hours` | `24`    | How long a fetched card is reused. |

Card colours are the theme keys `card_site`, `card_title` and `card_edge`
(see THEMING.md).

---

## Chat attachments

`/upload` receives one file by Zmodem (a picture: png, jpeg, gif, webp,
bmp; or ANSI art: `.ans`, `.bin`), checks its type by its bytes, posts it to
your media server and puts the returned link in the caller's input line.
Nothing is sent until they press Enter. The media server is the bundled
`avatar_chat_server` with `-media-addr`; see MEDIA.md for running it.

| Key             | Default | Meaning |
| --------------- | ------- | ------- |
| `upload_url`    | (unset) | Your media server's upload address, e.g. `https://bbs.example.org:10089/upload`. Unset turns `/upload` off. |
| `upload_max_mb` | `20`    | Largest file the door will receive. The server applies its own limits too. |

---

## Attention cues

- **Mentions.** A line from someone else that says your name as a whole
  word (an optional `@`, any case; the same rule as the website) is drawn in
  the theme's `bubble_mention` colours with a `mention_edge` bar down its
  left side.
- **PMs.** Each PM received since you last sent a line counts on a flashing
  `/r [N]` pill in the top bar.
- **Other channels.** Messages on joined channels you aren't viewing count on
  a flashing `/channels [N]` pill. A mention there rings the bell too.
- **Bell.** A PM or a mention sends the terminal bell, at most once every 3
  seconds.

| Key    | Default | Meaning                                       |
| ------ | ------- | --------------------------------------------- |
| `bell` | `true`  | Ring BEL on an incoming PM or mention of you. |

On a narrow bar the badges push out `/help` and `Tab user` first, never
`Esc exit`.

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
│       └── <username>.ini                      -- per-user [avatar] and [style]
├── imagecache/                                 -- inline pictures and card thumbnails
├── linkpreviews/                               -- link-preview cards (JSON)
└── termprobe.log                               -- one line per session: what the terminal answered
```

In Synchronet mode (`avatar_store`) the door writes nothing here; it uses
`<sbbs data>/user/NNNN.ini` and `<sbbs data>/chat/nickstyle/NNNN.json`.

`<bbs_id>` is the resolved BBS slug — config `bbs_id` if set, else
drop-file BBSID, else `slug(SystemName-Sysop)`.

`<username>` is lowercased. If your users include unusual characters,
they're passed through verbatim — the file system has to handle them.
