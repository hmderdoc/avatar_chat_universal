package chatserver

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// startWith runs a server set up by setup on a random port. stop cancels
// it and waits for ListenAndServe to return (and so its final save).
func startWith(t *testing.T, setup func(*Server)) (srv *Server, addr string, stop func()) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr = l.Addr().String()
	l.Close()
	srv = New(addr)
	srv.Logger = log.New(io.Discard, "", 0)
	if setup != nil {
		setup(srv)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = srv.ListenAndServe(ctx)
		close(done)
	}()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Error("server did not stop")
			}
		})
	}
	t.Cleanup(stop)
	for i := 0; i < 100; i++ {
		if c, err := net.Dial("tcp", addr); err == nil {
			c.Close()
			return srv, addr, stop
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server did not start on %s", addr)
	return
}

// raw is a bare TCP client: whole lines out, packets in.
type raw struct {
	t *testing.T
	c net.Conn
	r *bufio.Reader
}

func dialRaw(t *testing.T, addr string) *raw {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return &raw{t: t, c: c, r: bufio.NewReaderSize(c, MaxPacket+2)}
}

func (r *raw) send(lines ...string) {
	r.t.Helper()
	for _, l := range lines {
		if _, err := r.c.Write([]byte(l + "\r\n")); err != nil {
			r.t.Fatal(err)
		}
	}
}

// query sends one QUERY packet.
func (r *raw) query(oper, loc, extra string) {
	r.t.Helper()
	r.send(`{"scope":"chat","func":"QUERY","oper":"` + oper + `","location":"` + loc + `"` + extra + `}`)
}

// read returns the next packet, or ok false after d.
func (r *raw) read(d time.Duration) (p packet, ok bool) {
	r.t.Helper()
	_ = r.c.SetReadDeadline(time.Now().Add(d))
	line, err := r.r.ReadBytes('\n')
	if err != nil {
		return p, false
	}
	if err := json.Unmarshal(line, &p); err != nil {
		r.t.Fatalf("bad packet %q: %v", line, err)
	}
	return p, true
}

// expect reads until a packet with func/oper arrives (failing after 2s).
func (r *raw) expect(fn, oper string) packet {
	r.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		p, ok := r.read(time.Until(deadline))
		if ok && p.Func == fn && p.Oper == oper {
			return p
		}
	}
	r.t.Fatalf("no %s %s", fn, oper)
	return packet{}
}

// sync makes sure everything sent before it was handled: PING, then
// everything up to the PONG is returned.
func (r *raw) sync() []packet {
	r.t.Helper()
	r.send(`{"scope":"SOCKET","func":"PING","data":1}`)
	var got []packet
	for {
		p, ok := r.read(2 * time.Second)
		if !ok {
			r.t.Fatal("no PONG")
		}
		if p.Func == "PONG" {
			return got
		}
		got = append(got, p)
	}
}

// subscribeAs subscribes to loc as nick@system and waits for the reply.
func (r *raw) subscribeAs(loc, nick, system string) {
	r.t.Helper()
	r.query("SUBSCRIBE", loc, `,"nick":"`+nick+`","system":"`+system+`"`)
	r.expect("RESPONSE", "SUBSCRIBE")
}

const room = "channels.main.messages"

