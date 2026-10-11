package webchat

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gorilla/websocket"

	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
	"github.com/hmderdoc/avatar_chat_universal/internal/chatmedia"
	"github.com/hmderdoc/avatar_chat_universal/internal/chatserver"
	"github.com/hmderdoc/avatar_chat_universal/internal/denylist"
	"github.com/hmderdoc/avatar_chat_universal/internal/inlineimg"
)

var quiet = log.New(io.Discard, "", 0)

// startChat runs a local chatserver on TCP and returns its address.
func startChat(t *testing.T) (*chatserver.Server, string, context.CancelFunc) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	srv := chatserver.New(addr)
	srv.Logger = quiet
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = srv.ListenAndServe(ctx) }()
	for i := 0; i < 100; i++ {
		if c, err := net.Dial("tcp", addr); err == nil {
			c.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Cleanup(cancel)
	return srv, addr, cancel
}

type front struct {
	web    *Server
	chat   *chatserver.Server
	tcp    string // the chat's TCP address (local, or the upstream)
	http   string // http://127.0.0.1:port
	ws     string // ws://127.0.0.1:port/chat
	cancel context.CancelFunc
}

// startFront runs a front end over httptest: hosting its own chat, or with
// relay set, relaying to a separate chatserver.
func startFront(t *testing.T, relay bool, setup func(*Config), after func(*Server)) *front {
	t.Helper()
	cs, addr, cancel := startChat(t)
	cfg := Defaults()
	cfg.SystemName = "testbbs"
	if relay {
		cfg.Upstream = addr
	}
	if setup != nil {
		setup(&cfg)
	}
	web := New(cfg)
	web.Logger = quiet
	if !relay {
		web.Chat = cs
	}
	web.Static = fstest.MapFS{
		"index.html": {Data: []byte("<!doctype html><title>t</title>")},
		"app.js":     {Data: []byte("//js")},
		"embed.go":   {Data: []byte("package x")},
	}
	if after != nil {
		after(web)
	}
	hs := httptest.NewServer(web.Handler())
	t.Cleanup(hs.Close)
	return &front{web: web, chat: cs, tcp: addr, http: hs.URL, ws: "ws" + strings.TrimPrefix(hs.URL, "http") + "/chat", cancel: cancel}
}

func dialWS(t *testing.T, url string, h http.Header) *websocket.Conn {
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

// next reads frames until one matches func/oper, failing after 3s.
func next(t *testing.T, ws *websocket.Conn, fn, oper string) packet {
	t.Helper()
	_ = ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		mt, data, err := ws.ReadMessage()
		if err != nil {
			t.Fatalf("waiting for %s %s: %v", fn, oper, err)
		}
		if mt != websocket.TextMessage || strings.ContainsAny(string(data), "\r\n") {
			t.Fatalf("frame %d %q: want one text packet, no line ending", mt, data)
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

func put(t *testing.T, ws *websocket.Conn, s string) {
	t.Helper()
	if err := ws.WriteMessage(websocket.TextMessage, []byte(s)); err != nil {
		t.Fatal(err)
	}
}

const mainLoc = "channels.main.messages"

// testKey is the browser key the tests claim nick with.
func testKey(nick string) string { return "testkey-" + strings.Repeat("0", 32) + nick }

func claimPacket(nick string) string {
	return `{"scope":"SOCKET","func":"CLAIM","data":{"nick":"` + nick + `","key":"` + testKey(nick) + `"}}`
}

// subscribe claims nick (in the same frame) and joins main.
func subscribe(t *testing.T, ws *websocket.Conn, nick string) {
	t.Helper()
	put(t, ws, claimPacket(nick)+"\n"+`{"scope":"chat","func":"QUERY","oper":"SUBSCRIBE","location":"`+mainLoc+`","nick":"`+nick+`","system":"spoofed"}`)
}

func door(t *testing.T, addr, nick string) *chat.Client {
	t.Helper()
	c := chat.NewClient(addr)
	c.Nick, c.System = nick, "doorbbs"
	if err := c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if err := c.Subscribe("chat", mainLoc); err != nil {
		t.Fatal(err)
	}
	return c
}

func update(t *testing.T, c *chat.Client, oper string) *chat.Packet {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case u := <-c.Updates():
			if u.Oper == oper {
				return u
			}
		case <-deadline:
			t.Fatalf("no %s update", oper)
			return nil
		}
	}
}

// The same exchange works whether the front end hosts the chat or relays
// to another server: web users and a door see each other, and what a web
// user sends is stamped with their connection's nick and this board.
func TestWebAndDoorShareRoom(t *testing.T) {
	for _, relay := range []bool{false, true} {
		name := map[bool]string{false: "local", true: "relay"}[relay]
		t.Run(name, func(t *testing.T) {
			f := startFront(t, relay, nil, nil)
			a := dialWS(t, f.ws, nil)
			subscribe(t, a, "alice")
			next(t, a, "RESPONSE", "SUBSCRIBE")
			b := dialWS(t, f.ws, nil)
			subscribe(t, b, "bob")
			next(t, b, "RESPONSE", "SUBSCRIBE")
			next(t, a, "UPDATE", "SUBSCRIBE")
			d := door(t, f.tcp, "dora")
			next(t, a, "UPDATE", "SUBSCRIBE")
			next(t, b, "UPDATE", "SUBSCRIBE")

			// Door -> both browsers.
			if err := d.Write("chat", mainLoc, map[string]interface{}{"nick": map[string]string{"name": "dora"}, "str": "hi web"}, chat.LockWrite); err != nil {
				t.Fatal(err)
			}
			for _, ws := range []*websocket.Conn{a, b} {
				if p := next(t, ws, "UPDATE", "WRITE"); !strings.Contains(string(p.Data), "hi web") {
					t.Fatalf("browser got %s", p.Data)
				}
			}

			// Browser -> door and the other browser, claiming another
			// name and board; a ping rides in the same frame.
			put(t, a, `{"scope":"chat","func":"QUERY","oper":"WRITE","location":"`+mainLoc+`","nick":"root","system":"futureland","data":{"nick":{"name":"Sysop","host":"futureland","ip":"1.2.3.4"},"str":"hello\u0007 door","time":1}}`+"\r\n"+`{"scope":"SOCKET","func":"PING","data":9}`)
			next(t, a, "PONG", "")
			u := update(t, d, "WRITE")
			var m struct {
				Nick map[string]string `json:"nick"`
				Str  string            `json:"str"`
				Time int64             `json:"time"`
			}
			if err := json.Unmarshal(u.Data, &m); err != nil {
				t.Fatal(err)
			}
			if m.Nick["name"] != "alice" || m.Nick["host"] != "testbbs" || m.Nick["ip"] != "" || m.Str != "hello door" || m.Time < 1e12 {
				t.Fatalf("door got %s", u.Data)
			}
			if p := next(t, b, "UPDATE", "WRITE"); !strings.Contains(string(p.Data), `"alice"`) {
				t.Fatalf("bob got %s", p.Data)
			}

			// WHO lists everyone, the web users under their own nicks.
			put(t, a, `{"scope":"chat","func":"QUERY","oper":"WHO","location":"`+mainLoc+`"}`)
			who := string(next(t, a, "RESPONSE", "WHO").Data)
			for _, n := range []string{"alice", "bob", "dora"} {
				if !strings.Contains(who, n) {
					t.Fatalf("WHO = %s", who)
				}
			}

			// A browser leaving drops it from the room.
			b.Close()
			deadline := time.Now().Add(3 * time.Second)
			for {
				entries, err := d.Who("chat", mainLoc)
				if err == nil && len(entries) == 2 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("after close WHO = %v (%v)", entries, err)
				}
				time.Sleep(20 * time.Millisecond)
			}
		})
	}
}

// When the upstream goes away, the browser's socket closes too.
func TestRelayUpstreamGoneClosesBrowser(t *testing.T) {
	f := startFront(t, true, nil, nil)
	ws := dialWS(t, f.ws, nil)
	subscribe(t, ws, "alice")
	put(t, ws, `{"scope":"chat","func":"QUERY","oper":"WHO","location":"`+mainLoc+`"}`)
	next(t, ws, "RESPONSE", "WHO")
	f.cancel() // stops the upstream chatserver, closing its connections
	_ = ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		if _, _, err := ws.ReadMessage(); err != nil {
			if strings.Contains(err.Error(), "timeout") {
				t.Fatal("browser stayed open after the upstream closed")
			}
			return
		}
	}
}

