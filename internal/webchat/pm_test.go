package webchat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
)

// claimAs claims nick with key and returns the CLAIM reply's data.
func claimAs(t *testing.T, ws *websocket.Conn, nick, key string) string {
	t.Helper()
	put(t, ws, `{"scope":"SOCKET","func":"CLAIM","data":{"nick":"`+nick+`","key":"`+key+`"}}`)
	return string(next(t, ws, "CLAIM", "").Data)
}

// refusedWS sends s and expects an ERROR containing want.
func refusedWS(t *testing.T, ws *websocket.Conn, s, want string) {
	t.Helper()
	put(t, ws, s)
	if p := next(t, ws, "ERROR", ""); !strings.Contains(string(p.Data), want) {
		t.Fatalf("%s: %s (want %q)", s, p.Data, want)
	}
}

// noWrite fails if a WRITE update reaches ws before a PING round trip
// made after d (a read timeout would break the socket).
func noWrite(t *testing.T, ws *websocket.Conn, d time.Duration, why string) {
	t.Helper()
	time.Sleep(d)
	put(t, ws, `{"scope":"SOCKET","func":"PING","data":"nowrite"}`)
	_ = ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		_, data, err := ws.ReadMessage()
		if err != nil {
			t.Fatalf("%s: %v", why, err)
		}
		var p packet
		if json.Unmarshal(data, &p) != nil {
			continue
		}
		if p.Func == "UPDATE" && p.Oper == "WRITE" {
			t.Fatalf("%s: got %s", why, data)
		}
		if p.Func == "PONG" {
			return
		}
	}
}

