package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/hmderdoc/avatar_chat_universal/internal/apcaudio"
)

// nbConn accepts at most n bytes per non-blocking write, like a full socket.
type nbConn struct {
	bytes.Buffer
	n int
}

func (c *nbConn) WriteNB(p []byte) (int, error) {
	if len(p) > c.n {
		p = p[:c.n]
	}
	return c.Buffer.Write(p)
}

// Audio goes out only between frames: a frame still in flight finishes
// first, so an APC can never splice into one of its escape sequences.
func TestPaceFrameNeverSplicesAudioIntoAFrame(t *testing.T) {
	c := &nbConn{n: 3}
	a := &App{Conn: c}
	a.pending = []byte("\x1b[1;1HFRAME")
	a.audioOut.WriteString("<APC>")
	for i := 0; i < 10; i++ {
		if err := a.paceFrame(); err != nil {
			t.Fatal(err)
		}
	}
	if got := c.String(); got != "\x1b[1;1HFRAME<APC>" {
		t.Fatalf("wire order = %q", got)
	}
}

// Leaving the lounge silences the channel at once.
func TestSilenceOnExitFinishesFrameThenFlushes(t *testing.T) {
	var out bytes.Buffer
	a := &App{Conn: &out}
	a.tvAudio = apcaudio.NewSession(apcaudio.Defaults(2, "tv"))
	_ = a.tvAudio.Start(&a.audioOut)
	a.pending = []byte("FRAME")
	a.pendingOff = 2
	a.silenceOnExit()
	got := out.String()
	if !strings.HasPrefix(got, "AME") || !strings.Contains(got, "A;Flush;C=2") || strings.Contains(got, "Volume") {
		t.Fatalf("exit wrote %q", got)
	}
}