// A connection that drops (no UNSUBSCRIBE of its own) is announced as
// leaving to the rest of each room it was in, unless the same nick and
// system is still there on another connection.
func TestLeaveNoticeOnDisconnect(t *testing.T) {
	_, addr, _ := startWith(t, nil)
	alice := dialRaw(t, addr)
	alice.subscribeAs(room, "alice", "bbs1")

	bob := dialRaw(t, addr)
	bob.subscribeAs(room, "bob", "bbs2")
	bob.subscribeAs("channels.other.messages", "bob", "bbs2")
	bob2 := dialRaw(t, addr)
	bob2.subscribeAs(room, "Bob", "BBS2") // same user, second window
	alice.sync()                          // the two join notices

	bob.c.Close()
	time.Sleep(200 * time.Millisecond)
	for _, p := range alice.sync() {
		if p.Oper == "UNSUBSCRIBE" {
			t.Fatalf("leave notice while bob is still here: %+v", p)
		}
	}

	bob2.c.Close()
	p := alice.expect("UPDATE", "UNSUBSCRIBE")
	var who map[string]string
	_ = json.Unmarshal(p.Data, &who)
	if p.Location != room || p.Scope != "chat" || who["nick"] != "Bob" || who["system"] != "BBS2" {
		t.Fatalf("leave notice: %+v %s", p, p.Data)
	}

	// A plain one-connection user.
	carol := dialRaw(t, addr)
	carol.subscribeAs(room, "carol", "bbs3")
	alice.expect("UPDATE", "SUBSCRIBE")
	carol.c.Close()
	p = alice.expect("UPDATE", "UNSUBSCRIBE")
	if !strings.Contains(string(p.Data), `"carol"`) {
		t.Fatalf("leave notice: %s", p.Data)
	}
}

// A line longer than MaxPacket drops the connection; one that fits works.
func TestOverlongLineDropsConnection(t *testing.T) {
	_, addr, _ := startWith(t, nil)
	ok := dialRaw(t, addr)
	pad := strings.Repeat("x", MaxPacket-200)
	ok.query("WRITE", "channels.big.messages", `,"data":"`+pad+`"`)
	ok.expect("RESPONSE", "WRITE")

	bad := dialRaw(t, addr)
	if _, err := bad.c.Write([]byte(strings.Repeat("y", MaxPacket+10))); err != nil {
		t.Fatal(err)
	}
	_ = bad.c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := bad.r.ReadBytes('\n'); err == nil {
		t.Fatal("server answered an overlong line")
	} else if ne, isNet := err.(net.Error); isNet && ne.Timeout() {
		t.Fatal("connection not dropped after an overlong line")
	}
}

func TestRateLimit(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	var clock sync.Mutex
	srv, addr, _ := startWith(t, func(s *Server) {
		s.RateBurst, s.RatePerSec, s.RateTCP = 3, 1, true
		s.now = func() time.Time { clock.Lock(); defer clock.Unlock(); return now }
	})
	_ = srv
	c := dialRaw(t, addr)
	msg := func(i string) {
		c.query("WRITE", room, `,"data":{"str":"`+i+`"}`)
		c.query("PUSH", "channels.main.history", `,"data":{"str":"`+i+`"}`)
	}
	errorsIn := func(ps []packet) (n int) {
		for _, p := range ps {
			if p.Func == "ERROR" {
				if !strings.Contains(string(p.Data), "slow down") {
					t.Fatalf("error: %s", p.Data)
				}
				n++
			}
		}
		return n
	}
	// A burst of 3 messages (WRITE + PUSH each) is fine, the 4th and
	// 5th get one ERROR each and store nothing.
	for _, s := range []string{"1", "2", "3", "4", "5"} {
		msg(s)
	}
	if n := errorsIn(c.sync()); n != 2 {
		t.Fatalf("%d errors, want 2", n)
	}
	// Not limited: reads, joins, TV tuner state, the room registry.
	for i := 0; i < 20; i++ {
		c.query("WRITE", "channels.main.tvtuner", `,"data":[]`)
		c.query("PUSH", "channels.main.tvtuner", `,"data":{"str":"tune"}`)
		c.query("WRITE", "public_channels.main", `,"data":true`)
		c.query("SLICE", "channels.main.history", `,"data":{"start":-10}`)
		c.query("WHO", room, ``)
		c.query("KEYS", "public_channels", ``)
		c.query("SUBSCRIBE", room, `,"nick":"x"`)
	}
	if n := errorsIn(c.sync()); n != 0 {
		t.Fatalf("%d errors for unlimited ops", n)
	}
	got := srv.store.Slice("chat", "channels.main.history", 0, nil)
	if len(got) != 3 {
		t.Fatalf("history has %d entries, want 3", len(got))
	}

	// A web PM: WRITE to them, PUSH to their history and to our own:
	// one token. Refill gives two tokens.
	clock.Lock()
	now = now.Add(2 * time.Second)
	clock.Unlock()
	c.query("WRITE", "channels.bob.messages", `,"data":{"str":"pm"}`)
	c.query("PUSH", "channels.bob.history", `,"data":{"str":"pm"}`)
	c.query("PUSH", "channels.me.history", `,"data":{"str":"pm"}`)
	msg("6")
	msg("7")
	if n := errorsIn(c.sync()); n != 1 {
		t.Fatalf("%d errors after refill, want 1", n)
	}
	// A bare PUSH with nothing paid for takes a token of its own.
	c.query("PUSH", "channels.main.history", `,"data":{"str":"bare"}`)
	if n := errorsIn(c.sync()); n != 1 {
		t.Fatalf("bare PUSH over the limit: %d errors, want 1", n)
	}

	// Another connection has its own bucket.
	d := dialRaw(t, addr)
	d.query("WRITE", room, `,"data":{"str":"d"}`)
	if n := errorsIn(d.sync()); n != 0 {
		t.Fatal("a fresh connection was limited")
	}
}

