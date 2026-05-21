package discordbridge

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/hmderdoc/avatar_chat_universal/internal/bitmap"
	"github.com/hmderdoc/avatar_chat_universal/internal/bridgecore"
	"github.com/hmderdoc/avatar_chat_universal/internal/bridgemedia"
	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
	"github.com/hmderdoc/avatar_chat_universal/internal/media"
)

func (b *Bridge) chatMessageToDiscord(msg *chat.Message) (*discordgo.MessageSend, bool) {
	if msg == nil || strings.TrimSpace(msg.Str) == "" {
		return nil, false
	}
	if b.ignoredChatToDiscord(msg) {
		return nil, false
	}
	if msg.Nick != nil {
		origin, ok := bridgecore.ParseHostMarker(msg.Nick.Host)
		if ok && origin.Matches(b.origin()) {
			return nil, false
		}
	}

	out := &discordgo.MessageSend{
		AllowedMentions: &discordgo.MessageAllowedMentions{Parse: []discordgo.AllowedMentionType{}},
	}

	if bitmap.IsBitmap(msg.Str) {
		if !b.Config.Bridge.IncludeBitmapImage {
			setEmbeds(out, textEmbed(msg, fmt.Sprintf("[image from %s omitted]", nickName(msg)), ""))
			return out, true
		}
		img, err := bitmap.Parse(msg.Str)
		if err != nil {
			setEmbeds(out, textEmbed(msg, fmt.Sprintf("[image from %s omitted]", nickName(msg)), ""))
			return out, true
		}
		pngBytes, err := media.BitmapPNG(img, media.RenderOptions{})
		if err != nil {
			setEmbeds(out, textEmbed(msg, fmt.Sprintf("[image from %s: %dx%d omitted]", nickName(msg), img.Width, img.Height), ""))
			return out, true
		}
		avatarFile := b.addAvatarFile(out, msg)
		embed := textEmbed(msg, fmt.Sprintf("sent an image (%dx%d)", img.Width, img.Height), attachmentURL(avatarFile))
		imageEmbed := &discordgo.MessageEmbed{
			Image: &discordgo.MessageEmbedImage{URL: "attachment://avatar-chat-image.png"},
			Color: embedColor,
		}
		setEmbeds(out, embed, imageEmbed)
		out.Files = append(out.Files, &discordgo.File{
			Name:        "avatar-chat-image.png",
			ContentType: "image/png",
			Reader:      bytes.NewReader(pngBytes),
		})
		return out, true
	}

	text := stripChatControls(msg.Str)
	displayText, mediaEmbeds := b.mediaEmbedsFromText(out, text)
	var main *discordgo.MessageEmbed
	if msg.Nick == nil || msg.Nick.Name == "" {
		main = &discordgo.MessageEmbed{Description: displayText, Color: embedColor}
	} else {
		avatarFile := b.addAvatarFile(out, msg)
		main = textEmbed(msg, displayText, attachmentURL(avatarFile))
	}
	embeds := consolidateEmbeds(append([]*discordgo.MessageEmbed{main}, mediaEmbeds...))
	setEmbeds(out, embeds...)
	return out, true
}

// consolidateEmbeds folds a bare author/avatar header (no text of its own) into
// a lone media embed, so a dropped track or image renders as one rich card --
// author + title/metadata + full image -- instead of a separate header block
// followed by the media embed.
func consolidateEmbeds(embeds []*discordgo.MessageEmbed) []*discordgo.MessageEmbed {
	if len(embeds) != 2 {
		return embeds
	}
	main, media := embeds[0], embeds[1]
	if main == nil || media == nil || main.Title != "" || main.Description != "" || media.Author != nil {
		return embeds
	}
	media.Author = main.Author
	if media.Thumbnail == nil {
		media.Thumbnail = main.Thumbnail
	}
	return []*discordgo.MessageEmbed{media}
}

func (b *Bridge) ignoredChatToDiscord(msg *chat.Message) bool {
	if b == nil || b.Config == nil || len(b.Config.Bridge.ChatToDiscordIgnore) == 0 {
		return false
	}
	text := ""
	if msg != nil {
		text = msg.Str
		if msg.Nick != nil && msg.Nick.Name != "" {
			text = msg.Nick.Name + ": " + text
		}
	}
	for _, pattern := range b.Config.Bridge.ChatToDiscordIgnore {
		if pattern == "" {
			continue
		}
		if ok, err := regexp.MatchString(pattern, text); err == nil && ok {
			return true
		}
	}
	return false
}