// The relay answers the upstream's keepalive itself.
func TestRelayAnswersUpstreamPing(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	got := make(chan string, 1)
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_, _ = c.Write([]byte(`{"scope":"SOCKET","func":"PING","data":42}` + "\r\n"))
		buf := make([]byte, 512)
		_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
		n, _ := c.Read(buf)
		got <- string(buf[:n])
	}()
	cfg := Defaults()
	cfg.Upstream = l.Addr().String()
	web := New(cfg)
	web.Logger = quiet
	hs := httptest.NewServer(web.Handler())
	defer hs.Close()
	ws := dialWS(t, "ws"+strings.TrimPrefix(hs.URL, "http")+"/chat", nil)
	_ = ws
	select {
	case s := <-got:
		if !strings.Contains(s, `"PONG"`) || !strings.Contains(s, "42") || !strings.HasSuffix(s, "\r\n") {
			t.Fatalf("upstream got %q", s)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("no PONG upstream")
	}
}

func TestConnectionCaps(t *testing.T) {
	f := startFront(t, true, func(c *Config) { c.MaxPerIP = 2; c.MaxConns = 3 }, nil)
	a := dialWS(t, f.ws, nil)
	dialWS(t, f.ws, nil)
	if _, resp, err := websocket.DefaultDialer.Dial(f.ws, nil); err == nil || resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("third from one address: err %v resp %v", err, resp)
	}
	// A slot frees when a browser goes.
	a.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		ws, _, err := websocket.DefaultDialer.Dial(f.ws, nil)
		if err == nil {
			ws.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("slot never freed")
		}
		time.Sleep(20 * time.Millisecond)
	}
	f.web.mu.Lock()
	defer f.web.mu.Unlock()
	if f.web.conns > 2 {
		t.Fatalf("conns = %d", f.web.conns)
	}
}

