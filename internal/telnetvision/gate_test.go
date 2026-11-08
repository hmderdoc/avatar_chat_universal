package telnetvision

import (
	"testing"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
)

func solid(v byte) *Frame {
	px := make([]byte, 2*3)
	for i := range px {
		px[i] = v
	}
	return &Frame{Cols: 1, Rows: 1, Pixels: px}
}

func TestGateHoldsNoiseAndRepaintsDrift(t *testing.T) {
	dst := ansi.NewFrame(0, 0, 1, 1, 0)
	g := &Gate{Threshold: 10}
	opts := RenderOpts{Truecolor: true, Gate: g}
	solid(100).RenderTo(dst, 0, 0, 1, 1, opts)
	first := dst.CellAt(0, 0)

	// Within the threshold of what is shown: the shown cell stays.
	solid(108).RenderTo(dst, 0, 0, 1, 1, opts)
	if dst.CellAt(0, 0) != first {
		t.Fatal("noise within the threshold repainted the cell")
	}
	// Compared with what is shown (100), not the last frame (108): 111 repaints.
	solid(111).RenderTo(dst, 0, 0, 1, 1, opts)
	if got := dst.CellAt(0, 0); got == first || got.Fg.R != 111 {
		t.Fatalf("drift past the threshold kept %+v", got)
	}
	// Switching colour depth forgets the gate.
	opts.Truecolor = false
	solid(111).RenderTo(dst, 0, 0, 1, 1, opts)
	if dst.CellAt(0, 0).True {
		t.Fatal("16-colour render reused a truecolor cell")
	}
}
