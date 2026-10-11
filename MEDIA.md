# Chat attachments (`/upload`)

Callers can share a picture or a piece of ANSI art in chat: `/upload` takes
one file by Zmodem, hands it to **your** media server, and puts the link it
gets back in the caller's input line. They press Enter to send it, and every
client shows it the way it shows any link: an inline picture in the door, an
image card or the ANSI viewer on the web.

The file lives on your server, not on the chat network. Other boards only
ever see a URL, so where you keep the files, how long, and who may upload
are your call.

## Run the media endpoint

The bundled `avatar_chat_server` has an optional HTTP endpoint for this. It
is off unless you give it an address:

```sh
./avatar_chat_server -addr :10088 -media-addr :10089 -media-dir /srv/chatmedia \
    -media-url-base https://bbs.example.org:10089
```

or put the same keys in an INI file and pass `-config`:

```ini
; avatar_chat_server.ini
[media]
media_addr     = :10089
media_dir      = /srv/chatmedia
media_url_base = https://bbs.example.org:10089
retention_days = 7
```

Then point the door at it in `avatar_chat.ini`:

```ini
upload_url = https://bbs.example.org:10089/upload
```

The media endpoint can run beside your own chat server or by itself: a door
that chats on `futureland.today:10088` can still upload to your media
endpoint, because the chat network only carries the link.

| Key (INI) | Flag | Default | Meaning |
| --- | --- | --- | --- |
| `media_addr` | `-media-addr` | (off) | HTTP listen address, e.g. `:10089`. |
| `media_dir` | `-media-dir` | `chatmedia` | Where files are stored. |
| `media_url_base` | `-media-url-base` | `http://<request host>` | The public base URL put in links. Set it when the endpoint sits behind a proxy or uses HTTPS. |
| `max_image_mb` | `-max-image-mb` | `20` | Largest picture accepted. |
| `max_ansi_mb` | `-max-ansi-mb` | `1` | Largest art file accepted. |
| `retention_days` | `-retention-days` | `7` | Files older than this are deleted (checked hourly). |
| `max_store_mb` | `-max-store-mb` | `500` | Total storage; uploads are refused when full. |
| `uploads_per_hour` | `-uploads-per-hour` | `30` | Uploads per remote address per hour; `0` = no limit. |

An explicit flag wins over the INI.

## What it accepts and how it serves it

- **Pictures:** png, jpeg, gif, webp and bmp, recognised by their bytes, not
  their names. Every picture is decoded and re-encoded, which drops EXIF and
  GPS data, comments, and anything hidden after the image. bmp and webp are
  stored as png; animated GIFs keep their frames.
- **ANSI art:** `.ans` (also `.asc`, `.diz`, `.nfo`, stored as `.ans`) and
  `.bin` BinaryText, kept byte for byte with any SAUCE record. Art has no
  magic number, so the file name decides, and files that are mostly control
  bytes are refused.
- **Never:** HTML, SVG, XML or anything else, whatever it is named.

Files are stored and served as `/chatmedia/<YYYY-MM-DD>/<24 hex>.<ext>`, the
layout the Synchronet web chat uses, because clients recognise an attachment
(and show an "expired" card once it is gone) by that path. Nothing outside
that layout is served. Every response carries a fixed `Content-Type`,
`X-Content-Type-Options: nosniff` and `Content-Security-Policy: default-src
'none'; sandbox`. Art is served as `text/plain; charset=IBM437` (`.bin` as
`application/octet-stream`) with an attachment disposition.
`Access-Control-Allow-Origin: *` is set so a plain web page can fetch and
upload too.

Each upload is logged on stderr: time, remote address, offered name, type,
size and stored path.

## The upload API

`POST /upload?name=<original file name>` with the raw file as the request
body (any `Content-Type`). The answer is JSON:

```json
{"url": "https://bbs.example.org:10089/chatmedia/2026-10-11/0a6894cc9956583bd1adf3d1.png",
 "type": "image", "expiresDays": 7}
```

or `{"error": "..."}` with a 4xx/5xx status (413 too large, 415 not an
accepted type, 429 rate limited, 507 store full).

## HTTPS

If your website is served over HTTPS, browsers refuse pictures loaded over
plain HTTP on it (mixed content), so web users would not see your links.
Put the endpoint behind your web server's TLS (a reverse proxy of
`/upload` and `/chatmedia/` to the media port) and set `media_url_base` to
the HTTPS address. Terminal callers are not affected either way.

## There is no login

Anyone who can reach the endpoint can upload to it, within the limits
above. Keep `uploads_per_hour`, `max_store_mb` and `retention_days` at
levels you are comfortable with, or only expose the port where you need it
(for example, keep it on localhost and let only the door's host reach it,
with your web server proxying `/chatmedia/` for the public). An
authenticated upload is a likely future addition.