func filterOne(p *policy, s string) (string, string) {
	pass, reply := p.filter([]byte(s))
	return string(pass), string(reply)
}

func TestPolicy(t *testing.T) {
	cs, _, _ := startChat(t)
	cfg := Defaults()
	cfg.SystemName = "testbbs"
	web := New(cfg)
	web.Chat = cs
	p := &policy{srv: web}

	refused := func(s, want string) {
		t.Helper()
		pass, reply := filterOne(p, s)
		if pass != "" || !strings.Contains(reply, want) {
			t.Fatalf("%s\n pass %s\n reply %s (want %q)", s, pass, reply, want)
		}
	}
	refused(`{"scope":"chat","func":"QUERY","oper":"WRITE","location":"`+mainLoc+`","data":{"str":"x"}}`, "nickname first")
	refused(`{"scope":"chat","func":"QUERY","oper":"SUBSCRIBE","location":"`+mainLoc+`","nick":"eve"}`, "claim a nickname first")
	claimed := func(nick, want string) {
		t.Helper()
		_, reply := filterOne(p, claimPacket(nick))
		if !strings.Contains(reply, want) {
			t.Fatalf("claim %q: %s (want %s)", nick, reply, want)
		}
	}
	claimed("bad.name", `"reason":"invalid"`)
	claimed(strings.Repeat("n", 26), `"reason":"invalid"`)
	if _, reply := filterOne(p, `{"scope":"SOCKET","func":"CLAIM","data":{"nick":"eve","key":"short"}}`); !strings.Contains(reply, `"ok":false`) {
		t.Fatalf("short key: %s", reply)
	}
	refused(`{"scope":"chat","func":"QUERY","oper":"SUBSCRIBE","location":"channels.bob.messages","nick":"eve"}`, "no public room named bob")
	refused(`{"scope":"other","func":"QUERY","oper":"READ","location":"x"}`, "not allowed")
	refused(`{"scope":"chat","func":"QUERY","oper":"LOCK","location":"`+mainLoc+`"}`, "not allowed")
	refused(`{"scope":"chat","func":"QUERY","oper":"WRITE","location":"channels.main.history","data":[]}`, "not allowed")
	refused(`{"scope":"chat","func":"QUERY","oper":"READ","location":"users"}`, "not allowed")
	refused(`{"scope":"chat","func":"QUERY","oper":"WRITE","location":"public_channels.newroom","data":true}`, "creating rooms is off")
	refused(`not json`, "malformed")

	claimed("  eve   two ", `"ok":true`)
	pass, _ := filterOne(p, `{"scope":"chat","func":"QUERY","oper":"SUBSCRIBE","location":"`+mainLoc+`","nick":"mallory","system":"futureland"}`)
	if !strings.Contains(pass, `"nick":"eve two"`) || !strings.Contains(pass, `"system":"testbbs"`) {
		t.Fatalf("subscribe passed as %s", pass)
	}
	// The nick is fixed by the claim.
	claimed("mallory", `"reason":"fixed"`)
	refused(`{"scope":"chat","func":"QUERY","oper":"WRITE","location":"`+mainLoc+`","data":{"str":"[TVTUNER|off]"}}`, "not allowed")
	refused(`{"scope":"chat","func":"QUERY","oper":"WRITE","location":"`+mainLoc+`","data":{"str":"`+strings.Repeat("é", 1001)+`"}}`, "too long")
	refused(`{"scope":"chat","func":"QUERY","oper":"WRITE","location":"`+mainLoc+`","data":{"str":"  \r\n "}}`, "empty")

	avatar := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("A"), 120))
	pass, _ = filterOne(p, `{"scope":"chat","func":"QUERY","oper":"PUSH","location":"channels.main.history","data":{"nick":{"name":"x","avatar":"`+avatar+`","style":{"colors":["#ff0000",7],"tag":{"text":"hi|there"}}},"str":"see https://example.com/a.png"}}`)
	var out struct {
		Data webMessage `json:"data"`
		Lock int        `json:"lock"`
	}
	if err := json.Unmarshal([]byte(pass), &out); err != nil {
		t.Fatalf("%v: %s", err, pass)
	}
	if out.Data.Nick.Name != "eve two" || out.Data.Nick.Mailbox != "eve two@testbbs" || out.Data.Nick.Avatar != avatar || out.Data.Nick.Style == nil || out.Lock != chat.LockWrite {
		t.Fatalf("push passed as %s", pass)
	}
	if strings.Contains(pass, "tag") || strings.Contains(pass, "hi") && strings.Contains(pass, "there") {
		t.Fatalf("a tag got through: %s", pass)
	}
	if !web.seen.has("https://example.com/a.png") {
		t.Fatal("the sender's own link isn't fetchable for them")
	}
	pass, _ = filterOne(p, `{"scope":"chat","func":"QUERY","oper":"WRITE","location":"`+mainLoc+`","data":{"nick":{"avatar":"bm90IGFuIGF2YXRhcg=="},"str":"x"}}`)
	if strings.Contains(pass, "avatar") {
		t.Fatalf("bad avatar kept: %s", pass)
	}

	// SLICE is capped; KEYS public_channels is fine; PING is answered here.
	pass, _ = filterOne(p, `{"scope":"chat","func":"QUERY","oper":"SLICE","location":"channels.main.history","data":{"start":-100000}}`)
	if !strings.Contains(pass, `"start":-500`) {
		t.Fatalf("slice passed as %s", pass)
	}
	if pass, _ := filterOne(p, `{"scope":"chat","func":"QUERY","oper":"KEYS","location":"public_channels"}`); pass == "" {
		t.Fatal("KEYS refused")
	}
	if pass, reply := filterOne(p, `{"scope":"SOCKET","func":"PING","data":"x"}`); pass != "" || !strings.Contains(reply, "PONG") {
		t.Fatalf("ping: pass %s reply %s", pass, reply)
	}

	// A room in the public_channels registry is open; with CreateRooms a
	// web user can register one.
	reg := chat.NewClient(cs.Addr)
	if err := reg.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	_ = reg.Write("chat", "public_channels.lounge", true, chat.LockWrite)
	deadline := time.Now().Add(2 * time.Second)
	for !web.rooms.allowed("lounge") {
		if time.Now().After(deadline) {
			t.Fatal("registered room not allowed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	web.cfg.CreateRooms = true
	refused(`{"scope":"chat","func":"QUERY","oper":"WRITE","location":"public_channels.eve two","data":true}`, "room names")
	if pass, _ := filterOne(p, `{"scope":"chat","func":"QUERY","oper":"WRITE","location":"public_channels.newroom","data":"anything"}`); !strings.Contains(pass, `"data":true`) {
		t.Fatalf("create passed as %s", pass)
	}
	if pass, _ := filterOne(p, `{"scope":"chat","func":"QUERY","oper":"SUBSCRIBE","location":"channels.newroom.messages"}`); pass == "" {
		t.Fatal("new room not joinable")
	}
}

// The relay reads the room registry from the upstream.
func TestRelayRoomRegistry(t *testing.T) {
	f := startFront(t, true, nil, nil)
	reg := chat.NewClient(f.tcp)
	if err := reg.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	_ = reg.Write("chat", "public_channels.lounge", true, chat.LockWrite)
	time.Sleep(100 * time.Millisecond)
	if !f.web.rooms.allowed("Lounge") || f.web.rooms.allowed("bob") {
		t.Fatal("upstream registry not honored")
	}
}

func TestOrigins(t *testing.T) {
	f := startFront(t, false, func(c *Config) { c.Origins = []string{"https://bbs.example.org", "chat.example.net"} }, nil)
	try := func(origin string) bool {
		h := http.Header{}
		if origin != "" {
			h.Set("Origin", origin)
		}
		ws, _, err := websocket.DefaultDialer.Dial(f.ws, h)
		if err == nil {
			ws.Close()
		}
		return err == nil
	}
	host := strings.TrimPrefix(f.http, "http://")
	for origin, want := range map[string]bool{
		"http://" + host:               true, // the page's own origin
		"https://bbs.example.org":      true,
		"HTTPS://BBS.EXAMPLE.ORG/":     true,
		"http://bbs.example.org":       false, // a full entry pins the scheme
		"http://chat.example.net:8080": true,  // a host entry: any scheme or port
		"https://evil.example.com":     false,
		"":                             true, // no Origin: not a browser
	} {
		if got := try(origin); got != want {
			t.Errorf("origin %q allowed=%v, want %v", origin, got, want)
		}
	}
	// No list: only the page's own origin.
	g := startFront(t, false, nil, nil)
	if _, _, err := websocket.DefaultDialer.Dial(g.ws, http.Header{"Origin": {"https://anywhere.example"}}); err == nil {
		t.Fatal("a foreign page connected with no ws_origins")
	}
}

func TestOversizeDenyAndKeepalive(t *testing.T) {
	f := startFront(t, false, nil, func(s *Server) { s.pingEvery, s.pongWait = 50*time.Millisecond, 300*time.Millisecond })
	ws := dialWS(t, f.ws, nil)
	put(t, ws, `{"data":"`+strings.Repeat("x", chatserver.MaxPacket+10)+`"}`)
	_ = ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := ws.ReadMessage(); !websocket.IsCloseError(err, websocket.CloseMessageTooBig) {
		t.Fatalf("oversize frame: %v, want close 1009", err)
	}

	// A browser that answers pings stays; one that never reads goes.
	live := dialWS(t, f.ws, nil)
	pings := 0
	live.SetPingHandler(func(d string) error {
		pings++
		return live.WriteControl(websocket.PongMessage, []byte(d), time.Now().Add(time.Second))
	})
	_ = live.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := live.ReadMessage(); err == nil || !strings.Contains(err.Error(), "timeout") || pings < 5 {
		t.Fatalf("live client: err %v after %d pings", err, pings)
	}
	dead := dialWS(t, f.ws, nil)
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

	path := filepath.Join(t.TempDir(), "deny.txt")
	os.WriteFile(path, []byte("127.0.0.1\n"), 0o644)
	g := startFront(t, false, nil, func(s *Server) { s.Deny = denylist.New(path, quiet) })
	for _, u := range []string{g.http + "/", g.http + "/config.json"} {
		if resp, err := http.Get(u); err != nil || resp.StatusCode != http.StatusForbidden {
			t.Fatalf("denied GET %s: %v %v", u, resp, err)
		}
	}
	if _, resp, err := websocket.DefaultDialer.Dial(g.ws, nil); err == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("denied dial: %v %v", err, resp)
	}
}

func get(t *testing.T, u string) (*http.Response, string) {
	t.Helper()
	resp, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(b)
}

func TestStaticAndConfig(t *testing.T) {
	f := startFront(t, false, nil, nil)
	resp, body := get(t, f.http+"/")
	if resp.StatusCode != 200 || !strings.Contains(body, "<title>") ||
		!strings.Contains(resp.Header.Get("Content-Security-Policy"), "connect-src 'self'") ||
		resp.Header.Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("/: %d %v", resp.StatusCode, resp.Header)
	}
	if resp, _ := get(t, f.http+"/app.js"); resp.Header.Get("Content-Type") != "text/javascript; charset=utf-8" {
		t.Fatalf("app.js type %q", resp.Header.Get("Content-Type"))
	}
	for _, p := range []string{"/embed.go", "/../../etc/passwd", "/nope.js", "/.hidden.js"} {
		if resp, _ := get(t, f.http+p); resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: %d", p, resp.StatusCode)
		}
	}
	// The old ws_addr listener took upgrades at /.
	ws := dialWS(t, strings.TrimSuffix(f.ws, "/chat")+"/", nil)
	subscribe(t, ws, "olduser")
	next(t, ws, "RESPONSE", "SUBSCRIBE")

	resp, body = get(t, f.http+"/config.json")
	var c pageConfig
	if err := json.Unmarshal([]byte(body), &c); err != nil || c.DefaultChannel != "main" || c.SystemName != "testbbs" ||
		c.MaxMessage != 1000 || c.Uploads || c.Mode != "local" {
		t.Fatalf("config.json %s (%v)", body, err)
	}
	g := startFront(t, true, nil, nil)
	if _, body := get(t, g.http+"/config.json"); !strings.Contains(body, `"mode":"relay"`) {
		t.Fatalf("relay config.json %s", body)
	}
}

