// avatar_chat_server smoke test: two clients connect to a running server,
// subscribe to a channel, exchange messages, and verify everything lands.
// Usage: go run ./cmd/avatar_chat_server/smoke -addr 127.0.0.1:11088
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:11088", "server address to test")
	flag.Parse()

	if err := run(*addr); err != nil {
		fmt.Fprintln(os.Stderr, "smoke test FAILED:", err)
		os.Exit(1)
	}
	fmt.Println("smoke test PASSED")
}

func run(addr string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	alice := chat.NewClient(addr)
	bob := chat.NewClient(addr)
	if err := alice.Connect(ctx); err != nil {
		return fmt.Errorf("alice connect: %v", err)
	}
	defer alice.Close()
	if err := bob.Connect(ctx); err != nil {
		return fmt.Errorf("bob connect: %v", err)
	}
	defer bob.Close()
	log.Println("both clients connected")

	channel := "smoke-" + fmt.Sprint(time.Now().UnixNano())
	loc := "channels." + channel + ".messages"

	// Both subscribe.
	if err := alice.Subscribe("chat", loc); err != nil {
		return fmt.Errorf("alice subscribe: %v", err)
	}
	if err := bob.Subscribe("chat", loc); err != nil {
		return fmt.Errorf("bob subscribe: %v", err)
	}
	// Subscriptions are fire-and-forget; give the server a beat to ack.
	time.Sleep(50 * time.Millisecond)

	// Alice writes; Bob should receive an update.
	msg := chat.Message{
		Nick: &chat.Nick{Name: "alice", Host: "Smoke BBS"},
		Str:  "hello bob",
		Time: time.Now().UnixNano() / 1000000,
	}
	bobUpdates := bob.Updates()
	if err := alice.Write("chat", loc, msg, 2); err != nil {
		return fmt.Errorf("alice write: %v", err)
	}
	log.Println("alice wrote message; waiting for bob to see it...")

	select {
	case pkt, ok := <-bobUpdates:
		if !ok {
			return fmt.Errorf("bob updates channel closed before message arrived")
		}
		log.Printf("bob received packet: oper=%s loc=%s", pkt.Oper, pkt.Location)
		if pkt.Location != loc {
			return fmt.Errorf("bob got update for %q, want %q", pkt.Location, loc)
		}
	case <-time.After(3 * time.Second):
		return fmt.Errorf("bob did not receive alice's message within 3s")
	}

	// Push to history; Slice it back.
	histLoc := "channels." + channel + ".history"
	if err := alice.Push("chat", histLoc, msg, 2); err != nil {
		return fmt.Errorf("alice push history: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	var got []chat.Message
	if err := alice.Slice("chat", histLoc, 0, nil, 2, &got); err != nil {
		return fmt.Errorf("alice slice history: %v", err)
	}
	if len(got) != 1 || got[0].Str != "hello bob" {
		return fmt.Errorf("history slice: got %+v", got)
	}
	log.Printf("history slice OK: %d messages", len(got))

	// Bob writes back; Alice should receive.
	aliceUpdates := alice.Updates()
	reply := chat.Message{
		Nick: &chat.Nick{Name: "bob", Host: "Smoke BBS"},
		Str:  "hi alice",
		Time: time.Now().UnixNano() / 1000000,
	}
	if err := bob.Write("chat", loc, reply, 2); err != nil {
		return fmt.Errorf("bob write: %v", err)
	}
	select {
	case pkt, ok := <-aliceUpdates:
		if !ok {
			return fmt.Errorf("alice updates channel closed before reply arrived")
		}
		log.Printf("alice received reply: oper=%s loc=%s", pkt.Oper, pkt.Location)
	case <-time.After(3 * time.Second):
		return fmt.Errorf("alice did not receive bob's reply within 3s")
	}

	// WHO lookup -- should show both clients.
	who, err := alice.Who("chat", loc)
	if err != nil {
		return fmt.Errorf("who: %v", err)
	}
	log.Printf("who returned %d entries: %+v", len(who), who)
	if len(who) < 2 {
		return fmt.Errorf("who: expected >=2 entries, got %d", len(who))
	}

	return nil
}
