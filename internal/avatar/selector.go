package avatar

import (
	"fmt"
	"io"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
)

// SelectorAction is what the user did when the selector closed.
type SelectorAction int

const (
	// SelectorCancelled — user pressed Esc / no choice made.
	SelectorCancelled SelectorAction = iota
	// SelectorPicked — user chose an avatar; Avatar field is populated.
	SelectorPicked
	// SelectorUpload -- user wants to upload a new .bin via Zmodem.
	SelectorUpload
	// SelectorDisable — user wants to turn their avatar off.
	SelectorDisable
	// SelectorEditor — user wants to use the in-door editor (future).
	SelectorEditor
)

// Selector is a grid-based avatar picker. It auto-sizes the grid to fit
// the available frame, lets the user navigate with arrows / TAB / PgUp /
// PgDn, and offers action buttons (Upload / Disable / Editor / Cancel)
// alongside the picker so /avatar is a single command instead of several.
type Selector struct {
	Collections []*Collection

	conn  io.Writer
	input *ansi.Input
	frame *ansi.Frame

	width, height int

	Charset ansi.Charset // emitted via the underlying Frame at Run time

	collIdx int // selected collection
	avIdx   int // avatar index within selected collection

	cols, rows  int // grid dims (cells)
	cellW, cellH int

	gridX, gridY int // top-left of grid in frame coords
}

const (
	// Cell footprint includes 1-col gutter and 1-row top gutter for the
	// selection border.
	cellPadX = 2
	cellPadY = 1
)

// Charset is how the selector's frame should emit cell bytes. Set this on
// the returned *Selector before calling Run().
//
// NewSelector constructs a selector that auto-sizes its grid to (width,
// height). The grid fills as much of the available area as possible above
// the action-button footer.
func NewSelector(collections []*Collection, conn io.Writer, input *ansi.Input, width, height int) *Selector {
	if width < 60 {
		width = 60
	}
	if height < 16 {
		height = 16
	}
	cellW := Width + cellPadX  // 12
	cellH := Height + cellPadY // 7

	// Chrome rows: title (0) + nav hints (1) + separator (2) + grid + 1 row
	// at the bottom for the action-button bar. The selection summary that
	// used to sit above the action bar is gone; the title bar already
	// shows the collection name + avatar number, so it was redundant.
	const chromeTop = 3
	const chromeBottom = 1

	cols := (width - 4) / cellW
	if cols < 4 {
		cols = 4
	}
	gridArea := height - chromeTop - chromeBottom
	rows := gridArea / cellH
	if rows < 2 {
		rows = 2
	}

	gridW := cols * cellW
	gridX := (width - gridW) / 2
	if gridX < 0 {
		gridX = 0
	}
	gridY := chromeTop

	return &Selector{
		Collections: collections,
		conn:        conn,
		input:       input,
		width:       width,
		height:      height,
		cols:        cols,
		rows:        rows,
		cellW:       cellW,
		cellH:       cellH,
		gridX:       gridX,
		gridY:       gridY,
	}
}

