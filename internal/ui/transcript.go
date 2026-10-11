package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
	"github.com/hmderdoc/avatar_chat_universal/internal/avatar"
	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
	"github.com/hmderdoc/avatar_chat_universal/internal/inlineimg"
	"github.com/hmderdoc/avatar_chat_universal/internal/linkprev"
	"github.com/hmderdoc/avatar_chat_universal/internal/nickstyle"
	"github.com/hmderdoc/avatar_chat_universal/internal/theme"
)

// Transcript renders the chat scrollback. It mirrors the layout of the JS
// avatar_chat door (renderBubble at /sbbs/xtrn/avatar_chat/avatar_chat.js
// :1510-1535): consecutive messages from the same sender are grouped into
// one bubble that shares a single avatar in the left or right gutter, and
// the visible window is bottom-aligned with 1-row gaps between bubbles.
type Transcript struct {
	Frame *ansi.Frame

	// Scroll is how many rows the view sits above the newest line. 0 sticks
	// to the latest. Rows, not bubbles: a bubble holding a picture is taller
	// than a page, and paging by bubbles skipped it. Render clamps it to the
	// oldest row.
	Scroll int

	SelfName string

	// Theme drives bubble + header colors. Required; the App fills this
	// from its loaded theme.
	Theme *theme.Theme

	// Truecolor paints handle styles in 24-bit; otherwise each colour is
	// mapped to its nearest of the 16. The App sets it from the probe.
	Truecolor bool

	// Images, when set, turns image links into inline pictures inside the
	// bubble (nil = links stay text only).
	Images *imageManager
	// Cards, when set, adds a preview card under a message's first plain
	// link (nil = no cards).
	Cards *cardManager
	// AudioLabel, when set, adds a one-line card under each audio link;
	// it returns "" for a link that is not one (nil = audio links stay
	// text only).
	AudioLabel func(url string) string
	// SixelRegions lists, after Render, the fully visible pictures that
	// carry a sixel version, in frame coordinates, for the App to blit
	// over their cells.
	SixelRegions []sixelRegion
}

// sixelRegion is a picture's cell box in the transcript frame.
type sixelRegion struct {
	X, Y int
	Pic  *inlineimg.Picture
	URL  string
}

func NewTranscript(f *ansi.Frame, selfName string) *Transcript {
	return &Transcript{Frame: f, SelfName: selfName, Theme: theme.Default()}
}

const (
	// Gutter math from JS avatar_chat.js:1510-1535. Left avatars at column 0,
	// bubble starts at avatarW+3 = 13. Right avatars at frame.W-10, bubble
	// ends at avatarX-2.
	avatarW       = avatar.Width  // 10
	avatarH       = avatar.Height // 6
	avatarMargin  = 3             // cells between avatar and bubble
	bubbleMaxLine = 54            // cap on text width inside a bubble
	bubbleMinLine = 14            // floor on bubble width
	groupGap      = 1             // blank rows between two bubbles
)

// block is one rendered "bubble" (one sender's group of messages) plus
// optionally a notice. height is the number of rows it occupies.
type block struct {
	notice  bool
	private bool // PM — render with red bubble background
	left    bool // own message → right; others → left
	speaker string
	style   *nickstyle.Style // speaker's handle style (nil = theme colour)
	stamp   string
	host    string   // BBS / system name from Nick.Host, rendered after stamp
	rows    []string // wrapped lines of text
	// pics are inline pictures; each owns blank rows of the bubble starting
	// at its row index.
	pics []picSlot
	// cards are link-preview cards, laid out like pics over blank rows.
	cards []cardSlot
	// mention[i] marks rows[i] as part of a message from someone else that
	// says our name (chat.MentionsName); those rows get the mention colours
	// and a bar down the left edge. nil when no row is a mention.
	mention []bool
	avatar  avatar.Avatar
	width   int // bubble inner width (without spaces)

	// Effect (set when the first row begins with \x02<code>):
	//   'j' = join glow, sweeps left-to-right in green palette
	//   'l' = leave glow, sweeps right-to-left in red palette
	effect      byte
	effectStart int64 // msg.Time copied here so the renderer can compute elapsed
}

// height returns the rendered height of this block. Bubbles are at least as
// tall as the avatar (so the avatar doesn't visually clip), so height is
// max(avatarH, header+rows). Notices are 1 row per wrapped line.
func (b *block) height() int {
	if b.notice {
		if len(b.rows) == 0 {
			return 1
		}
		return len(b.rows)
	}
	textRows := 1 + len(b.rows) // 1 header + body
	if textRows < avatarH {
		return avatarH
	}
	return textRows
}