type fakeImages map[string][]byte

func (f fakeImages) Fetch(_ context.Context, u string) ([]byte, error) {
	if b, ok := f[u]; ok {
		return b, nil
	}
	return nil, inlineimg.ErrBlocked
}

type fakePages map[string]string

func (f fakePages) FetchPage(_ context.Context, u string, _ int64) (*inlineimg.Page, error) {
	return &inlineimg.Page{Body: []byte(f[u]), FinalURL: u, ContentType: "text/html; charset=utf-8"}, nil
}

func pngBytes() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 96, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 96; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 2), uint8(y * 3), 170, 255})
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

func TestImageProxy(t *testing.T) {
	pic := "https://pics.example/cat.png"
	page := "https://pics.example/fake.png"
	svg := "https://pics.example/x.svg"
	f := startFront(t, false, func(c *Config) { c.FetchPerMinute = 6 }, func(s *Server) {
		s.Images = fakeImages{
			pic:  pngBytes(),
			page: []byte("<!doctype html><html><script>alert(1)</script></html>"),
			svg:  []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		}
	})
	img := func(u string) *http.Response {
		resp, _ := get(t, f.http+"/img?url="+u)
		return resp
	}
	if r := img(pic); r.StatusCode != http.StatusForbidden {
		t.Fatalf("unseen URL: %d", r.StatusCode)
	}

	// A link becomes fetchable once it has gone out to a browser.
	ws := dialWS(t, f.ws, nil)
	subscribe(t, ws, "alice")
	next(t, ws, "RESPONSE", "SUBSCRIBE")
	d := door(t, f.tcp, "dora")
	_ = d.Write("chat", mainLoc, map[string]interface{}{"str": "look " + pic + " and " + page + ", " + svg}, chat.LockWrite)
	next(t, ws, "UPDATE", "WRITE")

	r := img(pic)
	if r.StatusCode != 200 || r.Header.Get("Content-Type") != "image/png" || r.Header.Get("X-Content-Type-Options") != "nosniff" ||
		!strings.Contains(r.Header.Get("Content-Security-Policy"), "sandbox") || !strings.Contains(r.Header.Get("Cache-Control"), "max-age") {
		t.Fatalf("picture: %d %v", r.StatusCode, r.Header)
	}
	for _, u := range []string{page, svg} {
		if r := img(u); r.StatusCode != http.StatusUnsupportedMediaType {
			t.Errorf("%s: %d, want 415", u, r.StatusCode)
		}
	}
	// 3 used of 6 a minute (the unseen try isn't counted): three more,
	// then 429.
	img(pic)
	img(pic)
	img(pic)
	if r := img(pic); r.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("over the rate: %d", r.StatusCode)
	}
}

