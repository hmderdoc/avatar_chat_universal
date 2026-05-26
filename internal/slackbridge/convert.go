package slackbridge

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/hmderdoc/avatar_chat_universal/internal/bitmap"
	"github.com/hmderdoc/avatar_chat_universal/internal/bridgecore"
	"github.com/hmderdoc/avatar_chat_universal/internal/bridgemedia"
	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
	"github.com/hmderdoc/avatar_chat_universal/internal/media"
)

// botSubtypes are message subtypes that are not plain human posts. Messages
// carrying these (or a non-empty bot_id) are not forwarded into Avatar Chat.
var botSubtypes = map[string]bool{
	"bot_message":     true,
	"message_changed": true,
	"message_deleted": true,
	"channel_join":    true,
	"channel_leave":   true,
	"channel_topic":   true,
	"channel_purpose": true,
	"channel_name":    true,
	"channel_archive": true,
	"thread_broadcast": true,
}

// slackMessageToChat converts an inbound Slack message event into an Avatar
// Chat message. Slack file URLs (url_private) require the bot token to
// download, so they are never forwarded; attachments are annotated by type
// instead.
func (b *Bridge) slackMessageToChat(ctx context.Context, sc *slackClient, payload *slackEventPayload) (*chat.Message, bool) {
	if payload == nil {
		return nil, false
	}
	// Learn the workspace id for the origin from the first event we see.
	if t := strings.TrimSpace(payload.TeamID); t != "" {
		b.setTeam(t)
	} else if t := strings.TrimSpace(payload.Event.Team); t != "" {
		b.setTeam(t)
	}

	ev := payload.Event
	if ev.Type != "message" {
		return nil, false
	}
	// Only forward real human posts: skip bot messages and edits/joins/etc.
	if strings.TrimSpace(ev.BotID) != "" {
		return nil, false
	}
	if ev.Subtype != "" && botSubtypes[ev.Subtype] {
		return nil, false
	}

	text := unescapeSlack(ev.Text)
	if b.Config.Bridge.IncludeAttachmentURL {
		if note := attachmentNote(ev.Files); note != "" {
			if text != "" {
				text += " "
			}
			text += note
		}
	}
	text = sanitizeText(text)
	if text == "" {
		return nil, false
	}

	name := "slack"
	if sc != nil {
		name = sc.userName(ctx, ev.User)
	} else if ev.User != "" {
		name = ev.User
	}

	return &chat.Message{
		Nick: &chat.Nick{
			Name: name,
			Host: bridgecore.FormatHostMarker(b.origin()),
		},
		Str:  text,
		Time: nowMs(),
	}, true
}

// attachmentNote describes inbound Slack files without leaking the
// token-bearing url_private. Mirrors the Telegram bridge's attachmentNote.
func attachmentNote(files []slackFile) string {
	if len(files) == 0 {
		return ""
	}
	var notes []string
	for _, f := range files {
		name := strings.TrimSpace(f.Title)
		if name == "" {
			name = strings.TrimSpace(f.Name)
		}
		switch {
		case name != "":
			notes = append(notes, "[file: "+name+"]")
		case strings.HasPrefix(f.Mimetype, "image/"):
			notes = append(notes, "[image]")
		case strings.HasPrefix(f.Mimetype, "audio/"):
			notes = append(notes, "[audio]")
		default:
			notes = append(notes, "[file]")
		}
	}
	return strings.Join(notes, " ")
}

// slackLinkRe matches Slack's <url|label> / <url> link markup.
var slackLinkRe = regexp.MustCompile(`<([^<>|]+)(\|([^<>]*))?>`)

// unescapeSlack strips Slack message markup: converts <url|label> -> label,
// <url> -> url, drops <@U123>/<#C123>/<!subteam> mentions to a readable form,
// and unescapes the &amp; / &lt; / &gt; entities Slack inserts.
func unescapeSlack(s string) string {
	s = slackLinkRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := slackLinkRe.FindStringSubmatch(m)
		target := sub[1]
		label := sub[3]
		switch {
		case strings.HasPrefix(target, "@"), strings.HasPrefix(target, "#"):
			// User/channel mention. Keep the label if Slack supplied one,
			// otherwise drop the raw id rather than leak <@U123>.
			if label != "" {
				return label
			}
			return ""
		case strings.HasPrefix(target, "!"):
			// Special mention like <!here>/<!channel>; keep label or the word.
			if label != "" {
				return label
			}
			return strings.TrimPrefix(target, "!")
		default:
			if label != "" {
				return label
			}
			return target
		}
	})
	// Order matters: unescape &amp; last so a literal "&amp;lt;" stays intact.
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&amp;", "&")
	return strings.TrimSpace(s)
}