// Render paints the visible bubbles into the transcript frame, bottom-aligned.
func (t *Transcript) Render(messages []*chat.Message) {
	t.Frame.Clear()
	t.SixelRegions = t.SixelRegions[:0]
	if len(messages) == 0 {
		return
	}

	bubbleW := t.bubbleWidth()
	blocks := t.buildBlocks(messages, bubbleW)

	// Lay every block out on one tall strip, then show the Frame.H rows that
	// end Scroll rows above its bottom. Blocks cut by the top or bottom edge
	// are drawn clipped (the frame drops off-screen cells).
	tops := make([]int, len(blocks))
	total := 0
	for i, b := range blocks {
		if i > 0 {
			total += groupGap
		}
		tops[i] = total
		total += b.height()
	}
	if oldest := total - t.Frame.H; t.Scroll > oldest {
		t.Scroll = oldest
	}
	if t.Scroll < 0 {
		t.Scroll = 0
	}
	// Strip row shown at frame row 0. A short transcript stays
	// bottom-aligned, so this goes negative.
	top := total - t.Scroll - t.Frame.H
	for i, b := range blocks {
		y := tops[i] - top
		if y >= t.Frame.H || y+b.height() <= 0 {
			continue
		}
		t.renderBlock(b, y)
	}
}

// PageRows is how far one PgUp/PgDn moves: a page less two rows of overlap,
// so the eye keeps its place.
func (t *Transcript) PageRows() int {
	if n := t.Frame.H - 2; n > 0 {
		return n
	}
	return 1
}

// bubbleWidth returns the body width of a bubble (the space the wrapped
// text occupies, before the surrounding 1-cell padding spaces).
func (t *Transcript) bubbleWidth() int {
	w := t.Frame.W - avatarW - avatarMargin - 2 - 1
	if w > bubbleMaxLine {
		w = bubbleMaxLine
	}
	if w < bubbleMinLine {
		w = bubbleMinLine
	}
	return w
}

// buildBlocks turns the message list into renderable blocks, grouping
// consecutive same-sender messages.
func (t *Transcript) buildBlocks(messages []*chat.Message, bubbleW int) []*block {
	out := make([]*block, 0, len(messages))
	for _, msg := range messages {
		if msg == nil {
			continue
		}

		// Notice / system / /me action: nick is nil or empty.
		if msg.Nick == nil || msg.Nick.Name == "" {
			text := msg.Str
			b := &block{notice: true, effectStart: msg.Time}
			// Detect leading \x02<code> marker for sweep animations.
			if len(text) >= 2 && text[0] == 0x02 {
				b.effect = text[1]
				text = text[2:]
			}
			b.rows = wrapText(text, t.Frame.W-2)
			out = append(out, b)
			continue
		}

		left := !strings.EqualFold(msg.Nick.Name, t.SelfName)
		stamp := relativeStamp(msg.Time)

		// Coalesce with previous block if same sender, same side, AND same
		// private/public class (don't merge a PM with a public message).
		if n := len(out); n > 0 && !out[n-1].notice && out[n-1].left == left && out[n-1].private == msg.Private && strings.EqualFold(out[n-1].speaker, msg.Nick.Name) {
			prev := out[n-1]
			prev.stamp = stamp
			prev.style = msg.Nick.Style
			prev.addRows([]string{""}, false)
			prev.addRows(wrapText(msg.Str, bubbleW), left && chat.MentionsName(msg.Str, t.SelfName))
			t.addPictures(prev, msg)
			t.addCard(prev, msg)
			t.addAudio(prev, msg)
			continue
		}

		b := &block{
			left:    left,
			private: msg.Private,
			speaker: msg.Nick.Name,
			style:   msg.Nick.Style,
			stamp:   stamp,
			host:    msg.Nick.Host,
			width:   bubbleW,
		}
		b.addRows(wrapText(msg.Str, bubbleW), left && chat.MentionsName(msg.Str, t.SelfName))
		t.addPictures(b, msg)
		t.addCard(b, msg)
		t.addAudio(b, msg)
		if msg.Nick.Avatar != "" {
			if a, err := avatar.FromBase64(msg.Nick.Avatar); err == nil {
				b.avatar = a
			}
		}
		if b.avatar == nil {
			// No avatar attached -- generate a deterministic identicon so
			// the gutter isn't blank. Mirrors the JS door's behavior at
			// /sbbs/repo/exec/load/avatar_lib.js:224-230.
			b.avatar = avatar.Identicon(msg.Nick.Name)
		}
		out = append(out, b)
	}
	return out
}

