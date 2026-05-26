# Bridge Architecture

Avatar Chat carries richer state than most chat systems: every message can
include a CP437 avatar, plain text, and occasional media envelopes such as
`[BITMAP|...]`. Platform bridges should preserve as much of that as the target
platform can naturally display.

## Shared Core

Bridge implementations should use shared internal packages before adding
platform-specific code:

| Package | Purpose |
| --- | --- |
| `internal/bridgecore` | Platform-neutral message/attachment types and origin identity. |
| `internal/media` | Render Avatar Chat media into bridge-friendly PNG bytes. |
| `internal/chat` | Existing JSON-chat TCP client/protocol implementation. |

Each bridge executable should be a thin adapter:

```text
Avatar Chat JSON <-> normalized bridge message <-> platform API
```

## Origin Rules

Every bridged message needs a full origin:

```text
protocol + network/workspace/server + channel/room
```

Do not suppress by protocol alone. For example, an IRC bridge connected to
Libera should suppress only messages from `irc/libera/#room`; it should still
forward messages from EFnet, synchro.net, Matrix, Discord, etc.

Host markers use:

```text
BRIDGE:<protocol>:<network>/<channel>
```

The first IRC bridge release used `IRC:<network>/<channel>`; `bridgecore`
continues to parse that for compatibility.

## Media Mapping

The common media path should produce attachments that each platform adapter can
send natively:

| Avatar Chat content | Discord/Telegram/Matrix/Slack | IRC |
| --- | --- | --- |
| Text | Message body, formatted where safe | Plain text |
| Avatar payload | Thumbnail/avatar PNG | Omit |
| `[BITMAP|...]` image | PNG attachment | filter/announce/dump |
| ANSI art/file | PNG attachment, optionally raw `.ans` too | announce/dump |
| Photo URL/file | Native image attachment or link | link |
| Audio URL/file | Native audio/file attachment or link | link |

`internal/media` currently renders avatars, parsed BITMAP payloads, and ANSI
frames into PNG. If a CP437 sprite sheet is supplied, it uses glyph rendering;
otherwise it falls back to colored cell/block rendering so bridge adapters can
still produce valid image attachments in minimal deployments.

## Adapters

All four planned adapters are implemented, each a thin command over the shared
core:

1. Discord — `cmd/avatar_chat_discord_bridge` / `internal/discordbridge`. Rich
   embeds, thumbnails, PNG and audio attachments. Uses the `discordgo` SDK.
2. Telegram — `cmd/avatar_chat_telegram_bridge` / `internal/telegrambridge`.
   Native photo/audio uploads. Hand-rolled Bot API client (long-poll), no SDK.
3. Matrix — `cmd/avatar_chat_matrix_bridge` / `internal/matrixbridge`. Native
   media-repo uploads. Hand-rolled Client-Server API client, no SDK.
4. Slack — `cmd/avatar_chat_slack_bridge` / `internal/slackbridge`. Socket Mode
   over `gorilla/websocket` + Web API; native file uploads.

Telegram, Matrix, and Slack share `internal/bridgeenv` for `.env` loading. The
hand-rolled clients mirror `internal/ircbridge/irc.go`: each platform's bridge
needs only a small slice of its API (receive in one channel, send text + media),
so a full SDK would be more dependency than payoff.

### Inbound media safety

Discord attachment URLs are public CDN links, so they're forwarded into Avatar
Chat as-is. Telegram file URLs embed the bot token, Matrix `mxc://` downloads
often require auth, and Slack `url_private` always does — so those three
**annotate inbound attachments by type** (`[photo]`, `[file: name]`, ...) instead
of forwarding a credential-bearing URL.
