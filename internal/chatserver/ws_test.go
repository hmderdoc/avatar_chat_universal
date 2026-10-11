package chatserver

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
	"github.com/hmderdoc/avatar_chat_universal/internal/denylist"
)

// startBoth runs one Server on TCP and, through httptest, on WebSocket.
func startBoth(t *testing.T, setup func(*Server)) (tcpAddr, wsURL string) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tcpAddr = l.Addr().String()
	l.Close()
	srv := New(tcpAddr)
	srv.Logger = log.New(io.Discard, "", 0)
	if setup != nil {
		setup(srv)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = srv.ListenAndServe(ctx) }()
	hs := httptest.NewServer(srv.WSHandler(ctx))
	t.Cleanup(func() { cancel(); hs.Close() })
	for i := 0; i < 100; i++ {
		if c, err := net.Dial("tcp", tcpAddr); err == nil {
			c.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	return tcpAddr, "ws" + strings.TrimPrefix(hs.URL, "http")
}

func wsDial(t *testing.T, url string, h http.Header) *websocket.Conn {
	t.Helper()
	ws, resp, err := websocket.DefaultDialer.Dial(url, h)
	if err != nil {
		code := 0
		if resp != nil {
			code = resp.StatusCode
		}
		t.Fatalf("dial %s: %v (HTTP %d)", url, err, code)
	}
	t.Cleanup(func() { ws.Close() })
	return ws
}

// wsNext reads frames until one matches func/oper, failing after 2s.
func wsNext(t *testing.T, ws *websocket.Conn, fn, oper string) packet {
	t.Helper()
	_ = ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		mt, data, err := ws.ReadMessage()
		if err != nil {
			t.Fatalf("waiting for %s %s: %v", fn, oper, err)
		}
		if mt != websocket.TextMessage {
			t.Fatalf("frame type %d, want text", mt)
		}
		if strings.ContainsAny(string(data), "\r\n") {
			t.Fatalf("frame carries a line ending: %q", data)
		}
		var p packet
		if err := json.Unmarshal(data, &p); err != nil {
			t.Fatalf("frame isn't one packet: %q", data)
		}
		if p.Func == fn && p.Oper == oper {
			return p
		}
	}
}

func wsSend(t *testing.T, ws *websocket.Conn, s string) {
	t.Helper()
	if err := ws.WriteMessage(websocket.TextMessage, []byte(s)); err != nil {
		t.Fatal(err)
	}
}

