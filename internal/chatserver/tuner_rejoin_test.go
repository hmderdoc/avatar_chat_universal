package chatserver

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
)

// Reproduces the reported flow: a room is tuned to a TV feed; a viewer who
// switches away to another channel and then switches back should re-detect the
// tuner from the room's history and re-enter the lounge.
func TestTunerPersistsAcrossChannelSwitch(t *testing.T) {
	addr := startServer(t)
	ctx := context.Background()

	// alice tunes "rooma".
	alice := chat.NewSession(chat.NewClient(addr), &chat.Nick{Name: "alice"}, "rooma")
	if err := alice.Connect(ctx, 50); err != nil {
		t.Fatal(err)
	}
	defer alice.Close()
	if err := alice.SetTuner("127.0.0.1", 7601, "cam"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond) // let the history push land

	// bob connects fresh to the tuned room -> should detect from history.
	bob := chat.NewSession(chat.NewClient(addr), &chat.Nick{Name: "bob"}, "rooma")
	if err := bob.Connect(ctx, 50); err != nil {
		t.Fatal(err)
	}
	defer bob.Close()
	if bob.Tuner() == nil {
		t.Fatal("tuner not detected on initial connect to tuned room")
	}

	// Switch away to an untuned room.
	if err := bob.JoinChannel("roomb", 50); err != nil {
		t.Fatal(err)
	}
	if bob.Tuner() != nil {
		t.Fatalf("tuner should clear in untuned room, got %+v", bob.Tuner())
	}

	// Switch back -> the bug report says this fails to re-enable.
	if err := bob.JoinChannel("rooma", 50); err != nil {
		t.Fatal(err)
	}
	if got := bob.Tuner(); got == nil {
		t.Fatal("BUG: tuner not re-detected when switching back to the tuned room")
	} else if got.Channel != "cam" || got.Port != 7601 {
		t.Fatalf("re-detected tuner wrong: %+v", got)
	}
}

// The real-world failure: on a busy channel the tune marker scrolls out of the
// message-history window, so detection must come from the dedicated durable
// location, not message history.
func TestTunerSurvivesBusyChannel(t *testing.T) {
	addr := startServer(t)
	ctx := context.Background()

	alice := chat.NewSession(chat.NewClient(addr), &chat.Nick{Name: "alice"}, "busy")
	if err := alice.Connect(ctx, 10); err != nil {
		t.Fatal(err)
	}
	defer alice.Close()
	if err := alice.SetTuner("127.0.0.1", 7601, "cam"); err != nil {
		t.Fatal(err)
	}
	// Flood with far more messages than the history window, so the tune marker
	// is long gone from .messages history.
	for i := 0; i < 40; i++ {
		_ = alice.Send(fmt.Sprintf("chatter %d", i))
	}
	time.Sleep(150 * time.Millisecond)

	// Fresh viewer with a SMALL history window (10) — the marker is way past it.
	bob := chat.NewSession(chat.NewClient(addr), &chat.Nick{Name: "bob"}, "busy")
	if err := bob.Connect(ctx, 10); err != nil {
		t.Fatal(err)
	}
	defer bob.Close()
	if bob.Tuner() == nil {
		t.Fatal("BUG: tuner not detected on a busy channel (durable state should survive)")
	}
}