// Run displays the selector and blocks until the user either picks an
// avatar, presses an action key (U/D/E), or cancels. Returns the chosen
// SelectorAction and (when applicable) the picked Avatar.
func (s *Selector) Run() (SelectorAction, Avatar, error) {
	if len(s.Collections) == 0 {
		return SelectorCancelled, nil, fmt.Errorf("avatar: selector has no collections")
	}
	s.frame = ansi.NewFrame(0, 0, s.width, s.height, ansi.Black|ansi.BgBlack)
	s.frame.Charset = s.Charset

	if err := ansi.HideCursor(s.conn); err != nil {
		return SelectorCancelled, nil, err
	}
	defer ansi.ShowCursor(s.conn)
	if err := ansi.ClearScreen(s.conn); err != nil {
		return SelectorCancelled, nil, err
	}

	for {
		s.draw()
		if err := s.frame.Render(s.conn); err != nil {
			return SelectorCancelled, nil, err
		}

		key, ok, err := s.input.Next(50 * time.Millisecond)
		if err != nil {
			return SelectorCancelled, nil, err
		}
		if !ok {
			continue
		}

		switch key.Type {
		case ansi.KeyEsc:
			return SelectorCancelled, nil, nil
		case ansi.KeyEnter:
			return SelectorPicked, s.Collections[s.collIdx].Avatars[s.avIdx].Clone(), nil
		case ansi.KeyLeft:
			s.move(-1, 0)
		case ansi.KeyRight:
			s.move(1, 0)
		case ansi.KeyUp:
			s.move(0, -1)
		case ansi.KeyDown:
			s.move(0, 1)
		case ansi.KeyHome:
			s.avIdx = 0
		case ansi.KeyEnd:
			s.avIdx = len(s.Collections[s.collIdx].Avatars) - 1
		case ansi.KeyPgUp:
			s.pageMove(-1)
		case ansi.KeyPgDn:
			s.pageMove(1)
		case ansi.KeyTab:
			if key.Ctrl {
				s.cycleCollection(-1)
			} else {
				s.cycleCollection(1)
			}
		case ansi.KeyChar:
			switch key.Rune {
			case 'q', 'Q':
				return SelectorCancelled, nil, nil
			case 'u', 'U':
				return SelectorUpload, nil, nil
			case 'd', 'D':
				return SelectorDisable, nil, nil
			case 'e', 'E':
				return SelectorEditor, nil, nil
			case '[', '<':
				s.cycleCollection(-1)
			case ']', '>':
				s.cycleCollection(1)
			}
		}
	}
}

// move shifts avIdx by (dx columns, dy rows) within the current collection.
// We treat the grid as a flat list of perPage entries that paginates via
// pageMove; left/right are -1/+1, up/down are -cols/+cols within a page,
// clamped so we stay in the same page.
func (s *Selector) move(dx, dy int) {
	col := s.Collections[s.collIdx]
	perPage := s.cols * s.rows
	page := s.avIdx / perPage
	posOnPage := s.avIdx % perPage

	row := posOnPage / s.cols
	colCell := posOnPage % s.cols

	colCell += dx
	row += dy

	if colCell < 0 {
		colCell = 0
	}
	if colCell >= s.cols {
		colCell = s.cols - 1
	}
	if row < 0 {
		row = 0
	}
	if row >= s.rows {
		row = s.rows - 1
	}
	candidate := page*perPage + row*s.cols + colCell
	if candidate >= len(col.Avatars) {
		candidate = len(col.Avatars) - 1
	}
	s.avIdx = candidate
}

func (s *Selector) pageMove(dir int) {
	col := s.Collections[s.collIdx]
	perPage := s.cols * s.rows
	page := s.avIdx / perPage
	posOnPage := s.avIdx % perPage

	page += dir
	if page < 0 {
		page = 0
	}
	maxPage := (len(col.Avatars) - 1) / perPage
	if page > maxPage {
		page = maxPage
	}
	candidate := page*perPage + posOnPage
	if candidate >= len(col.Avatars) {
		candidate = len(col.Avatars) - 1
	}
	s.avIdx = candidate
}

func (s *Selector) cycleCollection(dir int) {
	s.collIdx = (s.collIdx + dir + len(s.Collections)) % len(s.Collections)
	s.avIdx = 0
}

