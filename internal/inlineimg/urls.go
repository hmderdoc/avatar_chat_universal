// Package inlineimg turns image links in chat messages into pictures a BBS
// terminal can show: a guarded HTTP fetch with an on-disk cache, decoding
// (PNG, JPEG, GIF first frame, WebP, BMP), and three renderers picked by
// what the caller's terminal can do — DEC sixel, 24-bit half blocks, or 16
// colours through shadecell's per-cell shade matcher.
//
// The rules follow the futureland shell's chat (fshell_ts) so a link shows
// the same way in both: links are recognised by their extension, a picture
// is at most 10 rows by the bubble's width, and a keyword veil hides
// pictures whose URL or line looks NSFW until the user reveals them.
package inlineimg

import (
	"net/url"
	"regexp"
	"strings"
)

var (
	// urlPattern is the shell's URL_PATTERN (chat_model.ts): http(s) up to
	// whitespace, quotes, angle brackets, pipes or a CTRL-A colour code.
	urlPattern = regexp.MustCompile(`(?i)https?://[^\s<>"'\x01|]+`)
	// imageExt is the shell's IMAGE_EXT_RE: the path (not just anywhere in
	// the string) ends in an image extension, before any query/fragment.
	imageExt = regexp.MustCompile(`(?i)\.(png|jpe?g|gif|webp|bmp)(?:[?#]|$)`)
	trailing = regexp.MustCompile(`[).,;:!?]+$`)
)

// MaxPerMessage caps how many pictures one message can carry inline.
const MaxPerMessage = 3

// FindImageURLs returns the image links in text, in order, without
// duplicates, at most MaxPerMessage.
func FindImageURLs(text string) []string {
	if !strings.Contains(text, "://") {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, m := range urlPattern.FindAllString(text, -1) {
		u := trailing.ReplaceAllString(m, "")
		if u == "" || seen[u] || !imageExt.MatchString(u) {
			continue
		}
		if p, err := url.Parse(u); err != nil || p.Host == "" {
			continue
		}
		seen[u] = true
		out = append(out, u)
		if len(out) == MaxPerMessage {
			break
		}
	}
	return out
}

// DefaultNSFWKeywords is the shell's default list (social_lib.js
// NSFW_DEFAULT_KEYWORDS), used when the sysop sets no nsfw_keywords.
var DefaultNSFWKeywords = []string{
	"nude", "nudes", "nudity", "naked", "nsfw", "topless", "sex", "sexy", "sexual", "porn", "porno",
	"xxx", "erotic", "erotica", "boobs", "breasts", "tits", "nipples", "nipple", "genitals", "penis", "vagina",
	"pussy", "dick", "cock", "orgy", "lingerie", "thong", "stripper", "striptease", "undressed", "undressing",
}

// LooksNSFW applies the shell's keyword rule (social_lib.js nsfwText):
// percent-decode, split on anything that is not a letter or digit, and
// report whether any whole word equals a keyword, ignoring case.
func LooksNSFW(text string, keywords []string) bool {
	if len(keywords) == 0 {
		return false
	}
	if dec, err := url.PathUnescape(text); err == nil {
		text = dec
	}
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
	for _, w := range words {
		for _, k := range keywords {
			if w == strings.ToLower(k) {
				return true
			}
		}
	}
	return false
}
