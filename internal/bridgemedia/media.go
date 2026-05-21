// Package bridgemedia holds the platform-agnostic media handling shared by all
// chat bridges: finding media references in message text, classifying them,
// fetching bytes, reading ID3 metadata, and deriving human-friendly names and
// captions. Bridge adapters (Discord, Telegram, ...) layer their own
// presentation (embeds, media messages) on top of these primitives.
package bridgemedia

import (
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
)

// Kind classifies a media reference found in text.
type Kind int

const (
	KindNone Kind = iota
	KindImage
	KindAudio
)

// Ref is a media reference discovered in message text, after URL resolution.
type Ref struct {
	Kind  Kind
	URL   string // resolved (absolute where possible)
	Label string // markdown link label, may be empty
}

var (
	markdownURLRE = regexp.MustCompile(`!?\[([^\]]*)\]\(([^)\s]+\.(?i:png|jpe?g|gif|webp|mp3|wav|ogg|flac|m4a|aac|opus)(?:\?[^)\s]+)?)\)`)
	mediaPathRE   = regexp.MustCompile(`(?i)(?:https?://|\.?/|/)?[a-z0-9._~:/?#\[\]@!$&'()*+,;=%-]+\.(?:png|jpe?g|gif|webp|mp3|wav|ogg|flac|m4a|aac|opus)(?:\?[^ \t\r\n<>)]+)?`)
	extraSpaceRE  = regexp.MustCompile(`[ \t]{2,}`)
)

// ScanRefs finds media references in text and returns the text with those
// references removed (markdown links collapse to their label or a generic
// word; bare URLs are dropped) plus the ordered, de-duplicated refs. resolve
// maps a possibly-relative URL to an absolute one; pass nil for identity.
func ScanRefs(text string, resolve func(string) string) (string, []Ref) {
	if resolve == nil {
		resolve = func(s string) string { return s }
	}
	seen := map[string]bool{}
	var refs []Ref

	add := func(label, rawURL string) (Ref, bool) {
		rawURL = resolve(rawURL)
		if rawURL == "" || seen[rawURL] {
			return Ref{}, false
		}
		kind := Classify(rawURL)
		if kind == KindNone {
			return Ref{}, false
		}
		seen[rawURL] = true
		ref := Ref{Kind: kind, URL: rawURL, Label: strings.TrimSpace(label)}
		refs = append(refs, ref)
		return ref, true
	}

	clean := markdownURLRE.ReplaceAllStringFunc(text, func(match string) string {
		parts := markdownURLRE.FindStringSubmatch(match)
		if len(parts) != 3 {
			return match
		}
		ref, ok := add(parts[1], TrimURL(parts[2]))
		if !ok {
			return match
		}
		if ref.Label != "" {
			return ref.Label
		}
		return kindLabel(ref.Kind)
	})
	// Bare URLs we recognize are stripped so the raw link doesn't also linger.
	clean = mediaPathRE.ReplaceAllStringFunc(clean, func(match string) string {
		if _, ok := add("", TrimURL(match)); ok {
			return ""
		}
		return match
	})
	clean = extraSpaceRE.ReplaceAllString(clean, " ")
	return strings.TrimSpace(clean), refs
}

// Classify reports whether a URL points at an image, audio file, or neither.
func Classify(rawURL string) Kind {
	switch {
	case IsImageURL(rawURL):
		return KindImage
	case IsAudioURL(rawURL):
		return KindAudio
	default:
		return KindNone
	}
}

func kindLabel(k Kind) string {
	if k == KindAudio {
		return "Audio"
	}
	return "Image"
}

func IsImageURL(rawURL string) bool {
	lower := strings.ToLower(rawURL)
	for _, ext := range []string{".png", ".jpg", ".jpeg", ".gif", ".webp"} {
		if strings.Contains(lower, ext) {
			return true
		}
	}
	return false
}

func IsAudioURL(rawURL string) bool {
	lower := strings.ToLower(rawURL)
	for _, ext := range []string{".mp3", ".wav", ".ogg", ".flac", ".m4a", ".aac", ".opus"} {
		if strings.Contains(lower, ext) {
			return true
		}
	}
	return false
}

