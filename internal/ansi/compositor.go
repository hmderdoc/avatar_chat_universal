package ansi

import (
	"bytes"
	"fmt"
	"io"
)

// Compositor merges several Frames in z-order and emits the visible result
// to a writer with dirty-diff. Layers are evaluated top-most-first per cell:
// the first non-transparent cell wins. If every layer is transparent at a
// given position, a default opaque space is emitted so the prior screen
// content gets cleared.
//
// Use case: chat-door idle mode wants a background animation, the chat
// transcript on top of it (transparent in the gutters), and optionally a
// foreground animation overlay. Each lives in its own Frame; the Compositor
// owns the byte stream and handles the per-cell stacking.
type Compositor struct {
	// Layers are evaluated bottom-up at construction but composited
	// top-down at render time. Element 0 is the back; the last element
	// is the front.
	Layers []*Frame

	// Default cell used when every layer is transparent at a position.
	// Defaults to a black space.
	Default Cell

	// Charset selects byte encoding for cell chars; matches Frame.Charset.
	Charset Charset

	last        [][]Cell
	initialized bool
}

// NewCompositor builds a compositor for layers that share dimensions. All
// layers are expected to have the same X, Y, W, H — only layer 0's coords
// drive cursor positioning when emitting.
func NewCompositor(layers ...*Frame) *Compositor {
	return &Compositor{
		Layers:  layers,
		Default: Cell{Char: ' ', Attr: 0},
	}
}

// ForceRedraw makes the next Render emit every cell, regardless of whether
// it changed. Call after layout changes (resize, layer add/remove).
func (c *Compositor) ForceRedraw() {
	c.initialized = false
}

// composeAt walks layers top-down and returns the first opaque cell.
func (c *Compositor) composeAt(col, row int) Cell {
	for i := len(c.Layers) - 1; i >= 0; i-- {
		cell := c.Layers[i].cur[row][col]
		if !cell.Transparent {
			return cell
		}
	}
	return c.Default
}

// Render diffs the composited result against the last-emitted state and
// writes only the changed runs to w.
func (c *Compositor) Render(w io.Writer) error {
	if len(c.Layers) == 0 {
		return nil
	}
	base := c.Layers[0]
	H := base.H
	W := base.W

	if !c.initialized || len(c.last) != H {
		c.last = make([][]Cell, H)
		for i := range c.last {
			c.last[i] = make([]Cell, W)
		}
	}

	var buf bytes.Buffer
	scratch := make([]byte, 0, 4)

	for r := 0; r < H; r++ {
		// Compose this row and find the changed span.
		composed := make([]Cell, W)
		firstChange := -1
		lastChange := -1
		for col := 0; col < W; col++ {
			composed[col] = c.composeAt(col, r)
			if !c.initialized || composed[col] != c.last[r][col] {
				if firstChange < 0 {
					firstChange = col
				}
				lastChange = col
			}
		}
		if firstChange < 0 {
			continue
		}

		// Emit cursor move + cells.
		fmt.Fprintf(&buf, "\x1b[%d;%dH", base.Y+r+1, base.X+firstChange+1)
		var prevAttr Attr
		var primed bool
		for col := firstChange; col <= lastChange; col++ {
			cell := composed[col]
			if !primed || cell.Attr != prevAttr {
				buf.WriteString(cell.Attr.SGR())
				prevAttr = cell.Attr
				primed = true
			}
			buf.Write(encodeCellBytes(cell.Char, c.Charset, scratch))
		}
		copy(c.last[r], composed)
	}

	c.initialized = true
	if buf.Len() == 0 {
		return nil
	}
	_, err := w.Write(buf.Bytes())
	return err
}
