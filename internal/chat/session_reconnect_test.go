package chat_test

import (
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
)

// proxy relays TCP to addr; cut drops every relayed connection, the way a
// chat service restart looks to a client, while the server keeps its state.
type proxy struct {
	addr  string
	mu    sync.Mutex
	conns []net.Conn
}

func startProxy(t *testing.T, to string) *proxy {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	p := &proxy{addr: l.Addr().String()}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			up, err := net.Dial("tcp", to)
			if err != nil {
				c.Close()
				continue
			}
			p.mu.Lock()
			p.conns = append(p.conns, c, up)
			p.mu.Unlock()
			go func() { io.Copy(up, c); up.Close() }()
			go func() { io.Copy(c, up); c.Close() }()
		}
	}()
	return p
}

func (p *proxy) cut() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.conns {
		c.Close()
	}
	p.conns = nil
}

func TestSession_ServerDropIsNoticedAndResumeCatchesUp(t *testing.T) {
	addr := startServer(t)
	p := startProxy(t, addr)
	door := connect(t, p.addr, "alice", "main")
	bob := connect(t, addr, "bob", "main")

	if err := bob.Send("before the drop"); err != nil {
		t.Fatal(err)
	}
	if !cycleUntil(door, func() bool { return transcriptHas(door, "before the drop") }) {
		t.Fatal("live message not delivered")
	}

	lostAt := time.Now()
	p.cut()
	deadline := time.Now().Add(2 * time.Second)
	for door.IsAlive() && time.Now().Before(deadline) {
		door.Cycle()
		time.Sleep(10 * time.Millisecond)
	}
	if door.IsAlive() {
		t.Fatal("IsAlive still true after the server hung up")
	}
	if err := door.Send("into the void"); err == nil {
		t.Fatal("send on a dead connection reported success")
	}

	if err := bob.Send("while you were out"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)

	if err := door.Redial(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := door.Resume(lostAt); err != nil {
		t.Fatal(err)
	}
	if !transcriptHas(door, "while you were out") {
		t.Fatal("missed message not caught up")
	}
	n := 0
	for _, m := range door.Messages() {
		if m.Nick != nil && m.Str == "before the drop" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("message seen before the drop shown %d times", n)
	}

	// Live delivery works again after the resume.
	if err := bob.Send("after"); err != nil {
		t.Fatal(err)
	}
	if !cycleUntil(door, func() bool { return transcriptHas(door, "after") }) {
		t.Fatal("no live delivery after reconnect")
	}
}

// A PM sent while the recipient had no door running shows when they come
// back, once, and not again after they've seen it.
func TestSession_LoadMailboxShowsPMsSentWhileAway(t *testing.T) {
	addr := startServer(t)
	alice := connect(t, addr, "alice", "main")
	before := time.Now().UnixMilli() - 1
	if err := alice.SendPrivate("bob", "ping me when you're back"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	bob := connect(t, addr, "bob", "main")
	if n := bob.LoadMailbox(before); n != 1 {
		t.Fatalf("LoadMailbox shown %d, want 1", n)
	}
	var got *chat.Message
	for _, m := range bob.Messages() {
		if m.Nick != nil && m.Str == "ping me when you're back" {
			got = m
		}
	}
	if got == nil || !got.Private {
		t.Fatalf("PM not shown as private: %+v", got)
	}
	if bob.LastReplyTarget() != "alice" {
		t.Fatalf("/r target = %q", bob.LastReplyTarget())
	}
	if n := bob.LoadMailbox(time.Now().UnixMilli()); n != 0 {
		t.Fatalf("already-seen PMs shown again: %d", n)
	}
}

// A query on a connection the server dropped returns an error instead of
// spinning on the closed reply channel (which would also block Redial).
func TestSession_QueryAfterDropReturns(t *testing.T) {
	addr := startServer(t)
	p := startProxy(t, addr)
	door := connect(t, p.addr, "alice", "main")
	p.cut()
	for i := 0; i < 200 && door.IsAlive(); i++ {
		time.Sleep(5 * time.Millisecond)
	}
	done := make(chan struct{})
	go func() { door.Who(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Who hung on a dropped connection")
	}
	if err := door.Redial(context.Background()); err != nil {
		t.Fatal(err)
	}
}
