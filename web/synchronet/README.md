# Avatar Chat for a Synchronet web site

A chat page for Synchronet's stock web interface (webv4). Members chat in
the same rooms as the Avatar Chat door, the terminal shell and the
bridges, under their BBS login, with their real BBS avatar and handle
style. It replaces the web bundle that shipped with the old JS door
(`xtrn/avatar_chat`).

The browser only ever talks to your BBS. The page calls SSJS APIs on your
web server, and those scripts talk to the chat service from the BBS side.
That makes this the more secure of Avatar Chat's two web apps: every post
is tied to a logged-in account, and nothing outside your BBS can write to
the chat in a member's name. The other one is the standalone page in
`web/standalone`, for sysops without Synchronet; it connects straight to a
chat server and has no logins.

## What it does

- Public rooms: switch rooms, create one, see who is in a room and how
  many are in each, with unread counts. Everyone sees the MOTD ticker;
  only the host BBS's sysop can post to it.
- Private messages between members, including members of other BBSes on
  the same chat server, with a thread list and unread counts.
- Avatars: the sender's avatar as it travels on the message. Otherwise
  the page looks it up through stock webv4: by account number for your own
  users, and by `alias@QWKID` for remote ones.
- BITMAP art, the `[BITMAP|...]` messages the door's art editor and the
  shell send. You can also drop or choose an ANSI file in the browser, and
  it is sent as BITMAP art.
- Handle styles: colours for each letter plus a tag, shown for everyone.
  The Handle Style button edits your own. It saves to the same
  `data/chat/nickstyle/NNNN.json` record the door, the shell and
  Futureland's site use, so a change shows everywhere.
- Inline pictures (up to three per message), audio and video players for
  media links, and a link card for the first other link. Pictures whose
  link or message matches `nsfw_keywords` are blurred until clicked.
- Messages that say your name are highlighted. A whole-word match counts,
  with or without `@`, in any case.
- Messages are capped at 1,000 characters, which is what the chat server
  keeps. BITMAP art may run to 32,000.
- Live updates arrive through webv4's own event stream. A periodic poll
  covers anything that stream misses.
- Uploads (off by default): logged-in users can drop, paste or attach a
  picture, sound or video. The file is stored on this BBS's web server
  and its link is put in the composer.

Guests can read public rooms. They can't post, open private messages,
upload or ask for link cards, and they get no live stream. This matches
the old bundle.

## Install

From a checkout of this repository, build the bundle (or use the copy
already built in `dist/`):

```sh
cd xtrn/avatar_chat_universal/web/synchronet
npm install        # first time only: TypeScript and esbuild
npm run build
```

Copy `dist/webv4/` over your web directory, the one your
`ctrl/modopts.ini` `[web]` section calls `web_directory` (stock:
`../webv4`):

```sh
cp -r dist/webv4/. /sbbs/webv4/
```

That adds these files:

| File | What it is |
| --- | --- |
| `pages/avatarchat.xjs` | The page. |
| `root/avatarchat/app.js`, `app.css` | The browser code and styles. |
| `root/api/avatarchat.ssjs` | The chat API. |
| `root/api/avatarchat-upload.ssjs` | Uploads. It refuses everything unless `web_uploads = on`. |
| `lib/events/avatarchat.js` | Live updates, loaded by `root/api/events.ssjs`. |
| `lib/avatarchat-page.js` | What the page tells the browser. |

Then browse to `./?page=avatarchat.xjs`, or open a particular room with
`./?page=avatarchat.xjs&channel=lobby`. To put the page in the site menu,
copy it to a numbered name instead, such as `pages/050-avatarchat.xjs`.

Nothing needs a restart: the web server loads SSJS and XJS fresh on each
request.

### What it relies on from stock webv4 and exec/

The page relies on these stock files. All of them ship with Synchronet
and were checked against `/sbbs/repo/webv4` and `exec/load`:

- `lib/init.js`, `lib/auth.js` and `lib/request.js`: login and session.
- `root/api/events.ssjs`: the event stream that loads `lib/events/avatarchat.js`.
- `root/api/system.ssjs?call=get-avatar`: avatar lookups.
- `root/js/graphics-converter.js`, `root/js/avatars.js` and
  `root/images/cp437-ibm-vga8.png`: drawing avatars and BITMAP art.