// sliceUntil reads loc's history until has(data) or 3s pass (a door's
// PUSH gets no reply, so it may land after the browser asks).
func sliceUntil(t *testing.T, ws *websocket.Conn, loc string, has func(string) bool) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		put(t, ws, `{"scope":"chat","func":"QUERY","oper":"SLICE","location":"`+loc+`","data":{"start":-10}}`)
		data := string(next(t, ws, "RESPONSE", "SLICE").Data)
		if has(data) || time.Now().After(deadline) {
			return data
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func eachMode(t *testing.T, fn func(t *testing.T, relay bool)) {
	for _, relay := range []bool{false, true} {
		t.Run(map[bool]string{false: "local", true: "relay"}[relay], func(t *testing.T) { fn(t, relay) })
	}
}

// Spec test 2: a name belongs to the browser holding its key; another
// browser with that name is refused and can't read its mailbox.
func TestNameClaims(t *testing.T) {
	eachMode(t, func(t *testing.T, relay bool) {
		file := filepath.Join(t.TempDir(), "claims.txt")
		f := startFront(t, relay, func(c *Config) { c.ClaimsFile = file; c.ClaimsPerIPDay = 3 }, nil)
		keyA := strings.Repeat("a", 40)
		a := dialWS(t, f.ws, nil)
		if r := claimAs(t, a, "Alice", keyA); !strings.Contains(r, `"ok":true`) {
			t.Fatalf("first claim: %s", r)
		}
		// The same key gets the name back, in any case.
		a2 := dialWS(t, f.ws, nil)
		if r := claimAs(t, a2, "alice", keyA); !strings.Contains(r, `"ok":true`) {
			t.Fatalf("reclaim: %s", r)
		}
		// Another browser: refused, and once it has its own name it still
		// can't read Alice's mailbox.
		m := dialWS(t, f.ws, nil)
		if r := claimAs(t, m, "ALICE", strings.Repeat("m", 40)); !strings.Contains(r, `"reason":"taken"`) {
			t.Fatalf("second browser: %s", r)
		}
		if r := claimAs(t, m, "mallory", strings.Repeat("m", 40)); !strings.Contains(r, `"ok":true`) {
			t.Fatalf("mallory: %s", r)
		}
		box := chat.MailboxFor("Alice", "testbbs")
		refusedWS(t, m, `{"scope":"chat","func":"QUERY","oper":"SUBSCRIBE","location":"channels.`+box+`.messages"}`, "no public room")
		refusedWS(t, m, `{"scope":"chat","func":"QUERY","oper":"SLICE","location":"channels.`+box+`.history","data":{"start":-50}}`, "no public room")

		// The file holds one line per name, no key in clear; a fresh
		// server reading it still refuses the wrong key, and deleting the
		// line frees the name.
		body, _ := os.ReadFile(file)
		if strings.Contains(string(body), keyA) || !strings.Contains(string(body), hashKey(keyA)+" alice") {
			t.Fatalf("claims file: %q", body)
		}
		c2 := newClaims(file, 3, time.Now)
		if c2.claim("alice", strings.Repeat("m", 40), "9.9.9.9") != claimTaken {
			t.Fatal("reloaded claims let the wrong key in")
		}
		var kept []string
		for _, line := range strings.Split(string(body), "\n") {
			if !strings.HasSuffix(line, " alice") {
				kept = append(kept, line)
			}
		}
		time.Sleep(10 * time.Millisecond) // a new mtime
		os.WriteFile(file, []byte(strings.Join(kept, "\n")), 0o600)
		if c2.claim("alice", strings.Repeat("m", 40), "9.9.9.9") != claimOK {
			t.Fatal("a deleted line didn't free the name")
		}

		// New names per address per day are capped (3 here: alice and
		// mallory are two).
		x := dialWS(t, f.ws, nil)
		if r := claimAs(t, x, "third", strings.Repeat("x", 40)); !strings.Contains(r, `"ok":true`) {
			t.Fatalf("third: %s", r)
		}
		y := dialWS(t, f.ws, nil)
		if r := claimAs(t, y, "fourth", strings.Repeat("y", 40)); !strings.Contains(r, `"reason":"limit"`) {
			t.Fatalf("fourth: %s", r)
		}
	})
}

// Spec test 3: a plain-name mailbox is never readable from the page, even
// for a browser holding that very name: live, from history, or by WHO.
func TestPlainMailboxNeverReadable(t *testing.T) {
	eachMode(t, func(t *testing.T, relay bool) {
		f := startFront(t, relay, nil, nil)
		ws := dialWS(t, f.ws, nil)
		subscribe(t, ws, "Hm Derdoc")
		next(t, ws, "RESPONSE", "SUBSCRIBE")
		for _, op := range []string{
			`{"scope":"chat","func":"QUERY","oper":"SUBSCRIBE","location":"channels.Hm Derdoc.messages"}`,
			`{"scope":"chat","func":"QUERY","oper":"SLICE","location":"channels.Hm Derdoc.history","data":{"start":-10}}`,
			`{"scope":"chat","func":"QUERY","oper":"WHO","location":"channels.Hm Derdoc.messages"}`,
			`{"scope":"chat","func":"QUERY","oper":"READ","location":"channels.Hm Derdoc.history"}`,
		} {
			put(t, ws, op)
			p := next(t, ws, "ERROR", "")
			if !strings.Contains(string(p.Data), "not allowed") && !strings.Contains(string(p.Data), "no public room") {
				t.Fatalf("%s: %s", op, p.Data)
			}
		}
		// A door PMs the BBS user Hm Derdoc; the web "Hm Derdoc" sees
		// nothing, live, and its own mailbox history is empty.
		d := door(t, f.tcp, "dora")
		msg := map[string]interface{}{"nick": map[string]string{"name": "dora", "host": "doorbbs"}, "str": "for the real one"}
		if err := d.Write("chat", "channels.Hm Derdoc.messages", msg, chat.LockWrite); err != nil {
			t.Fatal(err)
		}
		_ = d.Push("chat", "channels.Hm Derdoc.history", msg, chat.LockWrite)
		noWrite(t, ws, 500*time.Millisecond, "the web Hm Derdoc got a BBS user's PM")
		if _, err := d.Who("chat", mainLoc); err != nil { // the door's push is in
			t.Fatal(err)
		}
		if data := sliceUntil(t, ws, "channels."+chat.MailboxFor("Hm Derdoc", "testbbs")+".history", func(string) bool { return true }); strings.Contains(data, "for the real one") {
			t.Fatalf("own mailbox history has the BBS user's PM: %s", data)
		}
	})
}

// A door (internal/chat Client, as the terminal door uses it) PMs a web
// user at their mailbox and the browser gets it live and in history; the
// web user's reply reaches the door, stamped with the mailbox to answer.
func TestDoorAndWebPM(t *testing.T) {
	eachMode(t, func(t *testing.T, relay bool) {
		f := startFront(t, relay, nil, nil)
		ws := dialWS(t, f.ws, nil)
		subscribe(t, ws, "alice")
		next(t, ws, "RESPONSE", "SUBSCRIBE")
		box := chat.MailboxFor("alice", "testbbs")
		put(t, ws, `{"scope":"chat","func":"QUERY","oper":"SUBSCRIBE","location":"channels.`+box+`.messages"}`)
		next(t, ws, "RESPONSE", "SUBSCRIBE")

		d := door(t, f.tcp, "dora")
		if err := d.Subscribe("chat", "channels.dora.messages"); err != nil {
			t.Fatal(err)
		}
		// A round trip on the door's connection: its subscriptions are in
		// before the browser writes.
		if _, err := d.Who("chat", mainLoc); err != nil {
			t.Fatal(err)
		}
		// The door learns where to send from a message alice wrote.
		put(t, ws, `{"scope":"chat","func":"QUERY","oper":"WRITE","location":"`+mainLoc+`","data":{"nick":{"mailbox":"Hm Derdoc"},"str":"anyone?"}}`)
		var seen struct {
			Nick struct{ Name, Host, Mailbox string } `json:"nick"`
		}
		json.Unmarshal(update(t, d, "WRITE").Data, &seen)
		if seen.Nick.Mailbox != box {
			t.Fatalf("stamped mailbox %q, want %q (a forged one must be replaced)", seen.Nick.Mailbox, box)
		}
		target := chat.PMTarget(seen.Nick.Name, seen.Nick.Host, seen.Nick.Mailbox)
		pm := map[string]interface{}{"nick": map[string]string{"name": "dora", "host": "doorbbs"}, "str": "psst alice"}
		if err := d.Write("chat", "channels."+target+".messages", pm, chat.LockWrite); err != nil {
			t.Fatal(err)
		}
		_ = d.Push("chat", "channels."+target+".history", pm, chat.LockWrite)
		if p := next(t, ws, "UPDATE", "WRITE"); p.Location != "channels."+box+".messages" || !strings.Contains(string(p.Data), "psst alice") {
			t.Fatalf("browser got %s %s", p.Location, p.Data)
		}
		if data := sliceUntil(t, ws, "channels."+box+".history", func(d string) bool { return strings.Contains(d, "psst alice") }); !strings.Contains(data, "psst alice") {
			t.Fatalf("own history: %s", data)
		}

		// alice replies to dora's plain mailbox (a BBS user) and keeps a
		// sent copy in her own history.
		reply := `{"nick":{"name":"x"},"str":"hi dora","private":{"to":{"name":"dora","host":"doorbbs","mailbox":"evil@x"}}}`
		put(t, ws, `{"scope":"chat","func":"QUERY","oper":"WRITE","location":"channels.dora.messages","data":`+reply+`}`)
		put(t, ws, `{"scope":"chat","func":"QUERY","oper":"PUSH","location":"channels.`+box+`.history","data":`+reply+`}`)
		got := update(t, d, "WRITE")
		var m webMessage
		json.Unmarshal(got.Data, &m)
		if got.Location != "channels.dora.messages" || m.Str != "hi dora" || m.Nick.Name != "alice" || m.Nick.Mailbox != box {
			t.Fatalf("door got %s %s", got.Location, got.Data)
		}
		if m.Private == nil || m.Private.To.Name != "dora" || m.Private.To.Mailbox != "" {
			t.Fatalf("private.to = %+v (a forged recipient mailbox must be dropped)", m.Private)
		}
		if data := sliceUntil(t, ws, "channels."+box+".history", func(d string) bool { return strings.Contains(d, "hi dora") }); !strings.Contains(data, "hi dora") {
			t.Fatalf("sent copy missing: %s", data)
		}
	})
}

// Spec test 5: a forged mailbox is ignored by the helpers, and the page
// can't write somewhere that isn't a mailbox name.
func TestForgedMailboxAndTargets(t *testing.T) {
	for _, c := range []struct{ name, host, mailbox string }{
		{"alice", "testbbs", "bob@testbbs"},
		{"alice", "testbbs", "alice@test.bbs"},
		{"alice", "testbbs", "Hm Derdoc"},
	} {
		if got := chat.PMTarget(c.name, c.host, c.mailbox); got != c.name {
			t.Errorf("PMTarget(%q, %q, %q) = %q, want the plain name", c.name, c.host, c.mailbox, got)
		}
	}
	f := startFront(t, false, nil, nil)
	ws := dialWS(t, f.ws, nil)
	subscribe(t, ws, "alice")
	next(t, ws, "RESPONSE", "SUBSCRIBE")
	refusedWS(t, ws, `{"scope":"chat","func":"QUERY","oper":"WRITE","location":"channels. padded.messages","data":{"str":"x"}}`, "no such recipient")
	refusedWS(t, ws, `{"scope":"chat","func":"QUERY","oper":"WRITE","location":"channels.bob.history","data":{"str":"x"}}`, "no public room")
	refusedWS(t, ws, `{"scope":"chat","func":"QUERY","oper":"WRITE","location":"channels.`+chat.MailboxFor("alice", "testbbs")+`.messages","data":{"str":"x"}}`, "not allowed")
	// Before a claim nothing goes anywhere.
	fresh := dialWS(t, f.ws, nil)
	refusedWS(t, fresh, `{"scope":"chat","func":"QUERY","oper":"WRITE","location":"channels.bob.messages","data":{"str":"x"}}`, "claim a nickname first")
}