func (b *Bridge) addAvatarFile(out *discordgo.MessageSend, msg *chat.Message) string {
	if b == nil || b.Config == nil || !b.Config.Bridge.IncludeAvatarImage || msg == nil || msg.Nick == nil || msg.Nick.Avatar == "" {
		return ""
	}
	pngBytes, err := media.AvatarPNG(msg.Nick.Avatar, media.RenderOptions{})
	if err != nil {
		return ""
	}
	name := safeFilePart(msg.Nick.Name) + "-avatar.png"
	out.Files = append(out.Files, &discordgo.File{
		Name:        name,
		ContentType: "image/png",
		Reader:      bytes.NewReader(pngBytes),
	})
	return name
}

func attachmentURL(name string) string {
	if name == "" {
		return ""
	}
	return "attachment://" + name
}

func (b *Bridge) mediaEmbedsFromText(out *discordgo.MessageSend, text string) (string, []*discordgo.MessageEmbed) {
	clean, refs := bridgemedia.ScanRefs(text, b.publicMediaURL)
	var embeds []*discordgo.MessageEmbed
	for _, ref := range refs {
		switch ref.Kind {
		case bridgemedia.KindImage:
			embeds = append(embeds, b.imageEmbed(out, ref))
		case bridgemedia.KindAudio:
			embeds = b.appendAudioEmbed(out, embeds, ref)
		}
	}
	return clean, embeds
}

func (b *Bridge) imageEmbed(out *discordgo.MessageSend, ref bridgemedia.Ref) *discordgo.MessageEmbed {
	title := ref.Label
	if title == "" {
		title = "Image"
	}
	embed := &discordgo.MessageEmbed{Title: title, URL: ref.URL, Color: embedColor}
	// Discord only inlines embed.Image.URL for an image/* content-type. BBS
	// downloads come back as application/octet-stream, which the proxy refuses
	// to render, so re-upload the bytes as a native attachment; fall back to the
	// remote URL if the fetch fails or it isn't actually an image.
	if name, ok := b.uploadImage(out, ref.URL); ok {
		embed.Image = &discordgo.MessageEmbedImage{URL: attachmentURL(name)}
	} else {
		embed.Image = &discordgo.MessageEmbedImage{URL: ref.URL}
	}
	return embed
}

func (b *Bridge) appendAudioEmbed(out *discordgo.MessageSend, embeds []*discordgo.MessageEmbed, ref bridgemedia.Ref) []*discordgo.MessageEmbed {
	// Discord renders a native player only for uploaded file attachments, and
	// embeds have no audio field -- so re-upload the bytes as a standalone
	// player attachment and add a caption embed built from ID3 metadata
	// (title/artist + cover art as a full image). Consolidation later folds in
	// the author header. On fetch failure, fall back to a plain link embed.
	meta, artName, ok := b.uploadAudio(out, ref.URL)
	if !ok {
		title := ref.Label
		if title == "" {
			title = "Audio"
		}
		return append(embeds, &discordgo.MessageEmbed{
			Title:       title,
			URL:         ref.URL,
			Description: ref.URL,
			Color:       embedColor,
		})
	}
	embed := &discordgo.MessageEmbed{URL: ref.URL, Color: embedColor}
	if title := bridgemedia.AudioTitle(meta, ref.Label, ref.URL); title != "" {
		embed.Title = title
	}
	if desc := bridgemedia.AudioDescription(meta); desc != "" {
		embed.Description = desc
	}
	if artName != "" {
		embed.Image = &discordgo.MessageEmbedImage{URL: attachmentURL(artName)}
	}
	// Only emit the caption embed if it carries something; the standalone player
	// plus the consolidated author header is enough otherwise.
	if embed.Title != "" || embed.Description != "" || embed.Image != nil {
		return append(embeds, embed)
	}
	return embeds
}

// uploadImage fetches a remote image and attaches its bytes to the outgoing
// Discord message so the embed renders regardless of the source's content-type.
// It returns the attachment file name and true on success; on any failure the
// caller should fall back to embedding the remote URL directly.
func (b *Bridge) uploadImage(out *discordgo.MessageSend, rawURL string) (string, bool) {
	if b == nil || b.fetchMedia == nil || out == nil {
		return "", false
	}
	data, err := b.fetchMedia(rawURL)
	if err != nil || len(data) == 0 {
		if b.Logger != nil && err != nil {
			b.Logger.Printf("bridge: image re-upload fetch failed for %s: %v", rawURL, err)
		}
		return "", false
	}
	ext, ctype, ok := bridgemedia.SniffImageExt(data)
	if !ok {
		return "", false
	}
	name := fmt.Sprintf("media-%d.%s", len(out.Files), ext)
	out.Files = append(out.Files, &discordgo.File{
		Name:        name,
		ContentType: ctype,
		Reader:      bytes.NewReader(data),
	})
	return name, true
}