// A browser on WebSocket and a door on TCP share a channel both ways.
func TestWSAndTCPShareChannel(t *testing.T) {
	tcpAddr, wsURL := startBoth(t, nil)
	const loc = "channels.test.messages"

	ws := wsDial(t, wsURL+"/chat", nil)
	wsSend(t, ws, `{"scope":"chat","func":"QUERY","oper":"SUBSCRIBE","location":"`+loc+`","nick":"webby","system":"web"}`)
	wsNext(t, ws, "RESPONSE", "SUBSCRIBE")

	door := chat.NewClient(tcpAddr)
	door.Nick = "door"
	if err := door.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer door.Close()
	if err := door.Subscribe("chat", loc); err != nil {
		t.Fatal(err)
	}
	wsNext(t, ws, "UPDATE", "SUBSCRIBE")

	// TCP -> WS.
	if err := door.Write("chat", loc, map[string]string{"text": "from door"}, chat.LockWrite); err != nil {
		t.Fatal(err)
	}
	if p := wsNext(t, ws, "UPDATE", "WRITE"); !strings.Contains(string(p.Data), "from door") {
		t.Fatalf("ws got %s", p.Data)
	}

	// WS -> TCP, sent with a trailing CRLF like a TCP line; a ping rides
	// in the same frame.
	wsSend(t, ws, `{"scope":"chat","func":"QUERY","oper":"WRITE","location":"`+loc+`","data":{"text":"from web"}}`+"\r\n"+`{"scope":"SOCKET","func":"PING","data":7}`)
	select {
	case upd := <-door.Updates():
		if upd.Oper != "WRITE" || !strings.Contains(string(upd.Data), "from web") {
			t.Fatalf("door got %+v", upd)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("door never saw the web write")
	}
	wsNext(t, ws, "PONG", "")

	// WHO lists both, and the WS nick is the one it subscribed with.
	wsSend(t, ws, `{"scope":"chat","func":"QUERY","oper":"WHO","location":"`+loc+`"}`)
	who := string(wsNext(t, ws, "RESPONSE", "WHO").Data)
	if !strings.Contains(who, "webby") || !strings.Contains(who, "door") {
		t.Fatalf("WHO = %s", who)
	}

	// Closing the socket drops its subscription.
	ws.Close()
	deadline := time.Now().Add(2 * time.Second)
	for {
		entries, err := door.Who("chat", loc)
		if err == nil && len(entries) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after close WHO = %v (%v)", entries, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// The framing a page uses through Synchronet's websocketservice.js works
// here too: subprotocol "binary", binary frames, CRLF-terminated packets.
func TestWSBinarySubprotocol(t *testing.T) {
	_, wsURL := startBoth(t, nil)
	d := websocket.Dialer{Subprotocols: []string{"binary"}}
	ws, _, err := d.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	if ws.Subprotocol() != "binary" {
		t.Fatalf("subprotocol %q", ws.Subprotocol())
	}
	if err := ws.WriteMessage(websocket.BinaryMessage, []byte(`{"scope":"SOCKET","func":"PING","data":"► é"}`+"\r\n")); err != nil {
		t.Fatal(err)
	}
	if p := wsNext(t, ws, "PONG", ""); string(p.Data) != `"► é"` {
		t.Fatalf("pong data %s", p.Data)
	}
}

func TestWSOversizeFrameRejected(t *testing.T) {
	_, wsURL := startBoth(t, nil)
	ws := wsDial(t, wsURL, nil)
	wsSend(t, ws, `{"data":"`+strings.Repeat("x", MaxPacket+10)+`"}`)
	_ = ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err := ws.ReadMessage()
	if !websocket.IsCloseError(err, websocket.CloseMessageTooBig) {
		t.Fatalf("oversize frame: %v, want close 1009", err)
	}
}

func TestWSDenyList(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deny.txt")
	if err := os.WriteFile(path, []byte("127.0.0.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, wsURL := startBoth(t, func(s *Server) { s.Deny = denylist.New(path, s.Logger) })
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("denied dial: err %v resp %v", err, resp)
	}
}

func TestWSOrigins(t *testing.T) {
	_, wsURL := startBoth(t, func(s *Server) {
		s.WSOrigins = []string{"https://bbs.example.org", "chat.example.net"}
	})
	try := func(origin string) bool {
		h := http.Header{}
		if origin != "" {
			h.Set("Origin", origin)
		}
		ws, _, err := websocket.DefaultDialer.Dial(wsURL, h)
		if err == nil {
			ws.Close()
		}
		return err == nil
	}
	for origin, want := range map[string]bool{
		"https://bbs.example.org":      true,
		"HTTPS://BBS.EXAMPLE.ORG/":     true,
		"http://bbs.example.org":       false, // full entry pins the scheme
		"http://chat.example.net:8080": true,  // host entry: any scheme or port
		"https://evil.example.com":     false,
		"":                             true, // no Origin: not a browser
	} {
		if got := try(origin); got != want {
			t.Errorf("origin %q allowed=%v, want %v", origin, got, want)
		}
	}
	// No list: anything goes.
	_, open := startBoth(t, nil)
	h := http.Header{"Origin": {"https://anywhere.example"}}
	wsDial(t, open, h)
}

func TestWSPathAndKeepalive(t *testing.T) {
	_, wsURL := startBoth(t, func(s *Server) {
		s.wsPing, s.wsWait = 50*time.Millisecond, 300*time.Millisecond
	})
	if _, resp, err := websocket.DefaultDialer.Dial(wsURL+"/other", nil); err == nil || resp == nil || resp.StatusCode != http.StatusNotFound {
		t.Fatalf("/other: err %v resp %v", err, resp)
	}

	// A client that answers pings (gorilla does while reading) stays up
	// well past the pong wait.
	ws := wsDial(t, wsURL, nil)
	pings := 0
	ws.SetPingHandler(func(d string) error {
		pings++
		return ws.WriteControl(websocket.PongMessage, []byte(d), time.Now().Add(time.Second))
	})
	_ = ws.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := ws.ReadMessage(); err == nil || !strings.Contains(err.Error(), "timeout") || pings < 5 {
		t.Fatalf("live client: err %v after %d pings", err, pings)
	}

	// One that never reads (so never pongs) is dropped.
	dead := wsDial(t, wsURL, nil)
	time.Sleep(600 * time.Millisecond)
	_ = dead.SetReadDeadline(time.Now().Add(time.Second))
	dead.SetPingHandler(func(string) error { return nil })
	for {
		if _, _, err := dead.ReadMessage(); err != nil {
			if strings.Contains(err.Error(), "timeout") {
				t.Fatal("silent client was not dropped")
			}
			break
		}
	}
}
