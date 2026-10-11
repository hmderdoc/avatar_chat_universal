// Package linkprev builds link-preview cards for chat: which link in a
// message gets one, what a page says about itself (OpenGraph / twitter-card
// meta tags, with <title> and description fallbacks), and a small disk
// cache so a busy channel does not hammer remote sites.
//
// The rules are the web chat's (future_synchro_web lib/link-preview.js and
// root/js/chat-embeds.js) so a link previews the same way on both: at most
// one card per message, the first plain link; only http(s) on ports 80 and
// 443, public addresses only, re-checked on every redirect, at most three;
// the first 256 KB requested, the first 192 KB parsed; 24 hours cached on
// success, one hour on failure. The fetching and its guards are
// inlineimg.Fetcher's, shared with inline pictures.
//
// Text comes back as printable ASCII: entities decoded, control characters
// and runs of spaces collapsed, common Unicode punctuation and Latin
// letters folded to their ASCII look-alikes, anything else dropped, so a
// card is safe to put straight into CP437 cells.
package linkprev

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hmderdoc/avatar_chat_universal/internal/inlineimg"
)

// Limits from link-preview.js.
const (
	MaxURLLength   = 2048
	FetchBytes     = 256 << 10 // Range: bytes=0-262143
	ParseBytes     = 192 << 10
	FetchTimeout   = 8 * time.Second
	DefaultTTL     = 24 * time.Hour
	FailTTL        = time.Hour
	titleCap       = 200
	descriptionCap = 300
	siteNameCap    = 80
)