// The real fetcher's address guards refuse a private address.
func TestImageProxyBlocksPrivate(t *testing.T) {
	inner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngBytes())
	}))
	defer inner.Close()
	f := startFront(t, false, nil, func(s *Server) { s.Images = inlineimg.NewFetcher("") })
	for _, u := range []string{inner.URL + "/a.png", "http://127.0.0.1/a.png", "http://10.0.0.1/a.png", "file:///etc/passwd"} {
		f.web.seen.add(u)
		if resp, _ := get(t, f.http+"/img?url="+u); resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s: %d, want 403", u, resp.StatusCode)
		}
	}
}

func TestPreview(t *testing.T) {
	link := "https://news.example/story"
	f := startFront(t, false, nil, func(s *Server) {
		s.Pages = fakePages{link: `<html><head><meta property="og:title" content="Big News"><meta property="og:image" content="/pic.jpg"></head></html>`}
		s.Images = fakeImages{"https://news.example/pic.jpg": pngBytes()}
	})
	if resp, _ := get(t, f.http+"/preview?url="+link); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("unseen link: %d", resp.StatusCode)
	}
	f.web.seen.add(link)
	resp, body := get(t, f.http+"/preview?url="+link)
	if resp.StatusCode != 200 || !strings.Contains(body, `"title":"Big News"`) || !strings.Contains(body, `"image":"https://news.example/pic.jpg"`) {
		t.Fatalf("preview %d %s", resp.StatusCode, body)
	}
	// The card's own picture comes through /img.
	if resp, _ := get(t, f.http+"/img?url=https://news.example/pic.jpg"); resp.StatusCode != 200 {
		t.Fatalf("card picture: %d", resp.StatusCode)
	}
}