// sendChatMessage forwards an Avatar Chat message out to Slack, mapping
// avatars / BITMAP payloads / linked media to native uploads where possible.
func (b *Bridge) sendChatMessage(ctx context.Context, sc *slackClient, msg *chat.Message) error {
	if msg == nil || strings.TrimSpace(msg.Str) == "" {
		return nil
	}
	if b.ignoredChatToSlack(msg) {
		return nil
	}
	// Don't echo our own forwarded messages back to Slack.
	if msg.Nick != nil {
		if origin, ok := bridgecore.ParseHostMarker(msg.Nick.Host); ok && origin.Matches(b.origin()) {
			return nil
		}
	}
	channel := b.Config.Slack.ChannelID

	callCtx, cancel := callTimeout(ctx)
	defer cancel()

	if bitmap.IsBitmap(msg.Str) {
		if !b.Config.Bridge.IncludeBitmapImage {
			return b.postCard(callCtx, sc, channel, msg, "[image omitted]")
		}
		img, err := bitmap.Parse(msg.Str)
		if err != nil {
			return b.postCard(callCtx, sc, channel, msg, "[image omitted]")
		}
		pngBytes, err := media.BitmapPNG(img, media.RenderOptions{})
		if err != nil {
			return b.postCard(callCtx, sc, channel, msg, fmt.Sprintf("[image %dx%d omitted]", img.Width, img.Height))
		}
		// Header card (avatar + name) then the art image below it.
		_ = b.postCard(callCtx, sc, channel, msg, fmt.Sprintf("sent ANSI art (%dx%d)", img.Width, img.Height))
		if err := sc.uploadFile(callCtx, channel, "avatar-chat-image.png", "ANSI art", pngBytes, ""); err != nil {
			b.Logger.Printf("bridge: chat->slack bitmap send failed: %v", err)
		}
		return nil
	}

	text := stripChatControls(msg.Str)
	clean, refs := bridgemedia.ScanRefs(text, b.publicMediaURL)

	// One grouped card -- avatar + name + body -- whenever there's body text or
	// media to attribute beneath it.
	if clean != "" || len(refs) > 0 {
		if err := b.postCard(callCtx, sc, channel, msg, clean); err != nil {
			return err
		}
	}

	for _, ref := range refs {
		switch ref.Kind {
		case bridgemedia.KindImage:
			b.sendImageRef(callCtx, sc, channel, ref)
		case bridgemedia.KindAudio:
			b.sendAudioRef(callCtx, sc, channel, ref)
		}
	}
	return nil
}

// postCard posts the sender's message: a bold mrkdwn name (+ origin tag) and the
// body. When avatars are enabled and the sender has one, it's sent as an
// uploaded image with that text as the file's comment -- Slack's only way to
// show a generated avatar (an inline icon needs a public URL, which is
// Pro-gated). With no avatar it's a plain mrkdwn message. mrkdwn has no text
// color, so names are bold only.
func (b *Bridge) postCard(ctx context.Context, sc *slackClient, channel string, msg *chat.Message, body string) error {
	text := mrkdwnLabel(msg)
	if body != "" {
		text += "\n" + slackEscape(body)
	}
	if b.Config.Bridge.IncludeAvatarImage && msg != nil && msg.Nick != nil && msg.Nick.Avatar != "" {
		if pngBytes, err := media.AvatarPNG(msg.Nick.Avatar, media.RenderOptions{}); err == nil {
			if err := sc.uploadFile(ctx, channel, "avatar.png", nickName(msg)+" avatar", pngBytes, text); err == nil {
				return nil
			}
			// fall through to a plain message on render/upload failure
		}
	}
	return sc.postMessage(ctx, channel, text)
}

func (b *Bridge) sendImageRef(ctx context.Context, sc *slackClient, channel string, ref bridgemedia.Ref) {
	if b.Config.Bridge.IncludeMedia && b.fetchMedia != nil {
		if data, err := b.fetchMedia(ref.URL); err == nil && len(data) > 0 {
			if ext, _, ok := bridgemedia.SniffImageExt(data); ok {
				if err := sc.uploadFile(ctx, channel, "media."+ext, ref.Label, data, ref.Label); err == nil {
					return
				}
			}
		}
	}
	// Fallback: post the link as text. Slack unfurls http(s) URLs.
	caption := ref.URL
	if ref.Label != "" {
		caption = ref.Label + ": " + ref.URL
	}
	if err := sc.postMessage(ctx, channel, caption); err != nil {
		b.Logger.Printf("bridge: chat->slack image link send failed: %v", err)
	}
}