// Meta is one link's card. Kind is "site" for a page with metadata, or
// "image" for a link that served a picture without an image extension
// (then Title is the file name and Image the link itself).
type Meta struct {
	URL         string `json:"url"`
	FinalURL    string `json:"finalUrl"`
	Kind        string `json:"kind"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Image       string `json:"image"`
	SiteName    string `json:"siteName"`
	// Audio is the page's og:audio stream (absolute http(s)), and AudioType
	// its og:audio:type, when the page announces one: a track page whose
	// link does not itself end in .mp3 can still be played.
	Audio     string `json:"audio,omitempty"`
	AudioType string `json:"audioType,omitempty"`
	// Video is the page's direct video stream (og:video, or Twitter's
	// player:stream), only when it is typed as media or names a video
	// file; an og:video typed text/html is an embed page and is ignored.
	Video string `json:"video,omitempty"`
}

// ErrNoCard means the link has nothing worth a card: not HTML, no
// metadata, or a fetch the guards refused or that failed.
var ErrNoCard = errors.New("linkprev: no preview")

var (
	videoExt  = regexp.MustCompile(`(?i)\.(mp4|webm|ogv|m4v|mov)(?:[?#]|$)`)
	audioExt  = regexp.MustCompile(`(?i)\.(mp3|ogg|oga|m4a|flac|wav)(?:[?#]|$)`)
	ansiExt   = regexp.MustCompile(`(?i)\.(ans|asc)(?:[?#]|$)`)
	mediaPath = regexp.MustCompile(`(?i)/chatmedia/\d{4}-\d{2}-\d{2}/[a-f0-9]{24}\.[a-z0-9]{1,4}(?:[?#]|$)`)
)

// PreviewURL returns the link in text that gets a card: the first one that
// is not drawn some other way (an inline picture, a video, audio or ANSI
// file, a chat attachment). "" when there is none.
//
// File types are judged by the path alone. A page such as
// /?page=records&track=Song.mp3 ends in ".mp3" only in its query; it is an
// HTML page whose card (og:audio) is what makes it playable, so it must not
// be skipped as an audio file.
func PreviewURL(text string) string {
	for _, u := range inlineimg.FindURLs(text) {
		p := u
		if pu, err := url.Parse(u); err == nil {
			p = pu.Path
		}
		if inlineimg.IsImageURL(p) || videoExt.MatchString(p) || audioExt.MatchString(p) ||
			ansiExt.MatchString(p) || mediaPath.MatchString(u) || len(u) > MaxURLLength {
			continue
		}
		return u
	}
	return ""
}

// PageFetcher is what Get needs from inlineimg.Fetcher.
type PageFetcher interface {
	FetchPage(ctx context.Context, rawURL string, maxBytes int64) (*inlineimg.Page, error)
}

// Get returns rawURL's card, from cache when fresh. A failure is cached
// too (for FailTTL) and comes back as ErrNoCard.
func Get(ctx context.Context, f PageFetcher, c *Cache, rawURL string) (*Meta, error) {
	if m, failed, ok := c.get(rawURL); ok {
		if failed {
			return nil, ErrNoCard
		}
		return m, nil
	}
	m, err := fetch(ctx, f, rawURL)
	if errors.Is(err, context.Canceled) {
		return nil, err // the door is quitting; nothing learned about the link
	}
	c.put(rawURL, m)
	if m == nil {
		return nil, ErrNoCard
	}
	return m, nil
}

func fetch(ctx context.Context, f PageFetcher, rawURL string) (*Meta, error) {
	ctx, cancel := context.WithTimeout(ctx, FetchTimeout)
	defer cancel()
	p, err := f.FetchPage(ctx, rawURL, FetchBytes)
	if err != nil {
		return nil, err
	}
	switch {
	case strings.HasPrefix(p.ContentType, "image/"):
		return &Meta{URL: rawURL, FinalURL: p.FinalURL, Kind: "image", Title: Clean(baseName(p.FinalURL), titleCap), Image: p.FinalURL}, nil
	case strings.HasPrefix(p.ContentType, "text/html"), strings.HasPrefix(p.ContentType, "application/xhtml"):
	default:
		return nil, nil
	}
	m := Parse(p.Body, p.FinalURL)
	if m.Title == "" && m.Description == "" && m.Image == "" {
		return nil, nil
	}
	m.URL, m.FinalURL, m.Kind = rawURL, p.FinalURL, "site"
	return m, nil
}

var (
	metaTag  = regexp.MustCompile(`(?i)<meta\b[^>]*>`)
	titleTag = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	attrRE   = map[string]*regexp.Regexp{}
)

func init() {
	for _, name := range []string{"property", "name", "content"} {
		attrRE[name] = regexp.MustCompile(`(?i)\b` + name + `\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))`)
	}
}

// attr is attrFromTag: the attribute's value, quoted or bare.
func attr(tag, name string) (string, bool) {
	m := attrRE[name].FindStringSubmatchIndex(tag)
	if m == nil {
		return "", false
	}
	for g := 1; g <= 3; g++ {
		if m[2*g] >= 0 {
			return tag[m[2*g]:m[2*g+1]], true
		}
	}
	return "", false
}

// Parse pulls the card out of an HTML prefix (parseMetadata): the first
// value of each meta key wins; og: before twitter: before the plain tags.
func Parse(html []byte, baseURL string) *Meta {
	if len(html) > ParseBytes {
		html = html[:ParseBytes]
	}
	src := string(html)
	meta := map[string]string{}
	for _, tag := range metaTag.FindAllString(src, -1) {
		key, ok := attr(tag, "property")
		if !ok {
			key, ok = attr(tag, "name")
		}
		content, has := attr(tag, "content")
		if !ok || !has {
			continue
		}
		key = strings.ToLower(key)
		if _, dup := meta[key]; !dup {
			meta[key] = content
		}
	}
	first := func(keys ...string) string {
		for _, k := range keys {
			if v := meta[k]; v != "" {
				return v
			}
		}
		return ""
	}
	title := first("og:title", "twitter:title")
	if title == "" {
		if m := titleTag.FindStringSubmatch(src); m != nil {
			title = m[1]
		}
	}
	image := ""
	if raw := first("og:image", "og:image:url", "twitter:image"); raw != "" {
		// An attribute's entities are part of its value in HTML (a
		// Wikipedia og:image carries "&amp;"); link-preview.js keeps them.
		image = resolve(decodeEntities(raw), baseURL)
	}
	audio := ""
	if raw := first("og:audio:secure_url", "og:audio", "og:audio:url"); raw != "" {
		audio = resolve(decodeEntities(raw), baseURL)
	}
	video := ""
	if raw := first("og:video:secure_url", "og:video:url", "og:video"); raw != "" && mediaVideo(raw, first("og:video:type")) {
		video = resolve(decodeEntities(raw), baseURL)
	} else if raw := first("twitter:player:stream"); raw != "" && mediaVideo(raw, first("twitter:player:stream:content_type")) {
		video = resolve(decodeEntities(raw), baseURL)
	}
	return &Meta{
		Video:       video,
		Audio:       audio,
		AudioType:   strings.ToLower(strings.TrimSpace(first("og:audio:type"))),
		Title:       Clean(title, titleCap),
		Description: Clean(first("og:description", "twitter:description", "description"), descriptionCap),
		Image:       image,
		SiteName:    Clean(first("og:site_name"), siteNameCap),
	}
}

// videoFile matches a path that names a video file.
var videoFile = regexp.MustCompile(`(?i)\.(mp4|m4v|webm|mov|mkv)$`)

// mediaVideo is the shell's isDirectMediaCandidate for og:video: a stated
// type decides (video/* only), otherwise the URL's path must name a video
// file (a query ending in .mp4 is not one).
func mediaVideo(rawURL, typ string) bool {
	if t := strings.ToLower(strings.TrimSpace(typ)); t != "" {
		return strings.HasPrefix(t, "video/")
	}
	u, err := url.Parse(strings.TrimSpace(decodeEntities(rawURL)))
	return err == nil && videoFile.MatchString(u.Path)
}

// resolve is resolveUrl: absolute http(s) as is, other schemes refused,
// protocol-, root- and path-relative forms against base.
func resolve(ref, base string) string {
	ref = strings.TrimSpace(ref)
	r, err := url.Parse(ref)
	if err != nil || ref == "" {
		return ""
	}
	if r.Scheme != "" && r.Scheme != "http" && r.Scheme != "https" {
		return ""
	}
	b, err := url.Parse(base)
	if err != nil {
		return ""
	}
	u := b.ResolveReference(r)
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || len(u.String()) > MaxURLLength {
		return ""
	}
	return u.String()
}

func baseName(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return ""
	}
	return filepath.Base(strings.TrimSuffix(p.Path, "/"))
}

var entityRE = regexp.MustCompile(`(?i)&(#x[0-9a-f]+|#[0-9]+|nbsp|quot|apos|lt|gt|amp);`)

// decodeEntities decodes the entities link-preview.js decodes; others stay.
func decodeEntities(s string) string {
	return entityRE.ReplaceAllStringFunc(s, func(m string) string {
		name := strings.ToLower(m[1 : len(m)-1])
		switch name {
		case "nbsp":
			return " "
		case "quot":
			return `"`
		case "apos":
			return "'"
		case "lt":
			return "<"
		case "gt":
			return ">"
		case "amp":
			return "&"
		}
		var n int64
		var err error
		if strings.HasPrefix(name, "#x") {
			n, err = strconv.ParseInt(name[2:], 16, 32)
		} else {
			n, err = strconv.ParseInt(name[1:], 10, 32)
		}
		if err != nil || n <= 0 || n >= 0x110000 {
			return ""
		}
		return string(rune(n))
	})
}

// Clean is cleanText for a terminal: decode entities, fold to printable
// ASCII, collapse whitespace, trim, and cap at max characters with "...".
func Clean(s string, max int) string {
	s = Fold(decodeEntities(s))
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= max {
		return s
	}
	cut := strings.TrimRight(s[:max-3], " ")
	return cut + "..."
}

// fold maps Unicode punctuation and Latin letters to ASCII look-alikes.
var fold = map[rune]string{
	' ': " ", ' ': " ", ' ': " ", ' ': " ", ' ': " ", ' ': " ",
	'‘': "'", '’': "'", '‚': ",", '‛': "'", '′': "'", '´': "'",
	'“': `"`, '”': `"`, '„': `"`, '″': `"`, '«': "<<", '»': ">>",
	'‹': "<", '›': ">", '‐': "-", '‑': "-", '‒': "-", '–': "-",
	'—': "--", '―': "--", '−': "-", '…': "...", '•': "*", '·': "*",
	'‧': "*", '©': "(c)", '®': "(R)", '™': "(TM)", '°': " deg",
	'×': "x", '÷': "/", '€': "EUR", '£': "GBP", '¥': "JPY", '¢': "c",
	'½': "1/2", '¼': "1/4", '¾': "3/4", 'ß': "ss", 'æ': "ae", 'Æ': "AE",
	'œ': "oe", 'Œ': "OE", 'ø': "o", 'Ø': "O", 'ł': "l", 'Ł': "L",
	'ð': "d", 'Ð': "D", 'þ': "th", 'Þ': "Th", 'ı': "i",
}

// latin maps a base letter's accented forms (U+00C0-U+017F) to it.
var latin = map[rune]rune{}

func init() {
	groups := map[rune]string{
		'A': "ÀÁÂÃÄÅĀĂĄ", 'a': "àáâãäåāăą", 'C': "ÇĆĈĊČ", 'c': "çćĉċč", 'D': "Ď", 'd': "ď",
		'E': "ÈÉÊËĒĔĖĘĚ", 'e': "èéêëēĕėęě", 'G': "ĜĞĠĢ", 'g': "ĝğġģ", 'H': "ĤĦ", 'h': "ĥħ",
		'I': "ÌÍÎÏĨĪĬĮİ", 'i': "ìíîïĩīĭį", 'J': "Ĵ", 'j': "ĵ", 'K': "Ķ", 'k': "ķ",
		'L': "ĹĻĽĿ", 'l': "ĺļľŀ", 'N': "ÑŃŅŇ", 'n': "ñńņň", 'O': "ÒÓÔÕÖŌŎŐ", 'o': "òóôõöōŏő",
		'R': "ŔŖŘ", 'r': "ŕŗř", 'S': "ŚŜŞŠ", 's': "śŝşš", 'T': "ŢŤŦ", 't': "ţťŧ",
		'U': "ÙÚÛÜŨŪŬŮŰŲ", 'u': "ùúûüũūŭůűų", 'W': "Ŵ", 'w': "ŵ", 'Y': "ÝŶŸ", 'y': "ýÿŷ",
		'Z': "ŹŻŽ", 'z': "źżž",
	}
	for base, forms := range groups {
		for _, r := range forms {
			latin[r] = base
		}
	}
}

// Fold turns s into printable ASCII: tabs, newlines and other controls
// become spaces, known look-alikes are substituted, anything else (emoji,
// CJK, invalid UTF-8) is dropped.
func Fold(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		i += n
		switch {
		case r == utf8.RuneError && n <= 1:
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0):
			b.WriteByte(' ')
		case r < 0x7f:
			b.WriteRune(r)
		default:
			if v, ok := fold[r]; ok {
				b.WriteString(v)
			} else if v, ok := latin[r]; ok {
				b.WriteRune(v)
			}
		}
	}
	return b.String()
}