// uploadAudio fetches a remote audio file and attaches it to the outgoing
// message as a standalone attachment so Discord renders its native player. It
// reads ID3 metadata to name the file (Artist - Title.mp3 rather than
// media-N.mp3) and, if cover art is present, attaches it too -- artName is
// returned so the caller can show it as the embed image. ok is true once the
// audio attaches; the caller should fall back to a link embed otherwise.
func (b *Bridge) uploadAudio(out *discordgo.MessageSend, rawURL string) (meta bridgemedia.Meta, artName string, ok bool) {
	if b == nil || b.fetchMedia == nil || out == nil {
		return meta, "", false
	}
	ext := bridgemedia.AudioExt(rawURL)
	if ext == "" {
		return meta, "", false
	}
	data, err := b.fetchMedia(rawURL)
	if err != nil || len(data) == 0 {
		if b.Logger != nil && err != nil {
			b.Logger.Printf("bridge: audio re-upload fetch failed for %s: %v", rawURL, err)
		}
		return meta, "", false
	}
	meta, _ = bridgemedia.ParseID3(data)
	name := bridgemedia.AudioFileName(meta, rawURL, ext)
	if name == "" {
		name = fmt.Sprintf("media-%d.%s", len(out.Files), ext)
	}
	out.Files = append(out.Files, &discordgo.File{
		Name:        name,
		ContentType: bridgemedia.AudioContentType(ext),
		Reader:      bytes.NewReader(data),
	})
	if len(meta.Art) > 0 {
		if iext, ctype, sniffed := bridgemedia.SniffImageExt(meta.Art); sniffed {
			artName = fmt.Sprintf("media-%d.%s", len(out.Files), iext)
			out.Files = append(out.Files, &discordgo.File{
				Name:        artName,
				ContentType: ctype,
				Reader:      bytes.NewReader(meta.Art),
			})
		}
	}
	return meta, artName, true
}

func (b *Bridge) discordMessageToChat(m *discordgo.MessageCreate) (*chat.Message, bool) {
	if m == nil || m.Author == nil || m.Author.Bot {
		return nil, false
	}
	if m.ChannelID != b.Config.Discord.ChannelID {
		return nil, false
	}

	text := strings.TrimSpace(m.Content)
	if b.Config.Bridge.IncludeAttachmentURL {
		for _, a := range m.Attachments {
			if a == nil || a.URL == "" {
				continue
			}
			label := strings.TrimSpace(a.Filename)
			if label == "" {
				label = "attachment"
			}
			if text != "" {
				text += "\n"
			}
			text += label + ": " + a.URL
		}
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, false
	}

	return &chat.Message{
		Nick: &chat.Nick{
			Name: displayName(m),
			Host: bridgecore.FormatHostMarker(b.origin()),
		},
		Str:  sanitizeText(text),
		Time: nowMs(),
	}, true
}

func displayName(m *discordgo.MessageCreate) string {
	if m.Member != nil && strings.TrimSpace(m.Member.Nick) != "" {
		return strings.TrimSpace(m.Member.Nick)
	}
	if m.Author.GlobalName != "" {
		return m.Author.GlobalName
	}
	return m.Author.Username
}

func nickName(msg *chat.Message) string {
	if msg != nil && msg.Nick != nil && msg.Nick.Name != "" {
		return msg.Nick.Name
	}
	return "unknown"
}

const embedColor = 0x21c7d9

func textEmbed(msg *chat.Message, text, thumbnail string) *discordgo.MessageEmbed {
	embed := &discordgo.MessageEmbed{
		Author:      &discordgo.MessageEmbedAuthor{Name: authorName(msg)},
		Description: text,
		Color:       embedColor,
	}
	if thumbnail != "" {
		embed.Thumbnail = &discordgo.MessageEmbedThumbnail{URL: thumbnail}
	}
	return embed
}

func authorName(msg *chat.Message) string {
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
	return name + " -- " + host
}

func setEmbeds(out *discordgo.MessageSend, embeds ...*discordgo.MessageEmbed) {
	filtered := embeds[:0]
	for _, embed := range embeds {
		if embed == nil {
			continue
		}
		if embed.Description == "" && embed.Author == nil && embed.Title == "" && embed.URL == "" && embed.Image == nil && embed.Thumbnail == nil {
			continue
		}
		filtered = append(filtered, embed)
	}
	if len(filtered) == 0 {
		return
	}
	out.Embeds = filtered
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

func sanitizeText(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\f', '\r', '\b', 0x07, 0x14, 0x15, 0x10:
			continue
		case '\n':
			b.WriteString(" / ")
		default:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}