// TCP connections (doors, bridges) aren't limited unless RateTCP, and
// RateBurst 0 turns the limit off even then.
func TestRateLimitOff(t *testing.T) {
	for name, setup := range map[string]func(*Server){
		"tcp default": nil,
		"burst 0":     func(s *Server) { s.RateTCP, s.RateBurst = true, 0 },
	} {
		_, addr, _ := startWith(t, setup)
		c := dialRaw(t, addr)
		for i := 0; i < 50; i++ {
			c.query("WRITE", room, `,"data":{"str":"x"}`)
			c.query("PUSH", "channels.main.history", `,"data":{"str":"x"}`)
		}
		for _, p := range c.sync() {
			if p.Func == "ERROR" {
				t.Fatalf("%s: limited: %s", name, p.Data)
			}
		}
	}
}

func TestMaxHistory(t *testing.T) {
	st := newStore()
	for i := 0; i < 10; i++ {
		st.Push("chat", "h", json.RawMessage(`{"i":`+string(rune('0'+i))+`}`), 4)
	}
	got := st.Slice("chat", "h", 0, nil)
	if len(got) != 4 || string(got[0]) != `{"i":6}` || string(got[3]) != `{"i":9}` {
		t.Fatalf("capped history: %s", got)
	}
}

// The store survives a restart: history, scalars and door names are saved
// on shutdown and loaded back; subscriptions are not.
func TestStorePersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "store.json")
	srv, addr, stop := startWith(t, func(s *Server) { s.StorePath = path; s.saveEvery = time.Hour })
	if err := srv.LoadStore(); err != nil { // missing file: empty, no error
		t.Fatal(err)
	}
	c := dialRaw(t, addr)
	c.subscribeAs(room, "DoorGuy", "bbs")
	for i := 0; i < 3; i++ {
		c.query("WRITE", room, `,"data":{"str":"hi"}`)
		c.query("PUSH", "channels.main.history", `,"data":{"str":"hi"}`)
	}
	c.query("WRITE", "public_channels.lounge", `,"data":true`)
	c.sync()
	stop() // the save is ListenAndServe's last act

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no snapshot after shutdown: %v", err)
	}
	if strings.Contains(string(b), "SUBSCRIBE") {
		t.Fatalf("snapshot holds subscriptions: %s", b)
	}
	if _, err := os.Stat(path + ".tmp"); err == nil {
		t.Fatal("temp file left behind")
	}

	srv2 := New("127.0.0.1:0")
	srv2.Logger = log.New(io.Discard, "", 0)
	srv2.StorePath = path
	srv2.MaxHistory = 2
	if err := srv2.LoadStore(); err != nil {
		t.Fatal(err)
	}
	if got := srv2.store.Slice("chat", "channels.main.history", 0, nil); len(got) != 2 {
		t.Fatalf("history after reload (capped to 2): %s", got)
	}
	if keys := srv2.Keys("chat", "public_channels"); len(keys) != 1 || keys[0] != "lounge" {
		t.Fatalf("registry after reload: %v", keys)
	}
	if !srv2.BBSName("doorguy") {
		t.Fatal("door name lost across restart")
	}
}