func TestMediaOnTheWebListener(t *testing.T) {
	mcfg := chatmedia.Defaults()
	mcfg.Dir = t.TempDir()
	media, err := chatmedia.New(mcfg)
	if err != nil {
		t.Fatal(err)
	}
	media.Logger = quiet
	f := startFront(t, false, nil, func(s *Server) { s.Media = media })
	if _, body := get(t, f.http+"/config.json"); !strings.Contains(body, `"uploads":true`) {
		t.Fatalf("config.json %s", body)
	}
	req, _ := http.NewRequest(http.MethodPost, f.http+"/upload?name=cat.png", bytes.NewReader(pngBytes()))
	req.Header.Set("Origin", "https://"+strings.TrimPrefix(f.http, "http://"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var reply struct{ URL string }
	_ = json.NewDecoder(resp.Body).Decode(&reply)
	resp.Body.Close()
	host := strings.TrimPrefix(f.http, "http://")
	// The page's https Origin says a TLS proxy is in front.
	if !strings.HasPrefix(reply.URL, "https://"+host+"/chatmedia/") {
		t.Fatalf("upload URL %q", reply.URL)
	}
	if resp, _ := get(t, f.http+strings.TrimPrefix(reply.URL, "https://"+host)); resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("serving the upload: %d", resp.StatusCode)
	}
}
