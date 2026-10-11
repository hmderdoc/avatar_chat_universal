package webchat

// PageStyle is one look the page can wear: the class ac-style-<ID> from
// the Synchronet page's stylesheet, which the standalone page shares.
type PageStyle struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// PageStyles mirrors PAGE_STYLES in web/synchronet/src/lib/webprefs.ts,
// in its order (a test keeps the two the same).
var PageStyles = []PageStyle{
	{"classic", "Classic"},
	{"cga", "CGA"},
	{"modern-dark", "Modern dark"},
	{"modern-light", "Modern light"},
	{"amber", "Amber phosphor"},
	{"green", "Green phosphor"},
	{"win95", "Windows 95"},
	{"compact", "Flat compact"},
}

// DefaultPageStyle is webprefs.ts DEFAULT_PAGE_STYLE.
const DefaultPageStyle = "classic"

// offeredStyles is web_styles (a list of ids) or all of them, in
// PageStyles order; never empty.
func offeredStyles(allow []string) []PageStyle {
	var out []PageStyle
	for _, s := range PageStyles {
		if len(allow) == 0 || contains(allow, s.ID) {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return append([]PageStyle(nil), PageStyles...)
	}
	return out
}

// defaultStyle is web_default_style when offered, else Classic when
// offered, else the first offered one.
func defaultStyle(wanted string, offered []PageStyle) string {
	for _, want := range []string{wanted, DefaultPageStyle} {
		for _, s := range offered {
			if s.ID == want {
				return want
			}
		}
	}
	return offered[0].ID
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