// addRows appends wrapped lines to the bubble, flagging them as a mention.
func (b *block) addRows(rows []string, mention bool) {
	if mention && b.mention == nil {
		b.mention = make([]bool, len(b.rows), len(b.rows)+len(rows))
	}
	b.rows = append(b.rows, rows...)
	if b.mention != nil {
		for range rows {
			b.mention = append(b.mention, mention)
		}
	}
}

// picSlot is one inline picture (or its veil) inside a bubble.
type picSlot struct {
	row    int // index into block.rows of its first row
	url    string
	pic    *inlineimg.Picture
	veiled bool
	cols   int
	rows   int
}

const (
	veilRows = 4
	veilCols = 40
)

// addPictures reserves bubble rows under msg's text for each image link in
// it: the picture once downloaded, a one-line placeholder while it loads,
// a shaded veil while the NSFW rule hides it, nothing if it failed.
func (t *Transcript) addPictures(b *block, msg *chat.Message) {
	if t.Images == nil {
		return
	}
	for _, u := range inlineimg.FindImageURLs(msg.Str) {
		v, ok := t.Images.lookup(u, msg.Str, msg.Nick.Name, b.width, t.Truecolor)
		if !ok {
			continue
		}
		switch {
		case v.pending:
			b.addRows([]string{"[loading image...]"}, false)
		case v.veiled:
			slot := picSlot{row: len(b.rows), url: u, veiled: true, cols: minInt(veilCols, b.width), rows: veilRows}
			b.addRows(make([]string, veilRows), false)
			b.pics = append(b.pics, slot)
		case v.pic != nil:
			slot := picSlot{row: len(b.rows), url: u, pic: v.pic, cols: v.pic.Cols, rows: v.pic.Rows}
			b.addRows(make([]string, v.pic.Rows), false)
			b.pics = append(b.pics, slot)
		}
	}
}

// paintPictures draws a bubble's pictures with their top-left at the
// bubble's text column, and records the sixel ones that are wholly inside
// the frame.
func (t *Transcript) paintPictures(b *block, textX, bodyY int) {
	for _, s := range b.pics {
		y0 := bodyY + s.row
		if s.veiled {
			paintVeil(t.Frame, textX, y0, s.cols, s.rows)
			continue
		}
		for r := 0; r < s.pic.Rows; r++ {
			y := y0 + r
			if y < 0 || y >= t.Frame.H {
				continue
			}
			for c := 0; c < s.pic.Cols; c++ {
				cell := s.pic.Cells[r*s.pic.Cols+c]
				if cell.True {
					t.Frame.SetCellTrue(textX+c, y, cell.Char, cell.Fg, cell.Bg)
				} else {
					t.Frame.SetCell(textX+c, y, cell.Char, cell.Attr)
				}
			}
		}
		if s.pic.Sixel != nil && y0 >= 0 && y0+s.pic.Rows <= t.Frame.H {
			t.SixelRegions = append(t.SixelRegions, sixelRegion{X: textX, Y: y0, Pic: s.pic, URL: s.url})
		}
	}
}

// cardSlot is one link-preview card inside a bubble.
type cardSlot struct {
	row    int // index into block.rows of its first row
	layout cardLayout
}

// addAudio adds a one-line card under each audio link in msg.
func (t *Transcript) addAudio(b *block, msg *chat.Message) {
	if t.AudioLabel == nil {
		return
	}
	for _, u := range inlineimg.FindURLs(msg.Str) {
		label := t.AudioLabel(u)
		if label == "" {
			continue
		}
		if len(label) > b.width {
			label = label[:b.width]
		}
		b.addRows([]string{label}, false)
	}
}

// addCard reserves bubble rows under msg's text for the preview card of
// its first plain link: a loading line, the card, a veiled card, or
// nothing when the link has no card.
func (t *Transcript) addCard(b *block, msg *chat.Message) {
	if t.Cards == nil {
		return
	}
	u := linkprev.PreviewURL(msg.Str)
	if u == "" {
		return
	}
	v, ok := t.Cards.lookup(u, msg.Str, t.Truecolor)
	if !ok {
		return
	}
	l := layoutCard(v, b.width)
	if l.rows == 0 {
		return
	}
	b.cards = append(b.cards, cardSlot{row: len(b.rows), layout: l})
	b.addRows(make([]string, l.rows), false)
}

