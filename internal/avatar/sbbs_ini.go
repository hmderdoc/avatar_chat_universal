package avatar

import "strings"

// Section-preserving edits of one section of a Synchronet user INI
// (<data>/user/NNNN.ini). That file is shared with Synchronet itself and with
// other mods (message pointers, door settings, drafts), and its own writer
// mixes "key=value" and "\tkey = value" styles, so the door never rewrites
// it whole: it reads the [avatar] section and replaces only the lines it
// owns, copying every other byte through.
//
// Lines are split on "\n" and keep any trailing "\r", so CRLF files stay CRLF.
// Section and key names match case-insensitively, as Synchronet's ini reader
// does, and the first occurrence of a key is the one read and rewritten.

// iniLine is one parsed line: a section header, a key line, or other text.
type iniLine struct {
	section string // lowercased name when this line is a [header]
	header  bool
	key     string // lowercased key on a key=value line
	prefix  string // everything up to and including the separator ("\tdata = ")
	value   string
	cr      bool // line ended "\r\n"
}

func splitINILines(text string) []string {
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	// A trailing newline yields one empty final element; drop it so joining
	// with "\n" plus the final newline round-trips.
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func parseINILine(raw string) iniLine {
	l := iniLine{}
	body := raw
	if strings.HasSuffix(body, "\r") {
		l.cr = true
		body = body[:len(body)-1]
	}
	trim := strings.TrimSpace(body)
	if strings.HasPrefix(trim, "[") && strings.HasSuffix(trim, "]") {
		l.header = true
		l.section = strings.ToLower(strings.TrimSpace(trim[1 : len(trim)-1]))
		return l
	}
	if trim == "" || trim[0] == ';' || trim[0] == '#' {
		return l
	}
	eq := strings.IndexByte(body, '=')
	if eq < 0 {
		return l
	}
	l.key = strings.ToLower(strings.TrimSpace(body[:eq]))
	// The prefix keeps the original indentation, key spelling and the
	// whitespace on both sides of '='.
	rest := body[eq+1:]
	val := strings.TrimLeft(rest, " \t")
	l.prefix = body[:eq+1] + rest[:len(rest)-len(val)]
	l.value = strings.TrimRight(val, " \t")
	return l
}

// readINISection returns the section's keys (lowercased) and values, first
// occurrence winning, and whether the section exists at all.
func readINISection(text, section string) (map[string]string, bool) {
	section = strings.ToLower(section)
	vals := map[string]string{}
	found := false
	in := false
	for _, raw := range splitINILines(text) {
		l := parseINILine(raw)
		if l.header {
			in = l.section == section
			found = found || in
			continue
		}
		if !in || l.key == "" {
			continue
		}
		if _, dup := vals[l.key]; !dup {
			vals[l.key] = l.value
		}
	}
	return vals, found
}

// iniSet is one key to write. With onlyIfAbsent the key is added when
// missing but an existing value is left alone (used for "created").
type iniSet struct {
	key          string
	value        string
	onlyIfAbsent bool
}

// writeINISection applies sets to the named section and returns the new
// file text. Existing keys are rewritten in place keeping their prefix;
// missing keys are appended after the section's last non-blank line in the
// "\tkey = value" style avatar_lib.js uses; a missing section is appended at
// the end of the file. Everything else is copied through unchanged.
func writeINISection(text, section string, sets []iniSet) string {
	section = strings.ToLower(section)
	lines := splitINILines(text)
	crlf := false
	for _, raw := range lines {
		if strings.HasSuffix(raw, "\r") {
			crlf = true
			break
		}
	}
	eol := ""
	if crlf {
		eol = "\r"
	}
	// Keep a missing final newline missing.
	final := "\n"
	if text != "" && !strings.HasSuffix(text, "\n") {
		final = ""
	}

	done := map[string]bool{}
	in := false
	start, lastBody := -1, -1 // header index; last non-blank line inside section
	for i, raw := range lines {
		l := parseINILine(raw)
		if l.header {
			in = false
			if start < 0 && l.section == section {
				in = true
				start, lastBody = i, i
			}
			continue
		}
		if !in {
			continue
		}
		if strings.TrimSpace(strings.TrimSuffix(raw, "\r")) != "" {
			lastBody = i
		}
		if l.key == "" || done[l.key] {
			continue
		}
		for _, s := range sets {
			if s.key != l.key {
				continue
			}
			done[l.key] = true
			if !s.onlyIfAbsent {
				cr := ""
				if l.cr {
					cr = "\r"
				}
				lines[i] = l.prefix + s.value + cr
			}
		}
	}

	var add []string
	for _, s := range sets {
		if !done[s.key] {
			add = append(add, "\t"+s.key+" = "+s.value+eol)
		}
	}
	if start < 0 {
		var out []string
		out = append(out, lines...)
		if len(out) > 0 && strings.TrimSpace(strings.TrimSuffix(out[len(out)-1], "\r")) != "" {
			out = append(out, eol)
		}
		out = append(out, "["+section+"]"+eol)
		out = append(out, add...)
		return strings.Join(out, "\n") + final
	}
	if len(add) > 0 {
		at := lastBody + 1
		out := make([]string, 0, len(lines)+len(add))
		out = append(out, lines[:at]...)
		out = append(out, add...)
		out = append(out, lines[at:]...)
		lines = out
	}
	return strings.Join(lines, "\n") + final
}