// AudioExt returns the audio extension present in rawURL (e.g. in a ?file=...
// query param), or "" if none.
func AudioExt(rawURL string) string {
	lower := strings.ToLower(rawURL)
	for _, ext := range []string{"mp3", "flac", "opus", "wav", "ogg", "m4a", "aac"} {
		if strings.Contains(lower, "."+ext) {
			return ext
		}
	}
	return ""
}

func AudioContentType(ext string) string {
	switch ext {
	case "mp3":
		return "audio/mpeg"
	case "wav":
		return "audio/wav"
	case "ogg":
		return "audio/ogg"
	case "opus":
		return "audio/opus"
	case "flac":
		return "audio/flac"
	case "m4a":
		return "audio/mp4"
	case "aac":
		return "audio/aac"
	default:
		return "application/octet-stream"
	}
}

// AudioFileName builds a human-friendly attachment name from ID3 metadata,
// falling back to the URL's basename. Returns "" if nothing usable is found.
func AudioFileName(meta Meta, rawURL, ext string) string {
	var base string
	switch {
	case meta.Title != "" && meta.Artist != "":
		base = meta.Artist + " - " + meta.Title
	case meta.Title != "":
		base = meta.Title
	default:
		base = URLBaseName(rawURL)
	}
	base = SanitizeFileName(base)
	if base == "" {
		return ""
	}
	return base + "." + ext
}

// AudioTitle picks the best display title: ID3 title, then a markdown label,
// then a prettified URL basename. Never the bare word "Audio".
func AudioTitle(meta Meta, label, rawURL string) string {
	if meta.Title != "" {
		return meta.Title
	}
	if l := strings.TrimSpace(label); l != "" {
		return l
	}
	if base := URLBaseName(rawURL); base != "" {
		return strings.TrimSpace(strings.ReplaceAll(base, "_", " "))
	}
	return ""
}

// AudioDescription joins artist and album for a caption line.
func AudioDescription(meta Meta) string {
	var parts []string
	if meta.Artist != "" {
		parts = append(parts, meta.Artist)
	}
	if meta.Album != "" {
		parts = append(parts, meta.Album)
	}
	return strings.Join(parts, " - ")
}

// URLBaseName returns the file basename (without extension) from a URL,
// checking a Synchronet-style ?file= query param first, then the path.
func URLBaseName(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	if f := u.Query().Get("file"); f != "" {
		return StripAudioExt(f)
	}
	base := path.Base(u.Path)
	if base == "/" || base == "." || base == "" {
		return ""
	}
	return StripAudioExt(base)
}

func StripAudioExt(s string) string {
	lower := strings.ToLower(s)
	for _, ext := range []string{".mp3", ".flac", ".opus", ".wav", ".ogg", ".m4a", ".aac"} {
		if strings.HasSuffix(lower, ext) {
			return s[:len(s)-len(ext)]
		}
	}
	return s
}

// SanitizeFileName keeps only characters safe for an attachment file name.
func SanitizeFileName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_' || r == '.' || r == '(' || r == ')':
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

// TrimURL strips trailing sentence punctuation that often abuts a URL in prose.
func TrimURL(s string) string {
	return strings.TrimRight(strings.TrimSpace(s), ".,;:!?")
}

// SniffImageExt detects an image type from its bytes (not a declared header,
// which BBS download endpoints get wrong) and returns a file extension and the
// content type. ok is false when the bytes are not a recognized image.
func SniffImageExt(data []byte) (ext, contentType string, ok bool) {
	contentType = http.DetectContentType(data)
	if !strings.HasPrefix(contentType, "image/") {
		return "", "", false
	}
	ext = strings.TrimPrefix(contentType, "image/")
	if i := strings.IndexByte(ext, ';'); i >= 0 {
		ext = ext[:i]
	}
	if ext == "jpeg" {
		ext = "jpg"
	}
	return ext, contentType, true
}
