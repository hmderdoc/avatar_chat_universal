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
[INSTALL.md](INSTALL.md#self-hosting-the-chat-server). The same server can
put a web chat page in front of your chat or someone else's: see
[WEB.md](WEB.md).

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
| `resize_probe_seconds` | `5` | Re-check the size this often and re-lay out the screen when it changes (typed text and scroll position are kept; open lists close). 0 = check at startup only. |

The size is probed at startup, after the splash clears, and then every
`resize_probe_seconds` while the chat screen is up, between frames, with
the cursor hidden for the trip. Probing stays off when `screen_cols` /
`screen_rows` or `-cols` / `-rows` pin the size, or when the terminal
didn't answer the startup probe. Sizes under 40x12 are ignored.

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
| `shared_ignore_file`  | (empty) | Synchronet only. A shell's per-user prefs JSON (`users["user-N"].notifications.ignored`, the futureland shell's `shell_prefs.json`) whose ignore entries the door also honours, read-only: entries for everywhere or for the shell's `local` chat apply, MRC/IRC ones don't, and a notifications-only entry still shows the line but never rings. Re-read when the file changes. Use an absolute path. |

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
| `user_themes` | `on`     | Users may pick another `themes/<name>.ini` with `/theme` or in `/settings`; the pick is saved per user under `<data>/prefs/` and `theme` stays the default. `off` pins everyone to `theme`. |

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
| `/msg <user> <text>` / `/m`   | Send a private message. To a standalone web user (the bundled server's page), it goes to their own web mailbox, so it never lands with a BBS user of the same name. |
| `/r <text>` / `/reply`        | Reply to the last private sender, to their web mailbox when they have one.    |
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
| `/settings` / `/prefs` / `/options` | Settings panel: avatar (pick, draw, upload, disable) and handle style (a colour per letter, presets). Enter (or a click on a letter) opens a colour grid for the letter: the 16 CGA colours, plus a larger palette on truecolor terminals; `f`/`F` still step through the 16. On Synchronet these are the user's BBS-wide records. |
| `/ignore [name]` / `/unignore <name>` | `/ignore` lists who you ignore; `/ignore <name>` hides their lines, art, private messages and join/leave notices, live and in history, and they never ring; `/unignore` undoes it. Kept per user in the door's prefs (`<data>/prefs/`). Names compare without case, and a bridge-relayed `<name> text` line counts. With `shared_ignore_file` set, people ignored in the shell are hidden too (change those there). |
| `/theme [name\|default]`      | List the colour themes, switch (remembered per user), or go back to the sysop's. See THEMING.md. |
| `/mouse [on\|off]`            | Turn mouse reporting on or off for yourself (remembered). See "Mouse". |
| `/draw [WxH]` / `/art`        | ANSI art editor (default 80x25, up to 80x40). Ctrl-Y posts the art as a `[BITMAP]` message every client draws; see "Art editor". |

---

## Art editor

`/draw` opens a full-screen CP437 canvas in the 16 CGA colours (16
foregrounds, 8 backgrounds, no iCE), the same palette the web's Moebius
editor and the shell's resource editor use. A canvas larger than the
screen scrolls with the cursor. Ctrl-Y posts the drawn part (blank rows and
columns to the right and below are trimmed) as a normal `[BITMAP|...]`
message, so the website, the shell, Moebius and the bridges all show it.
Esc leaves; it asks first if you drew something you have not sent.

| Key | Does |
| --- | --- |
| letters, digits, punctuation | Type at the cursor and move right; Enter goes to the next line. |
| F1-F10 | Stamp a glyph from the current F-key set (PabloDraw's sets, as in Moebius; set 6, shades and blocks, is the default). |
| Ctrl-N / Ctrl-P | Next / previous F-key set (16 sets). |
| Ctrl-F / Ctrl-G | Next foreground (16) / background (8) colour. Tab swaps them. |
| Ctrl-E | Pick the colours (and glyph) under the cursor. |
| Ctrl-O | Half-block mode: the cursor moves by half rows and Space paints the half under it, two pixels per cell. |
| Ctrl-B | Brush: moving the cursor paints (the last glyph, or a pixel in half-block mode). |
| Ctrl-K | Flood fill the area of identical cells under the cursor. |
| Backspace / Del | Erase left / under the cursor. |
| Ctrl-Z or Ctrl-U | Undo (64 steps). Ctrl-X clears the canvas (undoable). |
| Ctrl-Y | Send. Esc: leave. Ctrl-L: redraw. |

A full 80x40 canvas of noise still fits the web chat's 32000-character
limit for art; the editor refuses to send anything larger and says so.

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

## Sound (SyncTERM audio)

The TV lounge plays its channel's sound on terminals that support SyncTERM's
Audio APCs: clips are sent as WAV, cached by the terminal and decoded there
with libsndfile, so the door needs no audio library on any host.

| Key | Default | Meaning |
| --- | --- | --- |
| `tv_audio` | `auto` | `auto`: ask the terminal at startup (only a terminal that identified as CTerm is asked, so nothing else is sent an APC); `on`: skip the question; `off`: never. |
| `audio_links` | `auto` | Same values, for audio links in chat (`/play`). |
| `audio_max_mb` | `20` | Largest audio file `/play` downloads. |

**Audio links.** On a terminal that plays audio, each audio link in chat
gets a one-line card (`♫ song.mp3  /play`). Nothing plays until the caller
asks: `/play` plays the newest audio link, `/play N` the Nth newest,
`/play list` shows the last nine, `/stop` stops. The file is downloaded
with the same guards as pictures (public addresses only, size cap), decoded
in the door (mp3, Ogg Vorbis, FLAC, PCM WAV), mixed to mono at about 22 kHz
and streamed in real time on its own channel, so it can play alongside the
TV. Opus, AAC and m4a links are labelled as unplayable: there is no pure-Go
decoder for them. Downloads are cached in `<data>/audiocache`.

`/tvmute` (or `/mute`) silences and restores the lounge's sound. Leaving
the channel, `/tvoff` and quitting all stop playback at once. The probe's
verdict is logged to `<data>/termprobe.log`.

The door runs the audio queue open-loop, like the futureland shell: it
keeps a local ledger instead of asking the terminal for queue reports,
which would arrive in the key stream. Gaps are covered by a short silence
clip, and a queue that runs ahead is flushed back to the present, so sound
can skip briefly but never drifts far behind the picture. There is no
picture-to-sound sync in the door; video is shown as it arrives.

## Video links

With **ffmpeg and ffprobe** installed on the host, video links play on the
TV-lounge screen: the same renderer (24-bit half blocks on truecolor
terminals, 16-colour shade cells everywhere else) and, on SyncTERM, the
same sound path. With **yt-dlp** also installed, links to the sites in
`video_sites` (YouTube, Vimeo by default) play too. Without ffmpeg the
feature is simply absent. The door logs what it found to stderr (the BBS
log) at startup.

| Key | Default | Meaning |
| --- | --- | --- |
| `video` | `auto` | `auto`: on when ffmpeg and ffprobe are found; `off`: never. |
| `video_ffmpeg`, `video_ffprobe` | PATH lookup | Paths to the tools. |
| `video_ytdlp` | PATH lookup | Path to yt-dlp; `off` turns page links off. |
| `video_sites` | `youtube.com,youtu.be,vimeo.com` | Hosts (and their subdomains) whose page links go to yt-dlp. |
| `video_extractors` | `youtube,vimeo` | The only yt-dlp extractors it may use. |
| `video_max_mb` | `100` | Largest download. |
| `video_max_seconds` | `600` | Longest playback; ffmpeg stops there. |
| `video_max_concurrent` | `2` | Playbacks at once across every door process on the host. |
| `video_download_seconds` | `180` | Longest download. |

**Which links.** A link naming `.mp4 .m4v .webm .mov .mkv`, a page whose
preview card announces a direct `og:video`, or a page on an allowlisted
site. Each gets a card line (`► clip.mp4  /play`). Nothing plays until the
caller types `/play` (`/play N`, `/play list` share the audio list). The
video takes over the screen, a tuned TV channel steps aside and comes back
after; `Esc` or `/stop` ends it, `/tvmute` and `/tvcolor` work as in the
lounge. NSFW-looking links stay veiled until `/nsfw`. HLS and DASH links
(`.m3u8`, `.mpd`) are not played: ffmpeg would fetch their segments itself,
outside the door's guards.

**What the door enforces.** A chat line is written by whoever is on the
other end, so no URL from one reaches ffmpeg or yt-dlp directly:

- direct links and `og:video` are downloaded by the door's own fetcher with
  the picture guards (http/https on ports 80/443, public addresses only,
  every redirect re-checked, size cap) into a private `0700` directory
  under `<data>/videocache`;
- yt-dlp runs only for allowlisted hosts, limited to `video_extractors`,
  with `--ignore-config --no-plugin-dirs --no-cookies --no-exec
  --no-playlist --no-js-runtimes`, a size cap and a time limit, writing
  into the same kind of directory;
- ffprobe and ffmpeg read only that local file: protocol whitelist `file`,
  demuxers `mov` and `matroska` only (a "video" that is really an HLS or
  concat playlist naming other files is refused), the probed demuxer
  pinned, no stdin;
- every child runs with a minimal environment in its own process group at a
  lower priority, is killed with its group on stop, exit or hangup (Linux
  also kills it if the door dies), and is bounded by `video_max_seconds`;
- the download is deleted when playback ends; leftovers from a killed door
  are purged at the next start.

yt-dlp itself follows the site's own links to its media servers; the door
cannot put its address guard on those, only on which sites may be named.
YouTube and Vimeo refuse many datacenter addresses without a logged-in
account (cookies), which the door never passes, so page links may fail on
a hosted BBS while direct links work.

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

## Mouse

| Key     | Default | Notes |
| ------- | ------- | ----- |
| `mouse` | `on`    | Ask the terminal for mouse reports (xterm 1000 with SGR 1006; the older X10 form is read too). Each user can turn it off with `/mouse off`, saved under `<data>/prefs/`. `off` here disables it for everyone. |

With it on:

- The wheel scrolls the chat (3 rows a notch), moves the selection in
  `/who` and `/channels`, and pages through `/img`.
- Clicking a top or bottom pill runs it; `/join`, `/part`, `/me`, `/msg`
  and `/r` start the command in the input line instead.
- Clicking a speaker's name starts `/msg <name> `.
- Clicking an inline picture opens it in `/img`; clicking a `► … /play`
  line plays it; clicking a link card prints its full URL.
- In `/channels`, a click selects a row and a second click joins it.
- The `[X]` at the top left of every panel closes it as Esc does; the one
  at the left of the chat screen's first row is the `Esc exit` pill.
- In `/settings`, clicking a letter opens the colour grid for it; in the
  grid, clicking a swatch uses it.

Terminals that can't report the mouse ignore the request. While reports
are on, most terminals want Shift held to select text for copying.
Reporting is switched off while the art editor, the avatar picker or
editor, or a Zmodem transfer runs, and when the door exits.

---

## Synchronet web page (`[web]`)

The Synchronet web page in [web/synchronet](web/synchronet/README.md) reads
this same file, so the page and the door use the same chat server and rooms.
It reads the door's `host`, `port`, `default_channel`, `motd_channel`,
`max_history`, `images`, `link_previews`, `link_preview_ttl_hours` and
`nsfw_keywords` keys exactly as the door does. The keys below only matter
to the web page. Keep them in a `[web]` section at the **end** of the
file: Synchronet's INI reader files every key after a section header under
that section, so door keys placed after `[web]` would be invisible to the
web page. The door reads the file flat and ignores these keys, which is why
each one starts with `web_`.

| Key | Default | Notes |
| --- | ------- | ----- |
| `web_uploads` | `off` | Let logged-in users drop or attach pictures, sound and video on the web page. Files are stored on this BBS's own web server under `<web_root>/chatmedia/<date>/`. |
| `web_upload_types` | `png, jpg, gif, mp3, wav, mp4` | Which types are taken, judged by the file's bytes, never its name. Also known: `webp`, `ogg`, `flac`, `m4a`, `webm`, which need a line in `ctrl/mime_types.ini` first (the web page's README lists them). |
| `web_upload_max_mb` | `20` | Largest picture or sound file. |
| `web_upload_video_max_mb` | `50` | Largest video. Video is stored as uploaded, not re-encoded. |
| `web_upload_retention_days` | `7` | Day folders older than this are removed when someone uploads. `0` keeps everything. |
| `web_uploads_per_hour` | `30` | Uploads one user may finish per hour. `0` means no limit. |
| `web_upload_dir` | `<web_root>/chatmedia` | Where files go. If you move it, also set `web_media_base_url`. |
| `web_media_base_url` | from the request | Public address of that folder, e.g. `https://bbs.example.org/chatmedia`, for a reverse proxy that rewrites `Host`. Without it, the link is built from the browser's `Host` header, port included (a site on `:4080` links `:4080`), with `https` when the proxy sends `X-Forwarded-Proto: https`. `web_upload_url_base` is the older name and still works. |
| `web_motd_host_qwkid` | empty | QWK ID of the BBS whose sysop may post to the MOTD room. The old JS bundle's `motd_host_qwkid` at the top of the file still counts. |
| `web_styles` | all | Which page styles users may pick, by id, in the order shown: `classic`, `cga`, `modern-dark`, `modern-light`, `amber`, `green`, `win95`, `compact`. Unknown ids are skipped; a list with none left offers them all. A user whose saved style is no longer listed gets the default. |
| `web_default_style` | `classic` | The style guests and new users see. If it isn't in `web_styles`, the first listed style is used. |
| `web_motd_host_system` | empty | The same by BBS name, when no QWK ID is set. With neither set, the sysop may post when `host` is `127.0.0.1`/`localhost` or equals the BBS name. |

The web page finds this file at `<exec>/../xtrn/avatar_chat_universal/avatar_chat.ini`.
To keep it elsewhere, set `avatar_chat_ini = /path/to/avatar_chat.ini` in
the `[web]` section of `ctrl/modopts.ini`.

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