// draw paints the current page into s.frame. Each chrome row paints its
// own full-width background BEFORE writing text so a shorter collection
// name (e.g. switching from "DIGDIST.startrek" to "corporate") cannot
// leave trailing chars from the previous render visible.
func (s *Selector) draw() {
	s.frame.Clear()

	col := s.Collections[s.collIdx]
	perPage := s.cols * s.rows
	page := s.avIdx / perPage
	pageStart := page * perPage
	totalPages := (len(col.Avatars) + perPage - 1) / perPage

	titleAttr := ansi.White | ansi.BgBlue
	collAttr := ansi.Yellow | ansi.BgBlue
	hintAttr := ansi.LightGray | ansi.BgBlack

	// Row 0: title bar (full width). Combines product name + collection
	// summary so the active collection is unambiguously visible.
	fillRow(s.frame, 0, s.width, titleAttr)
	title := fmt.Sprintf(" Avatar Chat   collection: %s  (%d avatars)  page %d/%d  #%d ",
		col.Name, len(col.Avatars), page+1, totalPages, s.avIdx+1)
	if len(title) > s.width-2 {
		title = title[:s.width-2]
	}
	s.frame.PrintAt(1, 0, []byte(title), collAttr)

	// Row 1: nav hints (full width black bg). Hotkey words render in
	// WHITE; their descriptions stay in LIGHTGRAY so the keys pop.
	// NB: do not embed CP437 byte 0x1B in cell content -- it's also
	// ASCII ESC and corrupts terminal cursor state.
	fillRow(s.frame, 1, s.width, ansi.LightGray|ansi.BgBlack)
	hintSegs := []string{
		"arrows", " nav   ",
		"TAB", " collections   ",
		"ENTER", " pick   ",
		"ESC", " cancel ",
	}
	x := 1
	maxX := s.width - 1
	for j, seg := range hintSegs {
		if x >= maxX {
			break
		}
		attr := hintAttr
		if j%2 == 0 {
			attr = ansi.White | ansi.BgBlack
		}
		if x+len(seg) > maxX {
			seg = seg[:maxX-x]
		}
		s.frame.PrintAt(x, 1, []byte(seg), attr)
		x += len(seg)
	}

	// Row 2: visual separator.
	fillRow(s.frame, 2, s.width, ansi.Black|ansi.BgBlack)

	// Grid (gridY = chromeTop = 3).
	for r := 0; r < s.rows; r++ {
		for c := 0; c < s.cols; c++ {
			idx := pageStart + r*s.cols + c
			if idx >= len(col.Avatars) {
				continue
			}
			ax := s.gridX + c*s.cellW
			ay := s.gridY + r*s.cellH
			col.Avatars[idx].RenderTo(s.frame, ax, ay)
			if idx == s.avIdx {
				s.drawSelectionBorder(ax, ay)
			}
		}
	}

	// Bottom: single action row that combines the action pills (left) and
	// a "selected" summary (right). Two-row chrome at top, one at bottom.
	footerRow := s.height - 1
	fillRow(s.frame, footerRow, s.width, ansi.White|ansi.BgBlue)
	pillAttr := ansi.LightCyan | ansi.BgMagenta
	px := 1
	for _, label := range []string{" U pload ", " D isable ", " E ditor (BETA) ", " Esc cancel "} {
		if px+len(label) >= s.width-1 {
			break
		}
		s.frame.PrintAt(px, footerRow, []byte(label), pillAttr)
		px += len(label) + 1
	}
}

// fillRow paints the entire row y with ' ' in the given attribute. Used as
// a defensive background-clear before printing chrome text so leftover
// cells from a prior render (longer text, different layout) get cleanly
// overwritten even if the diff renderer would otherwise skip them.
func fillRow(f *ansi.Frame, y, width int, attr ansi.Attr) {
	for x := 0; x < width; x++ {
		f.SetCell(x, y, ' ', attr)
	}
}

// drawSelectionBorder draws a CP437 box around an avatar at (ax, ay).
func (s *Selector) drawSelectionBorder(ax, ay int) {
	const (
		corn_tl = 0xC9 // ╔
		corn_tr = 0xBB // ╗
		corn_bl = 0xC8 // ╚
		corn_br = 0xBC // ╝
		hline   = 0xCD // ═
		vline   = 0xBA // ║
	)
	border := ansi.Yellow | ansi.BgBlack
	left := ax - 1
	right := ax + Width
	top := ay - 1
	bottom := ay + Height

	s.frame.SetCell(left, top, corn_tl, border)
	s.frame.SetCell(right, top, corn_tr, border)
	s.frame.SetCell(left, bottom, corn_bl, border)
	s.frame.SetCell(right, bottom, corn_br, border)
	for x := ax; x < right; x++ {
		s.frame.SetCell(x, top, hline, border)
		s.frame.SetCell(x, bottom, hline, border)
	}
	for y := ay; y < bottom; y++ {
		s.frame.SetCell(left, y, vline, border)
		s.frame.SetCell(right, y, vline, border)
	}
}
