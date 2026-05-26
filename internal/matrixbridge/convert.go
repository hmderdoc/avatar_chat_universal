package matrixbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"html"
	"image"
	_ "image/png" // register PNG decoder for image.DecodeConfig
	"regexp"
	"strings"

	"github.com/hmderdoc/avatar_chat_universal/internal/bitmap"
	"github.com/hmderdoc/avatar_chat_universal/internal/bridgecore"
	"github.com/hmderdoc/avatar_chat_universal/internal/bridgemedia"
	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
	"github.com/hmderdoc/avatar_chat_universal/internal/media"
)

// matrixMessageToChat converts an inbound m.room.message event into an Avatar
// Chat message. Inbound media carries an mxc:// url that needs the homeserver
// plus auth to download, so it is never forwarded; attachments are annotated
// by type instead.
func (b *Bridge) matrixMessageToChat(ctx context.Context, mx *mxClient, ev *mxEvent) (*chat.Message, bool) {
	if ev == nil {
		return nil, false
	}
	var content mxMessageContent
	if len(ev.Content) > 0 {
		_ = json.Unmarshal(ev.Content, &content)
	}

	text := strings.TrimSpace(content.Body)
	switch content.MsgType {
	case "m.image", "m.audio", "m.video", "m.file":
		// Don't leak the mxc/auth URL into chat; annotate by type instead.
		note := attachmentNote(&content)
		if b.Config.Bridge.IncludeAttachmentURL {
			text = note
		} else {
			text = ""
		}
	}
	text = sanitizeText(text)
	if text == "" {
		return nil, false
	}

	name := localpart(ev.Sender)
	if mx != nil {
		name = mx.displayName(ctx, ev.Sender)
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

// attachmentNote describes a non-text attachment without leaking the
// mxc:// content URL (which needs the homeserver + auth to resolve).
func attachmentNote(content *mxMessageContent) string {
	name := strings.TrimSpace(content.Body)
	if name == "" {
		name = strings.TrimSpace(content.FileName)
	}
	switch content.MsgType {
	case "m.image":
		if name != "" {
			return "[image: " + name + "]"
		}
		return "[image]"
	case "m.audio":
		if name != "" {
			return "[audio: " + name + "]"
		}
		return "[audio]"
	case "m.video":
		if name != "" {
			return "[video: " + name + "]"
		}
		return "[video]"
	case "m.file":
		if name != "" {
			return "[file: " + name + "]"
		}
		return "[file]"
	}
	return ""
}

// sendChatMessage forwards an Avatar Chat message out to Matrix, mapping
// avatars / BITMAP payloads / linked media to native uploaded m.image and
// m.audio events where possible.
func (b *Bridge) sendChatMessage(ctx context.Context, mx *mxClient, msg *chat.Message) error {
	if msg == nil || strings.TrimSpace(msg.Str) == "" {
		return nil
	}
	if b.ignoredChatToMatrix(msg) {
		return nil
	}
	// Don't echo our own forwarded messages back to Matrix.
	if msg.Nick != nil {
		if origin, ok := bridgecore.ParseHostMarker(msg.Nick.Host); ok && origin.Matches(b.origin()) {
			return nil
		}
	}
	roomID := b.Config.Matrix.RoomID

	if bitmap.IsBitmap(msg.Str) {
		if !b.Config.Bridge.IncludeBitmapImage {
			plain, formatted := b.chatCard(ctx, mx, msg, "[image omitted]")
			return mx.sendHTML(ctx, roomID, plain, formatted)
		}
		img, err := bitmap.Parse(msg.Str)
		if err != nil {
			plain, formatted := b.chatCard(ctx, mx, msg, "[image omitted]")
			return mx.sendHTML(ctx, roomID, plain, formatted)
		}
		pngBytes, err := media.BitmapPNG(img, media.RenderOptions{})
		if err != nil {
			plain, formatted := b.chatCard(ctx, mx, msg, fmt.Sprintf("[image %dx%d omitted]", img.Width, img.Height))
			return mx.sendHTML(ctx, roomID, plain, formatted)
		}
		// Header card (avatar + name) then the art image below it.
		plain, formatted := b.chatCard(ctx, mx, msg, fmt.Sprintf("sent ANSI art (%dx%d)", img.Width, img.Height))
		_ = mx.sendHTML(ctx, roomID, plain, formatted)
		if err := mx.sendMedia(ctx, roomID, "m.image", "avatar-chat-image.png", "image/png", pngBytes); err != nil {
			b.Logger.Printf("bridge: chat->matrix bitmap send failed: %v", err)
		}
		return nil
	}

	text := stripChatControls(msg.Str)
	clean, refs := bridgemedia.ScanRefs(text, b.publicMediaURL)

	// One grouped card -- avatar + color-coded name + body -- sent whenever
	// there's body text or media to attribute beneath it.
	if clean != "" || len(refs) > 0 {
		plain, formatted := b.chatCard(ctx, mx, msg, clean)
		if err := mx.sendHTML(ctx, roomID, plain, formatted); err != nil {
			return err
		}
	}

	for _, ref := range refs {
		switch ref.Kind {
		case bridgemedia.KindImage:
			b.sendImageRef(ctx, mx, roomID, ref)
		case bridgemedia.KindAudio:
			b.sendAudioRef(ctx, mx, roomID, ref)
		}
	}
	return nil
}

func (b *Bridge) sendImageRef(ctx context.Context, mx *mxClient, roomID string, ref bridgemedia.Ref) {
	if b.Config.Bridge.IncludeMedia && b.fetchMedia != nil {
		if data, err := b.fetchMedia(ref.URL); err == nil && len(data) > 0 {
			if ext, ctype, ok := bridgemedia.SniffImageExt(data); ok {
				name := "media." + ext
				if err := mx.sendMedia(ctx, roomID, "m.image", name, ctype, data); err == nil {
					return
				}
			}
		}
	}
	// Fallback: forward the link as text.
	caption := ref.URL
	if ref.Label != "" {
		caption = ref.Label + ": " + ref.URL
	}
	if err := mx.sendText(ctx, roomID, caption); err != nil {
		b.Logger.Printf("bridge: chat->matrix image link send failed: %v", err)
	}
}

func (b *Bridge) sendAudioRef(ctx context.Context, mx *mxClient, roomID string, ref bridgemedia.Ref) {
	if b.Config.Bridge.IncludeMedia && b.fetchMedia != nil {
		if ext := bridgemedia.AudioExt(ref.URL); ext != "" {
			if data, err := b.fetchMedia(ref.URL); err == nil && len(data) > 0 {
				meta, _ := bridgemedia.ParseID3(data)
				// Matrix audio players don't surface embedded cover art, so post
				// it as a visible image first when present.
				if len(meta.Art) > 0 {
					if art, ok := bridgemedia.ThumbnailJPEG(meta.Art, 512, 1<<20); ok {
						_ = mx.sendMedia(ctx, roomID, "m.image", "cover.jpg", "image/jpeg", art)
					}
				}
				name := bridgemedia.AudioFileName(meta, ref.URL, ext)
				if name == "" {
					name = "audio." + ext
				}
				ctype := bridgemedia.AudioContentType(ext)
				if err := mx.sendMedia(ctx, roomID, "m.audio", name, ctype, data); err == nil {
					return
				}
			}
		}
	}
	caption := ref.URL
	if ref.Label != "" {
		caption = ref.Label + ": " + ref.URL
	}
	if err := mx.sendText(ctx, roomID, caption); err != nil {
		b.Logger.Printf("bridge: chat->matrix audio link send failed: %v", err)
	}
}

func (b *Bridge) ignoredChatToMatrix(msg *chat.Message) bool {
	if b == nil || b.Config == nil || len(b.Config.Bridge.ChatToMatrixIgnore) == 0 {
		return false
	}
	text := ""
	if msg != nil {
		text = msg.Str
		if msg.Nick != nil && msg.Nick.Name != "" {
			text = msg.Nick.Name + ": " + text
		}
	}
	for _, pattern := range b.Config.Bridge.ChatToMatrixIgnore {
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

// nickPalette is a set of distinct, readable-on-dark colors used to give each
// sender a stable color so names are visually distinguishable.
var nickPalette = []string{
	"#e06c75", "#98c379", "#e5c07b", "#61afef", "#c678dd", "#56b6c2",
	"#d19a66", "#7fdbca", "#f78c6c", "#82aaff", "#c3e88d", "#ff6ac1",
}

// nickColor maps a name to a stable palette color via a hash, so the same
// person always renders in the same color.
func nickColor(name string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(strings.ToLower(strings.TrimSpace(name))))
	return nickPalette[h.Sum32()%uint32(len(nickPalette))]
}

// chatCard builds one formatted message that groups the sender's avatar, a
// color-coded name (+ origin tag), and the body into a bordered block. Matrix's
// allowed HTML is limited: <blockquote> gives the bordered grouping, <font
// color>/data-mx-color the per-name color, and an inline mxc <img> puts the
// avatar in the same event as the name. Returns (plain, formatted); the plain
// string is the fallback body for clients that don't render HTML.
func (b *Bridge) chatCard(ctx context.Context, mx *mxClient, msg *chat.Message, body string) (string, string) {
	name := nickName(msg)
	color := nickColor(name)

	originPlain, originHTML := "", ""
	if msg != nil && msg.Nick != nil {
		if origin, ok := bridgecore.ParseHostMarker(strings.TrimSpace(msg.Nick.Host)); ok {
			prefix := strings.ToUpper(origin.Protocol)
			if prefix == "" {
				prefix = "BRIDGE"
			}
			originPlain = " -- " + prefix + ":" + origin.Display()
			originHTML = " <i>via " + htmlEscape(prefix+":"+origin.Display()) + "</i>"
		}
	}

	avatarHTML := ""
	if b.Config.Bridge.IncludeAvatarImage && msg != nil && msg.Nick != nil && msg.Nick.Avatar != "" {
		if pngBytes, err := media.AvatarPNG(msg.Nick.Avatar, media.RenderOptions{}); err == nil {
			if mxc, err := mx.upload(ctx, "avatar.png", "image/png", pngBytes); err == nil {
				w, h := 56, 56
				if cfg, _, derr := image.DecodeConfig(bytes.NewReader(pngBytes)); derr == nil && cfg.Height > 0 {
					w = cfg.Width * h / cfg.Height
					if w < 1 {
						w = 1
					}
				}
				avatarHTML = fmt.Sprintf(`<img src="%s" width="%d" height="%d" alt="avatar" />&nbsp;`, mxc, w, h)
			}
		}
	}

	var fb strings.Builder
	fb.WriteString("<blockquote>")
	fb.WriteString(avatarHTML)
	fb.WriteString(fmt.Sprintf(`<font data-mx-color="%s" color="%s"><b>%s</b></font>`, color, color, htmlEscape(name)))
	fb.WriteString(originHTML)
	if body != "" {
		fb.WriteString("<br/>")
		fb.WriteString(htmlEscape(body))
	}
	fb.WriteString("</blockquote>")

	plain := name + originPlain
	if body != "" {
		plain += ": " + body
	}
	return plain, fb.String()
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