// paintCards draws a bubble's cards at the bubble's text column: an edge
// bar, the thumbnail, then the site, title and description on the
// bubble's own background.
func (t *Transcript) paintCards(b *block, textX, bodyY int, bubbleAttr ansi.Attr) {
	th := t.Theme
	if th == nil {
		th = theme.Default()
	}
	bg := bubbleAttr &^ 0x0F
	for _, s := range b.cards {
		l := s.layout
		y0 := bodyY + s.row
		for r := 0; r < l.rows; r++ {
			t.Frame.SetCell(textX, y0+r, 0xDD, bg|(th.CardEdge&0x0F)) // ▌
		}
		x := textX + 1
		if p := l.thumb; p != nil {
			for r := 0; r < p.Rows; r++ {
				for c := 0; c < p.Cols; c++ {
					cell := p.Cells[r*p.Cols+c]
					if cell.True {
						t.Frame.SetCellTrue(x+c, y0+r, cell.Char, cell.Fg, cell.Bg)
					} else {
						t.Frame.SetCell(x+c, y0+r, cell.Char, cell.Attr)
					}
				}
			}
			x += p.Cols + 1
		}
		for i, ln := range l.lines {
			attr := bubbleAttr
			switch ln.role {
			case 's':
				attr = bg | (th.CardSite & 0x0F)
			case 't':
				attr = bg | (th.CardTitle & 0x0F)
			case 'v':
				attr = ansi.White | ansi.BgMagenta
			}
			t.Frame.PrintAt(x, y0+i, []byte(ln.text), attr)
		}
	}
}

// paintVeil fills a box with shade noise and a reveal hint, the shell's
// NSFW veil: frame cells only, so it needs no fetch.
func paintVeil(f *ansi.Frame, x, y, cols, rows int) {
	shades := [3]byte{0xB0, 0xB1, 0xB2}
	seed := uint32(y*31 + x)
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			seed = seed*1103515245 + 12345
			pick := (seed >> 8) % 3
			attr := ansi.DarkGray | ansi.BgBlack
			if pick == 1 {
				attr = ansi.LightGray | ansi.BgBlack
			}
			f.SetCell(x+c, y+r, shades[pick], attr)
		}
	}
	label := " NSFW - /nsfw to show "
	if cols >= len(label) && rows >= 2 {
		f.PrintAt(x+(cols-len(label))/2, y+rows/2, []byte(label), ansi.White|ansi.BgMagenta)
	}
}

// mentionEdge is CP437 ▌ (left half block): painted in the theme's
// MentionEdge colour over the bubble's own background, it draws a bar down
// the left of a mention without changing the bubble's width.
const mentionEdge = 0xDD

// renderBlock paints a single block at row y in the transcript frame.
func (t *Transcript) renderBlock(b *block, y int) {
	if b.notice {
		t.renderNotice(b, y)
		return
	}

	// Avatar gutter coordinates.
	var avatarX, bubbleX int
	if b.left {
		avatarX = 0
		bubbleX = avatarW + avatarMargin
	} else {
		avatarX = t.Frame.W - avatarW
		bubbleX = avatarX - avatarMargin - b.width - 2 // 2 = surrounding pad spaces
		if bubbleX < 0 {
			bubbleX = 0
		}
	}

	// Header: " speaker  HH:MM  bbsname"
	th := t.Theme
	if th == nil {
		th = theme.Default()
	}
	speakerAttr := th.SpeakerLeft
	timeAttr := th.Timestamp
	hostAttr := th.Host
	if !b.left {
		speakerAttr = th.SpeakerSelf
	}
	headerW := paintHandle(t.Frame, bubbleX, y, b.speaker, b.style, speakerAttr, t.Frame.W-bubbleX, t.Truecolor)
	stampX := bubbleX + headerW + 1
	t.Frame.PrintAt(stampX, y, []byte(b.stamp), timeAttr)
	if b.host != "" {
		hostX := stampX + len(b.stamp) + 2
		// Stay inside the bubble's right edge (b.width + 2 pad spaces),
		// with an ellipsis if we have to chop the host name.
		bubbleEnd := bubbleX + b.width + 2
		if bubbleEnd > t.Frame.W {
			bubbleEnd = t.Frame.W
		}
		avail := bubbleEnd - hostX
		if avail > 0 {
			h := b.host
			if len(h) > avail {
				if avail >= 3 {
					h = h[:avail-3] + "..."
				} else {
					h = h[:avail]
				}
			}
			t.Frame.PrintAt(hostX, y, []byte(h), hostAttr)
		}
	}

	// Body rows
	bubbleAttr := th.BubbleLeft
	if !b.left {
		bubbleAttr = th.BubbleSelf
	}
	// PM override -- private bubble color regardless of side.
	if b.private {
		bubbleAttr = th.BubblePrivate
	}
	for i, line := range b.rows {
		row := y + 1 + i
		if row >= t.Frame.H {
			break
		}
		attr := bubbleAttr
		hl := i < len(b.mention) && b.mention[i]
		if hl && !b.private {
			attr = th.BubbleMention
		}
		padded := " " + padTo(line, b.width) + " "
		t.Frame.PrintAt(bubbleX, row, []byte(padded), attr)
		if hl {
			t.Frame.SetCell(bubbleX, row, mentionEdge, (attr&^0x0F)|(th.MentionEdge&0x0F))
		}
	}
	t.paintPictures(b, bubbleX+1, y+1)
	t.paintCards(b, bubbleX+1, y+1, bubbleAttr)

	// Avatar (drawn last so it's on top of any overlap). It stays beside its
	// own header: a bubble cut by the frame edge loses the off-screen avatar
	// rows rather than pushing the avatar over its neighbour.
	if b.avatar != nil {
		b.avatar.RenderTo(t.Frame, avatarX, y)
	}
}

