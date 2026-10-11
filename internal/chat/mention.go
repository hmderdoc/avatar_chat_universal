package chat

import "strings"

// MentionsName reports whether text names `name` as a whole word, with an
// optional leading @, ignoring case. It is the web chat's rule
// (ChatService.mentionsMe in future_synchro_web root/js/chat.js:
// (^|[^a-z0-9])@?NAME([^a-z0-9]|$), case-insensitive), so a message that
// lights up on the website lights up in the door too.
func MentionsName(text, name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" || len(text) < len(name) {
		return false
	}
	t := strings.ToLower(text)
	for from := 0; from <= len(t)-len(name); {
		i := strings.Index(t[from:], name)
		if i < 0 {
			return false
		}
		start := from + i
		end := start + len(name)
		if (start == 0 || !isWordByte(t[start-1])) && (end == len(t) || !isWordByte(t[end])) {
			return true
		}
		from = start + 1
	}
	return false
}

// isWordByte is [a-z0-9] after lowercasing: the characters that glue a name
// to its neighbours. Everything else (space, punctuation, @, high CP437)
// is a boundary, matching the web's [^a-z0-9].
func isWordByte(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
}