// cacheVersion is mixed into every cache file name; bump it when Meta grows.
const cacheVersion = "v3\x00"

// Cache keeps cards on disk under Dir, one JSON file per URL, trimmed
// oldest-first to Max bytes. A nil *Cache or empty Dir caches nothing.
type Cache struct {
	Dir string
	TTL time.Duration // successes; 0 = DefaultTTL
	Max int64         // 0 = 2 MB
	now func() time.Time
}

type cacheFile struct {
	Fetched int64 `json:"fetched"` // unix seconds
	Meta    *Meta `json:"meta"`    // nil = the link had no card
}

func (c *Cache) path(rawURL string) string {
	// The version prefix retires cards cached before a Meta field was
	// added (v2: og:audio, v3: og:video), so they are fetched again rather than served
	// without it for a whole TTL.
	sum := sha256.Sum256([]byte(cacheVersion + rawURL))
	return filepath.Join(c.Dir, hex.EncodeToString(sum[:16])+".json")
}

func (c *Cache) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (c *Cache) get(rawURL string) (m *Meta, failed, ok bool) {
	if c == nil || c.Dir == "" {
		return nil, false, false
	}
	b, err := os.ReadFile(c.path(rawURL))
	if err != nil {
		return nil, false, false
	}
	var cf cacheFile
	if json.Unmarshal(b, &cf) != nil {
		return nil, false, false
	}
	ttl := c.TTL
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	if cf.Meta == nil {
		ttl = FailTTL
	}
	if c.clock().Sub(time.Unix(cf.Fetched, 0)) > ttl {
		return nil, false, false
	}
	return cf.Meta, cf.Meta == nil, true
}

func (c *Cache) put(rawURL string, m *Meta) {
	if c == nil || c.Dir == "" {
		return
	}
	b, err := json.Marshal(cacheFile{Fetched: c.clock().Unix(), Meta: m})
	if err != nil || os.MkdirAll(c.Dir, 0o755) != nil {
		return
	}
	p := c.path(rawURL)
	tmp := p + ".tmp" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if os.WriteFile(tmp, b, 0o644) != nil {
		return
	}
	if os.Rename(tmp, p) != nil {
		os.Remove(tmp)
		return
	}
	max := c.Max
	if max <= 0 {
		max = 2 << 20
	}
	inlineimg.TrimDir(c.Dir, ".json", max)
}