// renderNotice paints a notice line. Three modes:
//
//   1. block.effect == 'j' or 'l' AND animation still active: render the
//      line with a sweeping color wave (green L→R for join, red R→L for
//      leave). After the animation window expires, falls through to (3).
//   2. Plain notice with embedded \x01<code> color escapes: parse them.
//   3. Plain notice in default dark-gray.
func (t *Transcript) renderNotice(b *block, y int) {
	defaultAttr := ansi.DarkGray
	if t.Theme != nil {
		defaultAttr = t.Theme.NoticeDefault
	}
	const animDurationMs = 1800
	if b.effect != 0 && b.effectStart > 0 {
		elapsed := time.Now().UnixNano() / 1000000 - b.effectStart
		if elapsed < animDurationMs {
			t.renderSweepNotice(b, y, b.effect, elapsed, animDurationMs)
			return
		}
	}
	// Track attr across line wraps. wrapText splits at word boundaries
	// without carrying forward the active color, so a name that wraps
	// (e.g. "Disk McHardy" in the user list) would otherwise have its
	// second half rendered in defaultAttr instead of the active color.
	attr := defaultAttr
	for i, line := range b.rows {
		row := y + i
		if row >= t.Frame.H {
			break
		}
		col := 0
		t.Frame.SetCell(col, row, '*', defaultAttr)
		col++
		t.Frame.SetCell(col, row, ' ', defaultAttr)
		col++
		k := 0
		for k < len(line) && col < t.Frame.W {
			c := line[k]
			if c == 0x01 && k+1 < len(line) {
				attr = decodeColorCode(line[k+1], defaultAttr)
				k += 2
				continue
			}
			t.Frame.SetCell(col, row, c, attr)
			col++
			k++
		}
	}
}

// renderSweepNotice draws a one-line notice with a moving color wave.
// effect 'j' = green L→R; 'l' = red R→L. The wave's center walks across
// the line over animDurationMs; cells near the center are bright (white →
// light variant → base variant), cells far away fall to dark gray.
func (t *Transcript) renderSweepNotice(b *block, y int, effect byte, elapsedMs, durationMs int64) {
	if len(b.rows) == 0 {
		return
	}
	line := "* " + b.rows[0]
	maxCol := t.Frame.W
	if len(line) > maxCol {
		line = line[:maxCol]
	}
	progress := float64(elapsedMs) / float64(durationMs)
	if progress < 0 {
		progress = 0
	}
	if progress > 1 {
		progress = 1
	}
	// Wave travels from -lead to len(line)+lead, so it enters and exits
	// off-screen for a smoother arrival/departure.
	const lead = 4
	span := float64(len(line) + lead*2)
	var waveX float64
	switch effect {
	case 'l':
		// Right-to-left.
		waveX = float64(len(line)+lead) - progress*span
	default:
		// Left-to-right (join, default).
		waveX = -float64(lead) + progress*span
	}

	row := y
	if row >= t.Frame.H {
		return
	}
	for col := 0; col < len(line); col++ {
		distance := float64(col) - waveX
		if effect == 'l' {
			distance = -distance
		}
		attr := sweepColor(effect, distance)
		t.Frame.SetCell(col, row, line[col], attr)
	}
}