- `exec/load/json-client.js`, `http.js` and `avatar_lib.js`, plus `modopts.js`.

It needs nothing from `webv4_custom` or `mods/`.

## Settings

The page reads the door's own `avatar_chat.ini`, so the two never
disagree about the chat server or the rooms. By default that is
`<exec>/../xtrn/avatar_chat_universal/avatar_chat.ini`. To use another
path, set it in `ctrl/modopts.ini`:

```ini
[web]
avatar_chat_ini = /sbbs/xtrn/avatar_chat_universal/avatar_chat.ini
```

These keys are shared with the door and read exactly as the door reads
them: `host`, `port`, `default_channel`, `motd_channel`, `max_history`,
`images`, `link_previews`, `link_preview_ttl_hours` and `nsfw_keywords`.

Keys only the web page uses go in a `[web]` section at the end of
`avatar_chat.ini`. The full table is under "Synchronet web page" in
[CONFIG.md](../../CONFIG.md). In short:

```ini
[web]
web_uploads = on
; web_upload_types = png, jpg, gif, mp3, wav, mp4
; web_upload_max_mb = 20
; web_upload_video_max_mb = 50
; web_upload_retention_days = 7
; web_uploads_per_hour = 30
; web_upload_dir = /sbbs/webv4/root/chatmedia
; web_upload_url_base = https://bbs.example.org/chatmedia
; web_motd_host_qwkid = MYBBS
```

The section must come last. Synchronet's INI reader files every key after
a section header under that section, so a door key placed after `[web]`
would be invisible to the web page.

## Uploads

Uploads are off until `web_uploads = on`. When they are on:

- The composer gets an **Attach** button. The drop zone also takes
  pictures, sound and video, and pasting a file into the composer works
  too. ANSI files still become BITMAP art in the browser and are never
  stored.
- The file goes up in base64 chunks. The Synchronet web server hands
  scripts at most 4 MiB of request body, and each chunk stays under that.
- When the upload finishes, the server reads the file's first bytes to
  decide what it is and which extension to store it under. The name the
  browser sent is ignored, so nothing that could run or render as a page
  (`.ssjs`, `.xjs`, `.html`, `.svg`) can land under the web root. Unknown
  types, HTML, SVG, QuickTime and HEIC are refused.
- JPEG and PNG files have their metadata cut out before they are stored:
  Exif, XMP, IPTC and PNG text chunks, which is where camera details and
  GPS positions live. One side effect: a phone photo that relied on its
  Exif orientation tag may show sideways. Pictures are not re-encoded,
  because SSJS has no image library. Audio and video are stored as
  uploaded.
- Files are saved as `<web_root>/chatmedia/<YYYY-MM-DD>/<24 hex>.<ext>`,
  the link shape the door, the shell and Futureland's site already
  recognise. The link is put in the composer and nothing is posted until
  the user presses Send.
- Limits:
  - Per-type size caps.
  - Three unfinished uploads per user at a time; abandoned ones are swept
    after an hour.
  - `web_uploads_per_hour` per user.