// The debounced save lands without a shutdown.
func TestStoreSavesAfterChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.json")
	_, addr, _ := startWith(t, func(s *Server) { s.StorePath = path; s.saveEvery = 50 * time.Millisecond })
	c := dialRaw(t, addr)
	c.query("PUSH", "channels.main.history", `,"data":{"str":"saved"}`)
	c.sync()
	deadline := time.Now().Add(3 * time.Second)
	for {
		b, _ := os.ReadFile(path)
		if strings.Contains(string(b), "saved") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("no debounced save")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestCorruptStoreMovedAside(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "store.json")
	for _, bad := range []string{`{"scalars":`, `{"version":99}`, `[]`} {
		if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		srv := New("127.0.0.1:0")
		srv.Logger = log.New(io.Discard, "", 0)
		srv.StorePath = path
		srv.now = func() time.Time { return time.Unix(int64(len(bad)), 0) } // a distinct .bad name each
		if err := srv.LoadStore(); err != nil {
			t.Fatalf("%q: %v", bad, err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%q: bad file left in place", bad)
		}
		if len(srv.store.arrays)+len(srv.store.scalars) != 0 {
			t.Fatalf("%q: store not empty", bad)
		}
	}
	m, _ := filepath.Glob(path + ".bad-*")
	if len(m) != 3 {
		t.Fatalf("moved aside: %v", m)
	}
}

// BBSName: a TCP connection's nick, while connected and for NameRetention
// after; never a web (ServeConn) connection's.
func TestBBSName(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	var clock sync.Mutex
	srv, addr, _ := startWith(t, func(s *Server) {
		s.NameRetention = 24 * time.Hour
		s.now = func() time.Time { clock.Lock(); defer clock.Unlock(); return now }
	})
	door := dialRaw(t, addr)
	door.subscribeAs(room, "Sysop Sam", "bbs")
	if !srv.BBSName("sysop sam") || srv.BBSName("nobody") {
		t.Fatal("connected door name")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	in := make(chan []byte, 1)
	in <- []byte(`{"scope":"chat","func":"QUERY","oper":"SUBSCRIBE","location":"` + room + `","nick":"webby","system":"web"}`)
	go srv.ServeConn(ctx, nopWire{}, "web-1", func() ([]byte, error) {
		select {
		case b := <-in:
			return b, nil
		case <-ctx.Done():
			return nil, io.EOF
		}
	})
	door.expect("UPDATE", "SUBSCRIBE") // webby joined
	if srv.BBSName("webby") {
		t.Fatal("a web connection's nick counted as a door's")
	}

	door.c.Close()
	time.Sleep(100 * time.Millisecond)
	clock.Lock()
	now = now.Add(23 * time.Hour)
	clock.Unlock()
	if !srv.BBSName("SYSOP SAM") {
		t.Fatal("door name forgotten within retention")
	}
	clock.Lock()
	now = now.Add(2 * time.Hour)
	clock.Unlock()
	if srv.BBSName("sysop sam") {
		t.Fatal("door name kept past retention")
	}
}

type nopWire struct{}

func (nopWire) WritePacket([]byte) error { return nil }
func (nopWire) Close() error             { return nil }

// READ of a history answers the array, so the JS door's "is it an array
// yet?" check doesn't WRITE [] over it.
func TestReadReturnsArray(t *testing.T) {
	_, addr, _ := startWith(t, nil)
	c := dialRaw(t, addr)
	c.query("PUSH", "channels.main.history", `,"data":{"str":"a"}`)
	c.query("READ", "channels.main.history", ``)
	if p := c.expect("RESPONSE", "READ"); string(p.Data) != `[{"str":"a"}]` {
		t.Fatalf("READ of a history: %s", p.Data)
	}
	c.query("READ", "channels.none.history", ``)
	if p := c.expect("RESPONSE", "READ"); string(p.Data) != `null` {
		t.Fatalf("READ of nothing: %s", p.Data)
	}
}