// sweepColor returns the per-cell attribute given the signed distance
// from the wave's leading edge. Negative distance = wave hasn't reached
// here yet; positive = already passed. Brightness is symmetric around 0.
func sweepColor(effect byte, distance float64) ansi.Attr {
	abs := distance
	if abs < 0 {
		abs = -abs
	}
	bright, mid, dim := ansi.LightGreen, ansi.Green, ansi.LightGray
	if effect == 'l' {
		bright, mid, dim = ansi.LightRed, ansi.Red, ansi.LightGray
	}
	switch {
	case abs < 1:
		return ansi.White
	case abs < 3:
		return bright
	case abs < 5:
		return mid
	case abs < 7:
		return dim
	default:
		return ansi.DarkGray
	}
}

// decodeColorCode maps a single-letter code (after \x01) to an ansi.Attr.
// Lowercase = regular CGA color; uppercase = bright/light variant.
// Unknown codes fall back to the supplied default.
//
//	n  default notice color           N  white (alias)
//	r  red               R  light red
//	g  green             G  light green
//	b  blue              B  light blue
//	c  cyan              C  light cyan
//	m  magenta           M  light magenta
//	y  brown (dark yel)  Y  yellow
//	w  light gray        W  white
//	d  dark gray         (no bright counterpart)
func decodeColorCode(c byte, def ansi.Attr) ansi.Attr {
	switch c {
	case 'n':
		return def
	case 'N', 'W':
		return ansi.White
	case 'w':
		return ansi.LightGray
	case 'r':
		return ansi.Red
	case 'R':
		return ansi.LightRed
	case 'g':
		return ansi.Green
	case 'G':
		return ansi.LightGreen
	case 'b':
		return ansi.Blue
	case 'B':
		return ansi.LightBlue
	case 'c':
		return ansi.Cyan
	case 'C':
		return ansi.LightCyan
	case 'm':
		return ansi.Magenta
	case 'M':
		return ansi.LightMagenta
	case 'y':
		return ansi.Brown
	case 'Y':
		return ansi.Yellow
	case 'd', 'D':
		return ansi.DarkGray
	}
	return def
}

// relativeStamp formats an epoch-ms message time as a friendly relative
// string ("just now", "5 min ago", "2 hr ago", "yesterday HH:MM"). Older
// messages fall back to "MM/DD HH:MM". Recomputed every render, so the
// label updates as time elapses (matching avatar_chat.js's strategy at
// avatar_chat.js:120-148).
func relativeStamp(epochMs int64) string {
	t := time.Unix(0, (epochMs) * 1000000)
	delta := time.Since(t)
	if delta < 0 {
		delta = 0
	}
	switch {
	case delta < 30*time.Second:
		return "just now"
	case delta < time.Hour:
		m := int(delta / time.Minute)
		if m < 1 {
			m = 1
		}
		return fmt.Sprintf("%dm ago", m)
	case delta < 24*time.Hour:
		h := int(delta / time.Hour)
		mm := int(delta/time.Minute) % 60
		if mm == 0 {
			return fmt.Sprintf("%dh ago", h)
		}
		return fmt.Sprintf("%dh %dm ago", h, mm)
	case delta < 7*24*time.Hour:
		d := int(delta / (24 * time.Hour))
		if d == 1 {
			return "yesterday " + t.Format("15:04")
		}
		return fmt.Sprintf("%dd ago", d)
	default:
		return t.Format("01/02 15:04")
	}
}

// wrapText breaks s into lines of at most width chars, preferring to split
// on the last space within the last 12 chars before width.
func wrapText(s string, width int) []string {
	if width < 1 {
		width = 1
	}
	if len(s) <= width {
		return []string{s}
	}
	var out []string
	for len(s) > width {
		split := width
		for i := width; i > width-12 && i > 0; i-- {
			if s[i-1] == ' ' {
				split = i
				break
			}
		}
		out = append(out, strings.TrimRight(s[:split], " "))
		s = strings.TrimLeft(s[split:], " ")
	}
	if s != "" {
		out = append(out, s)
	}
	return out
}

func padTo(s string, w int) string {
	if len(s) >= w {
		return s[:w]
	}
	return s + strings.Repeat(" ", w-len(s))
}