func (b *Bridge) sendAudioRef(ctx context.Context, sc *slackClient, channel string, ref bridgemedia.Ref) {
	if b.Config.Bridge.IncludeMedia && b.fetchMedia != nil {
		if ext := bridgemedia.AudioExt(ref.URL); ext != "" {
			if data, err := b.fetchMedia(ref.URL); err == nil && len(data) > 0 {
				meta, _ := bridgemedia.ParseID3(data)
				// Post the cover art as a visible image first when present.
				if len(meta.Art) > 0 {
					if art, ok := bridgemedia.ThumbnailJPEG(meta.Art, 512, 1<<20); ok {
						_ = sc.uploadFile(ctx, channel, "cover.jpg", "cover", art, "")
					}
				}
				name := bridgemedia.AudioFileName(meta, ref.URL, ext)
				if name == "" {
					name = "audio." + ext
				}
				title := bridgemedia.AudioTitle(meta, ref.Label, ref.URL)
				if err := sc.uploadFile(ctx, channel, name, title, data, title); err == nil {
					return
				}
			}
		}
	}
	caption := ref.URL
	if ref.Label != "" {
		caption = ref.Label + ": " + ref.URL
	}
	if err := sc.postMessage(ctx, channel, caption); err != nil {
		b.Logger.Printf("bridge: chat->slack audio link send failed: %v", err)
	}
}

func (b *Bridge) ignoredChatToSlack(msg *chat.Message) bool {
	if b == nil || b.Config == nil || len(b.Config.Bridge.ChatToSlackIgnore) == 0 {
		return false
	}
	text := ""
	if msg != nil {
		text = msg.Str
		if msg.Nick != nil && msg.Nick.Name != "" {
			text = msg.Nick.Name + ": " + text
		}
	}
	for _, pattern := range b.Config.Bridge.ChatToSlackIgnore {
		if pattern == "" {
			continue
		}
		if ok, err := regexp.MatchString(pattern, text); err == nil && ok {
			return true
		}
	}
	return false
}

// authorLabel renders the sender's name, suffixed with the upstream origin when
// the message reached Avatar Chat through another bridge.
func authorLabel(msg *chat.Message) string {
	name := nickName(msg)
	if msg == nil || msg.Nick == nil || strings.TrimSpace(msg.Nick.Host) == "" {
		return name
	}
	host := strings.TrimSpace(msg.Nick.Host)
	if origin, ok := bridgecore.ParseHostMarker(host); ok {
		prefix := strings.ToUpper(origin.Protocol)
		if prefix == "" {
			prefix = "BRIDGE"
		}
		return name + " -- " + prefix + ":" + origin.Display()
	}
	return name
}

// slackEscape escapes the three characters Slack treats specially in message
// text (Slack un-escapes them on display).
func slackEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// mrkdwnLabel is authorLabel for Slack mrkdwn: a bold name plus an italic
// upstream-origin tag, with text escaped for Slack.
func mrkdwnLabel(msg *chat.Message) string {
	name := "*" + slackEscape(nickName(msg)) + "*"
	if msg == nil || msg.Nick == nil || strings.TrimSpace(msg.Nick.Host) == "" {
		return name
	}
	if origin, ok := bridgecore.ParseHostMarker(strings.TrimSpace(msg.Nick.Host)); ok {
		prefix := strings.ToUpper(origin.Protocol)
		if prefix == "" {
			prefix = "BRIDGE"
		}
		return name + " _via " + slackEscape(prefix+":"+origin.Display()) + "_"
	}
	return name
}

func nickName(msg *chat.Message) string {
	if msg != nil && msg.Nick != nil && msg.Nick.Name != "" {
		return msg.Nick.Name
	}
	return "unknown"
}

func (b *Bridge) publicMediaURL(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" || strings.HasPrefix(rawURL, "http://") || strings.HasPrefix(rawURL, "https://") {
		return rawURL
	}
	if b == nil || b.Config == nil || b.Config.Bridge.PublicBaseURL == "" {
		return rawURL
	}
	base := strings.TrimRight(b.Config.Bridge.PublicBaseURL, "/")
	path := rawURL
	if strings.HasPrefix(path, "./") {
		path = path[1:]
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return base + path
}

func safeFilePart(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "avatar"
	}
	return b.String()
}

func stripChatControls(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case 0x01, 0x02:
			if i+1 < len(s) {
				i++
			}
		default:
			b.WriteByte(s[i])
		}
	}
	return strings.TrimSpace(b.String())
}

// sanitizeText strips terminal control bytes from text bound for the BBS chat
// (CP437 terminals) and flattens newlines, matching the Telegram bridge.
func sanitizeText(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\f', '\r', '\b', 0x07, 0x14, 0x15, 0x10, 0x1b:
			continue
		case '\n':
			b.WriteString(" / ")
		default:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}