- Day folders older than `web_upload_retention_days` are deleted the next
  time anyone uploads. Only folders named `YYYY-MM-DD` and files named
  `<24 hex>.<ext>` are touched. If another program also writes into that
  folder (Futureland's own uploader, say), give each its own
  `web_upload_dir`.

**MIME types.** Stock `ctrl/mime_types.ini` lists png, jpg, gif, mp3, wav
and mp4, so those are the default `web_upload_types`. To take the others,
add their lines to `ctrl/mime_types.ini` first, then list them in
`web_upload_types`:

```ini
webp = image/webp
ogg  = audio/ogg
flac = audio/flac
m4a  = audio/mp4
webm = video/webm
```

**Behind a reverse proxy.** The link is built from `X-Forwarded-Proto`
and `X-Forwarded-Host` when the proxy sends them. Otherwise it uses the
request's own host. Set `web_upload_url_base` to pin it.

**Why files stay on this BBS** rather than going to a door's `upload_url`:
a stock Synchronet site can already serve static files from its web root,
so no other daemon is needed. The browser posts to the same origin it
loaded the page from. And each sysop hosts their own members' files,
which is the rule for the whole network.

## Security notes

- **Logins.** Every write (posting, private messages, creating a room,
  saving a style, uploading) needs a logged-in member. Link cards need a
  login too.
- **Write token.** A write must also carry a token the page received for
  that login session, sent in an `X-AvatarChat-Token` header. The API
  refuses a write when the browser's `Origin` header names another site.
  A cross-site page can't send that header without a CORS preflight, and
  the web server never grants one. Stock webv4 has a CSRF check, but stock
  login never mints its token, so this package keeps its own, in the
  member's `data/user/NNNN.web` session file. It changes at every login.
- **Rooms that clash with accounts.** A room can't share a name with a
  local account, because `channels.<alias>` is that member's private
  mailbox.
- **Link cards.** The BBS fetches them, never the browser. Only http and
  https on the default ports are fetched, with no user:password in the
  URL. The address must be public; loopback, private, link-local, CGNAT,
  documentation and multicast ranges are refused, for IPv4 and IPv6, and
  checked again at every redirect. At most the first 256 KB is read. Cards
  are cached in `data/avatarchat/previews/`.
  - **DNS rebinding.** The name is checked before `http.js` resolves it
    again, so an answer that changes in between is not caught. The door,
    which connects to the checked address itself, doesn't have this gap.
- **Third-party media.** Pictures, audio and video in messages load
  straight from their own hosts in the viewer's browser, as on
  Futureland's site, with no referrer sent. Card images do too.
- **Escaping.** Message text is always escaped. Links are built so that a
  quote inside a URL can't reach an attribute. The old bundle had that
  hole.

## Moving from the old JS door's bundle

The three old files have the same names as the new ones (`avatarchat.xjs`,
`avatarchat.ssjs`, `lib/events/avatarchat.js`), so copying `dist/webv4/`
over the web directory replaces them.

1. Install and configure the door (`xtrn/avatar_chat_universal`) if you
   haven't. The page reads its `avatar_chat.ini`, never
   `xtrn/avatar_chat/avatar_chat.ini`.
2. Carry your settings over:
   - `host`, `port`, `default_channel` and `motd_channel` have the same
     names and meaning.
   - `motd_host_qwkid` and `motd_host_system` still work at the top of the
     file. Their new home is `[web] web_motd_host_qwkid` and
     `web_motd_host_system`.
   - `poll_delay_ms`, `reconnect_delay_ms` and `input_max_length` were
     terminal-only and aren't used here.
3. Copy `dist/webv4/` over the web directory as above. The URL is
   unchanged: `./?page=avatarchat.xjs`.
4. Once the JS door is gone, delete `xtrn/avatar_chat`. The web page
   doesn't use it.

Compared with the old bundle:

- Settings come from the door's `avatar_chat.ini`.
- **New features:** handle styles (shown and editable), inline pictures,
  media players, link cards with the NSFW veil, mention highlights, and
  uploads.
- **Avatars:** remote users' avatars are found by alias and QWK ID.
- **Composer:** 1,000 characters instead of 500.
- **Security:**
  - Writes need the session token and a same-site origin. The old bundle
    took any POST from a logged-in browser.
  - The link quote hole is fixed.
  - A room can't be named after an account.
- **Code:** the server side is TypeScript in `src/`, compiled to ES5
  (rule: Synchronet code is TypeScript). The browser code moved out of the
  page into `root/avatarchat/app.js`.

## Development

```sh
npm run build          # typecheck, compile src/ to ES5, bundle into dist/webv4/
npm test               # node tests of the pure modules (link guard, sniffing, styles, config)
npm run test:jsexec    # the built SSJS under Synchronet's jsexec against a throwaway chat server
npm run test:browser   # the page in headless Chromium against test/browser/mock.js
```

- **`test:jsexec`** builds `cmd/avatar_chat_server` into a temporary
  directory and runs it on `127.0.0.1:18188`. It wraps each built script so
  that `system`, `user`, `http_request` and `settings` point into a sandbox
  directory, so it never touches a live chat service or the BBS's data.
  Override the binary path with `sh test/jsexec/run.sh /path/to/jsexec`.
- **`test:browser`** needs Playwright, either installed here or pointed to
  with `PLAYWRIGHT=/path/to/node_modules/playwright`, and Go, to encode a
  sample BITMAP with the door's own encoder.

`dist/` is committed so a sysop can install without Node. Rebuild it
after changing `src/`, `browser/` or `pages/`.
