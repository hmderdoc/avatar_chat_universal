package telegrambridge

import (
	"context"
	"fmt"
	"html"
	"regexp"
	"strings"

	"github.com/hmderdoc/avatar_chat_universal/internal/bitmap"
	"github.com/hmderdoc/avatar_chat_universal/internal/bridgecore"
	"github.com/hmderdoc/avatar_chat_universal/internal/bridgemedia"
	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
	"github.com/hmderdoc/avatar_chat_universal/internal/media"
)

// telegramMessageToChat converts an inbound Telegram message into an Avatar
// Chat message. Telegram media URLs embed the bot token, so they are never
// forwarded; attachments are annotated by type instead.
func (b *Bridge) telegramMessageToChat(m *tgMessage) (*chat.Message, bool) {
	if m == nil || (m.From != nil && m.From.IsBot) {
		return nil, false
	}

	text := strings.TrimSpace(m.Text)
	if text == "" {
		text = strings.TrimSpace(m.Caption)
	}
	if b.Config.Bridge.IncludeAttachmentURL {
		if note := attachmentNote(m); note != "" {
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

	return &chat.Message{
		Nick: &chat.Nick{
			Name: telegramDisplayName(m),
			Host: bridgecore.FormatHostMarker(b.origin()),
		},
		Str:  text,
		Time: nowMs(),
	}, true
}

// attachmentNote describes a non-text attachment without leaking the
// token-bearing Telegram file URL.
func attachmentNote(m *tgMessage) string {
	switch {
	case len(m.Photo) > 0:
		return "[photo]"
	case m.Audio != nil:
		name := strings.TrimSpace(strings.TrimSpace(m.Audio.Performer + " - " + m.Audio.Title))
		name = strings.Trim(name, " -")
		if name != "" {
			return "[audio: " + name + "]"
		}
		return "[audio]"
	case m.Voice != nil:
		return "[voice message]"
	case m.Document != nil:
		if n := strings.TrimSpace(m.Document.FileName); n != "" {
			return "[file: " + n + "]"
		}
		return "[file]"
	case m.Sticker != nil:
		if e := strings.TrimSpace(m.Sticker.Emoji); e != "" {
			return "[sticker " + e + "]"
		}
		return "[sticker]"
	}
	return ""
}

func telegramDisplayName(m *tgMessage) string {
	if m.From != nil {
		name := strings.TrimSpace(strings.TrimSpace(m.From.FirstName) + " " + strings.TrimSpace(m.From.LastName))
		if name != "" {
			return name
		}
		if m.From.Username != "" {
			return m.From.Username
		}
	}
	// Channel posts have no From; fall back to the channel title.
	if m.Chat != nil && strings.TrimSpace(m.Chat.Title) != "" {
		return strings.TrimSpace(m.Chat.Title)
	}
	return "telegram"
}

// sendChatMessage forwards an Avatar Chat message out to Telegram, mapping
// avatars / BITMAP payloads / linked media to native photo and audio uploads
// where possible.
func (b *Bridge) sendChatMessage(ctx context.Context, tg *tgClient, msg *chat.Message) error {
	if msg == nil || strings.TrimSpace(msg.Str) == "" {
		return nil
	}
	if b.ignoredChatToTelegram(msg) {
		return nil
	}
	// Don't echo our own forwarded messages back to Telegram.
	if msg.Nick != nil {
		if origin, ok := bridgecore.ParseHostMarker(msg.Nick.Host); ok && origin.Matches(b.origin()) {
			return nil
		}
	}
	chatID := b.Config.Telegram.ChatID
	label := htmlLabel(msg)

	if bitmap.IsBitmap(msg.Str) {
		if !b.Config.Bridge.IncludeBitmapImage {
			return tg.sendMessage(ctx, chatID, label+" <i>[image omitted]</i>", "HTML")
		}
		img, err := bitmap.Parse(msg.Str)
		if err != nil {
			return tg.sendMessage(ctx, chatID, label+" <i>[image omitted]</i>", "HTML")
		}
		pngBytes, err := media.BitmapPNG(img, media.RenderOptions{})
		if err != nil {
			return tg.sendMessage(ctx, chatID, fmt.Sprintf("%s <i>[image %dx%d omitted]</i>", label, img.Width, img.Height), "HTML")
		}
		caption := fmt.Sprintf("%s sent ANSI art (%dx%d)", label, img.Width, img.Height)
		return tg.sendPhoto(ctx, chatID, "avatar-chat-image.png", pngBytes, caption, "HTML")
	}

	text := stripChatControls(msg.Str)
	clean, refs := bridgemedia.ScanRefs(text, b.publicMediaURL)

	// Plain text: with avatars enabled and one present, post the sender's avatar
	// as the image with the (HTML-styled) text as its caption; otherwise a
	// styled text message.
	if clean != "" {
		if err := b.sendBody(ctx, tg, chatID, label, clean, msg); err != nil {
			return err
		}
	}

	for _, ref := range refs {
		switch ref.Kind {
		case bridgemedia.KindImage:
			b.sendImageRef(ctx, tg, chatID, msg, ref)
		case bridgemedia.KindAudio:
			b.sendAudioRef(ctx, tg, chatID, msg, ref)
		}
	}
	return nil
}

// tgCaptionLimit is Telegram's maximum photo-caption length.
const tgCaptionLimit = 1024

// sendBody posts a text message. With avatars enabled and the sender has one
// (and the caption is short enough to ride on a photo), it sends the avatar as
// the image with the HTML-styled text as caption; otherwise a styled text
// message. On any render/send failure it falls back to the text message.
func (b *Bridge) sendBody(ctx context.Context, tg *tgClient, chatID, label, clean string, msg *chat.Message) error {
	body := label + "\n" + htmlEscape(clean)
	if b.Config.Bridge.IncludeAvatarImage && msg != nil && msg.Nick != nil && msg.Nick.Avatar != "" && len([]rune(body)) <= tgCaptionLimit {
		if pngBytes, err := media.AvatarPNG(msg.Nick.Avatar, media.RenderOptions{}); err == nil {
			if err := tg.sendPhoto(ctx, chatID, "avatar.png", pngBytes, body, "HTML"); err == nil {
				return nil
			}
		}
		// fall through to a plain text message on render/send failure
	}
	return tg.sendMessage(ctx, chatID, body, "HTML")
}

func (b *Bridge) sendImageRef(ctx context.Context, tg *tgClient, chatID string, msg *chat.Message, ref bridgemedia.Ref) {
	caption := htmlLabel(msg)
	if ref.Label != "" {
		caption += " " + htmlEscape(ref.Label)
	}
	if b.Config.Bridge.IncludeMedia && b.fetchMedia != nil {
		if data, err := b.fetchMedia(ref.URL); err == nil && len(data) > 0 {
			if ext, _, ok := bridgemedia.SniffImageExt(data); ok {
				if err := tg.sendPhoto(ctx, chatID, "media."+ext, data, caption, "HTML"); err == nil {
					return
				}
			}
		}
	}
	// Fallback: forward the link as plain text.
	link := ref.URL
	if ref.Label != "" {
		link = ref.Label + ": " + ref.URL
	}
	if err := tg.sendMessage(ctx, chatID, link, ""); err != nil {
		b.Logger.Printf("bridge: chat->telegram image link send failed: %v", err)
	}
}

func (b *Bridge) sendAudioRef(ctx context.Context, tg *tgClient, chatID string, msg *chat.Message, ref bridgemedia.Ref) {
	if b.Config.Bridge.IncludeMedia && b.fetchMedia != nil {
		if ext := bridgemedia.AudioExt(ref.URL); ext != "" {
			if data, err := b.fetchMedia(ref.URL); err == nil && len(data) > 0 {
				meta, _ := bridgemedia.ParseID3(data)
				name := bridgemedia.AudioFileName(meta, ref.URL, ext)
				if name == "" {
					name = "audio." + ext
				}
				title := bridgemedia.AudioTitle(meta, ref.Label, ref.URL)
				if err := tg.sendAudio(ctx, chatID, name, data, htmlLabel(msg), "HTML", title, meta.Artist, b.audioThumb(meta, msg)); err == nil {
					return
				}
			}
		}
	}
	link := ref.URL
	if ref.Label != "" {
		link = ref.Label + ": " + ref.URL
	}
	if err := tg.sendMessage(ctx, chatID, link, ""); err != nil {
		b.Logger.Printf("bridge: chat->telegram audio link send failed: %v", err)
	}
}

// audioThumb returns a Telegram-legal JPEG thumbnail for an audio message:
// embedded ID3 cover art when present, else (when include_avatar_image is on)
// the sender's avatar so the player still carries a small image.
func (b *Bridge) audioThumb(meta bridgemedia.Meta, msg *chat.Message) []byte {
	if len(meta.Art) > 0 {
		if t, ok := bridgemedia.ThumbnailJPEG(meta.Art, 320, 200*1024); ok {
			return t
		}
	}
	if b.Config.Bridge.IncludeAvatarImage && msg != nil && msg.Nick != nil && msg.Nick.Avatar != "" {
		if pngBytes, err := media.AvatarPNG(msg.Nick.Avatar, media.RenderOptions{}); err == nil {
			if t, ok := bridgemedia.ThumbnailJPEG(pngBytes, 320, 200*1024); ok {
				return t
			}
		}
	}
	return nil
}

func (b *Bridge) ignoredChatToTelegram(msg *chat.Message) bool {
	if b == nil || b.Config == nil || len(b.Config.Bridge.ChatToTelegramIgnore) == 0 {
		return false
	}
	text := ""
	if msg != nil {
		text = msg.Str
		if msg.Nick != nil && msg.Nick.Name != "" {
			text = msg.Nick.Name + ": " + text
		}
	}
	for _, pattern := range b.Config.Bridge.ChatToTelegramIgnore {
		if pattern == "" {
			continue
		}
		if ok, err := regexp.MatchString(pattern, text); err == nil && ok {
			return true
		}
	}
	return false
}

// htmlLabel renders the sender as an HTML-formatted label: a bold name, plus an
// italic upstream-origin tag when the message reached Avatar Chat through
// another bridge. All interpolated text is HTML-escaped for Telegram's HTML
// parse mode.
func htmlLabel(msg *chat.Message) string {
	name := "<b>" + htmlEscape(nickName(msg)) + "</b>"
	if msg == nil || msg.Nick == nil || strings.TrimSpace(msg.Nick.Host) == "" {
		return name
	}
	if origin, ok := bridgecore.ParseHostMarker(strings.TrimSpace(msg.Nick.Host)); ok {
		prefix := strings.ToUpper(origin.Protocol)
		if prefix == "" {
			prefix = "BRIDGE"
		}
		return name + " <i>via " + htmlEscape(prefix+":"+origin.Display()) + "</i>"
	}
	return name
}

func htmlEscape(s string) string { return html.EscapeString(s) }

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
// (CP437 terminals) and flattens newlines, matching the Discord bridge.
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
