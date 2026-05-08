package chatserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
)

// startServer spins up a server on a random localhost port and returns its
// address. It cancels via t.Cleanup.
func startServer(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close() // we just wanted a free port

	srv := New(addr)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = srv.ListenAndServe(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
		}
	})

	// Wait briefly for listener to come up.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.Dial("tcp", addr)
		if err == nil {
			c.Close()
			return addr
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server did not start on %s", addr)
	return ""
}

func TestSubscribeWriteFanOut(t *testing.T) {
	addr := startServer(t)

	// Client A subscribes; client B writes; A should receive an UPDATE.
	a := chat.NewClient(addr)
	a.Nick = "alice"
	if err := a.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	b := chat.NewClient(addr)
	b.Nick = "bob"
	if err := b.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	if err := a.Subscribe("chat", "channels.test.messages"); err != nil {
		t.Fatal(err)
	}
	// Give A's subscribe time to register before B's WRITE.
	time.Sleep(50 * time.Millisecond)

	type payload struct {
		Hello string `json:"hello"`
	}
	if err := b.Write("chat", "channels.test.messages", payload{Hello: "world"}, chat.LockWrite); err != nil {
		t.Fatal(err)
	}

	select {
	case upd, ok := <-a.Updates():
		if !ok {
			t.Fatal("updates channel closed unexpectedly")
		}
		if upd.Func != "UPDATE" {
			t.Errorf("func = %q, want UPDATE", upd.Func)
		}
		if upd.Oper != "WRITE" {
			t.Errorf("oper = %q, want WRITE", upd.Oper)
		}
		if upd.Location != "channels.test.messages" {
			t.Errorf("location = %q", upd.Location)
		}
		var got payload
		if err := json.Unmarshal(upd.Data, &got); err != nil {
			t.Fatalf("unmarshal data: %v; raw %s", err, upd.Data)
		}
		if got.Hello != "world" {
			t.Errorf("got %+v, want hello=world", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("did not receive update from B's WRITE")
	}
}

func TestPushSliceRoundTrip(t *testing.T) {
	addr := startServer(t)
	c := chat.NewClient(addr)
	if err := c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	for i := 0; i < 5; i++ {
		if err := c.Push("chat", "channels.test.history", map[string]int{"i": i}, chat.LockWrite); err != nil {
			t.Fatal(err)
		}
	}

	var got []map[string]int
	if err := c.Slice("chat", "channels.test.history", -3, nil, chat.LockRead, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d items, want 3: %+v", len(got), got)
	}
	for i, m := range got {
		want := i + 2 // last 3 of 0..4
		if m["i"] != want {
			t.Errorf("got[%d].i = %d, want %d", i, m["i"], want)
		}
	}
}

func TestPingPong(t *testing.T) {
	addr := startServer(t)
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// Send a SOCKET-scope PING; expect PONG echoed back.
	ts := time.Now().UnixNano() / 1000000
	body := fmt.Sprintf(`{"scope":"SOCKET","func":"PING","data":%d}`+"\r\n", ts)
	if _, err := c.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 256)
	n, err := c.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := string(buf[:n])
	wantSubstr := `"func":"PONG"`
	if !contains(got, wantSubstr) {
		t.Errorf("expected PONG, got: %s", got)
	}
}

func contains(s, sub string) bool {
	return len(sub) <= len(s) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
