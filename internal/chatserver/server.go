package chatserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/denylist"
)

// MaxPacket matches Socket.prototype.max_recv in json-sock.js.
const MaxPacket = 131072

// Defaults New gives the Server's tunables.
const (
	DefaultMaxHistory    = 1000
	DefaultRateBurst     = 10
	DefaultRatePerSec    = 1.0
	DefaultNameRetention = 90 * 24 * time.Hour
)

// slowDown is the ERROR a connection gets for a message over its rate.
const slowDown = "you're sending messages too fast; slow down"

// packet mirrors chat.Packet but lives in this package to avoid an import
// cycle with the client.
type packet struct {
	Scope    string          `json:"scope,omitempty"`
	Func     string          `json:"func,omitempty"`
	Oper     string          `json:"oper,omitempty"`
	Location string          `json:"location,omitempty"`
	Data     json.RawMessage `json:"data,omitempty"`
	Lock     int             `json:"lock,omitempty"`
	Timeout  int             `json:"timeout,omitempty"`
	Nick     string          `json:"nick,omitempty"`
	System   string          `json:"system,omitempty"`
}

// Server holds shared state across connections: the data store and the
// subscription registry.
type Server struct {
	Addr   string
	Logger *log.Logger
	// Deny, when set, refuses connections from listed addresses before
	// anything is read from them. A nil list denies nobody.
	Deny *denylist.List

	// StorePath, when set, is the store's snapshot on disk (persist.go):
	// LoadStore reads it, ListenAndServe saves it a few seconds after a
	// change and once more on shutdown. "" keeps everything in memory.
	StorePath string
	// MaxHistory caps every array PUSH builds (chat history); the oldest
	// entries go. 0 = no cap.
	MaxHistory int
	// Flood control, per connection: a chat message (a WRITE to
	// channels.*.messages, with the PUSHes to channels.*.history that go
	// with it) takes a token from a bucket of RateBurst refilled at
	// RatePerSec. Either at 0 turns it off. It applies to web (ServeConn)
	// connections; TCP ones (doors, bridges) only with RateTCP, since a
	// bridge carries a whole channel and the JS door takes any ERROR for
	// a lost connection.
	RateBurst  int
	RatePerSec float64
	RateTCP    bool
	// NameRetention is how long after a door or bridge last used a nick
	// it stays refused to new web name claims (BBSName). 0 = only while
	// it's connected.
	NameRetention time.Duration

	store *store

	mu          sync.RWMutex
	subs        map[string]map[*conn]bool // scope+location → connections
	connections map[*conn]bool

	saveMu    sync.Mutex
	savedGen  uint64
	saveEvery time.Duration    // debounce between a change and its save
	now       func() time.Time // tests move the clock
}

func New(addr string) *Server {
	return &Server{
		Addr:          addr,
		Logger:        log.Default(),
		MaxHistory:    DefaultMaxHistory,
		RateBurst:     DefaultRateBurst,
		RatePerSec:    DefaultRatePerSec,
		NameRetention: DefaultNameRetention,
		store:         newStore(),
		subs:          map[string]map[*conn]bool{},
		connections:   map[*conn]bool{},
		saveEvery:     5 * time.Second,
		now:           time.Now,
	}
}

// ListenAndServe blocks accepting connections on s.Addr until ctx is canceled
// or the listener errors. With StorePath set it saves the store while it
// runs and once more before it returns.
func (s *Server) ListenAndServe(ctx context.Context) error {
	lc := net.ListenConfig{}
	l, err := lc.Listen(ctx, "tcp", s.Addr)
	if err != nil {
		return fmt.Errorf("chatserver: listen %s: %v", s.Addr, err)
	}
	s.Logger.Printf("chatserver: listening on %s", l.Addr())
	go func() {
		<-ctx.Done()
		_ = l.Close()
	}()
	if s.StorePath != "" {
		go s.persistLoop(ctx)
		defer func() {
			if err := s.saveStore(); err != nil {
				s.Logger.Printf("chatserver: %v", err)
			}
		}()
	}

	for {
		c, err := l.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
			}
			return fmt.Errorf("chatserver: accept: %v", err)
		}
		if s.Deny.Denied(c.RemoteAddr().String()) {
			s.Logger.Printf("chatserver: refused %s (deny list)", c.RemoteAddr())
			_ = c.Close()
			continue
		}
		go s.handleConn(ctx, c)
	}
}

// conn is one client, over TCP or any other Wire (the web front end's
// WebSocket). All share the packet loop, the subscription registry and the
// nick handling; only the wire differs. Writes are serialized through
// writeMu.
type conn struct {
	server  *Server
	wire    Wire
	writeMu sync.Mutex

	id     string // remote addr, used in logs and as a default nick
	web    bool   // came through ServeConn (the web front end), not TCP
	nick   string // nick and system change under server.mu
	system string

	subs map[string]subLoc // under server.mu

	// Flood control (allow); only the connection's own loop touches these.
	tokens     float64
	lastRefill time.Time
	pushCredit int  // history PUSHes the last message WRITE paid for
	dropPush   bool // the last message WRITE was refused; so is its PUSH

	closed  bool
	closeMu sync.Mutex
}

// Wire carries whole packets out: a CRLF-terminated line on TCP, one text
// frame on WebSocket. WritePacket is never called concurrently.
type Wire interface {
	WritePacket(body []byte) error
	Close() error
}

type tcpWire struct {
	c net.Conn
	w *bufio.Writer
}

func (t *tcpWire) WritePacket(body []byte) error {
	_ = t.c.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if _, err := t.w.Write(body); err != nil {
		return err
	}
	if _, err := t.w.WriteString("\r\n"); err != nil {
		return err
	}
	return t.w.Flush()
}

func (t *tcpWire) Close() error { return t.c.Close() }

// subLoc is one subscription's scope and location.
type subLoc struct{ scope, location string }

func (s *Server) handleConn(ctx context.Context, tcp net.Conn) {
	c := &conn{
		server: s,
		wire:   &tcpWire{c: tcp, w: bufio.NewWriter(tcp)},
		id:     tcp.RemoteAddr().String(),
		subs:   map[string]subLoc{},
	}
	// A line, CRLF included, must fit the buffer; a longer one drops the
	// connection. The slice is only good until the next read, and serve
	// is done with a chunk before it asks for the next.
	r := bufio.NewReaderSize(tcp, MaxPacket+2)
	s.serve(ctx, c, func() ([]byte, error) {
		line, err := r.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			s.Logger.Printf("chatserver: %s sent a packet over %d bytes; dropping the connection", c.id, MaxPacket)
		}
		return line, err
	})
}

// ServeConn runs a client that arrived some other way than the TCP
// listener (the web front end's WebSocket) until next fails or ctx ends.
// id names it in WHO when it never gives a nick. next returns the next
// chunk off the wire; a chunk may hold several newline-separated packets.
// Its nick never counts as a door's (BBSName).
func (s *Server) ServeConn(ctx context.Context, w Wire, id string, next func() ([]byte, error)) {
	s.serve(ctx, &conn{server: s, wire: w, id: id, web: true, subs: map[string]subLoc{}}, next)
}

// serve runs one connection's packet loop until next fails or ctx ends.
func (s *Server) serve(ctx context.Context, c *conn, next func() ([]byte, error)) {
	s.mu.Lock()
	s.connections[c] = true
	s.mu.Unlock()

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = c.wire.Close() // unblocks next
		case <-done:
		}
	}()
	defer func() {
		close(done)
		_ = c.wire.Close()
		s.dropAllSubs(c)
		s.mu.Lock()
		delete(s.connections, c)
		s.mu.Unlock()
	}()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		chunk, err := next()
		if err != nil {
			return
		}
		for _, line := range bytes.Split(chunk, []byte{'\n'}) {
			line = bytes.TrimRight(line, "\r")
			if len(line) == 0 {
				continue
			}
			var pkt packet
			if err := json.Unmarshal(line, &pkt); err != nil {
				s.sendError(c, "malformed JSON")
				continue
			}
			s.dispatch(c, &pkt)
		}
	}
}

func (s *Server) dispatch(c *conn, pkt *packet) {
	if pkt.Scope == "SOCKET" {
		switch pkt.Func {
		case "PING":
			c.send(&packet{Scope: "SOCKET", Func: "PONG", Data: pkt.Data})
		case "PONG":
			// Drop.
		}
		return
	}
	if pkt.Func != "QUERY" {
		s.sendError(c, "unknown func: "+pkt.Func)
		return
	}

	if !s.allow(c, pkt) {
		return
	}

	switch pkt.Oper {
	case "WRITE":
		s.opWrite(c, pkt)
	case "PUSH":
		s.opPush(c, pkt)
	case "READ":
		s.opRead(c, pkt)
	case "SLICE":
		s.opSlice(c, pkt)
	case "SUBSCRIBE":
		s.opSubscribe(c, pkt)
	case "UNSUBSCRIBE":
		s.opUnsubscribe(c, pkt)
	case "WHO":
		s.opWho(c, pkt)
	case "STATUS":
		s.respond(c, pkt, json.RawMessage(`{"locked":false}`))
	case "KEYS":
		keys := s.store.Keys(pkt.Scope, pkt.Location)
		raw, _ := json.Marshal(keys)
		s.respond(c, pkt, raw)
	default:
		s.sendError(c, "unsupported oper: "+pkt.Oper)
	}
}

func (s *Server) opWrite(c *conn, pkt *packet) {
	s.store.Write(pkt.Scope, pkt.Location, pkt.Data)
	s.respond(c, pkt, pkt.Data)
	s.fanOut(c, pkt, "WRITE", pkt.Data)
}

func (s *Server) opPush(c *conn, pkt *packet) {
	n := s.store.Push(pkt.Scope, pkt.Location, pkt.Data, s.MaxHistory)
	resp, _ := json.Marshal(n)
	s.respond(c, pkt, resp)
}

func (s *Server) opRead(c *conn, pkt *packet) {
	v := s.store.Read(pkt.Scope, pkt.Location)
	if v == nil {
		v = json.RawMessage("null")
	}
	s.respond(c, pkt, v)
}

func (s *Server) opSlice(c *conn, pkt *packet) {
	type sliceArgs struct {
		Start int  `json:"start"`
		End   *int `json:"end,omitempty"`
	}
	var args sliceArgs
	if len(pkt.Data) > 0 {
		_ = json.Unmarshal(pkt.Data, &args)
	}
	out := s.store.Slice(pkt.Scope, pkt.Location, args.Start, args.End)
	if out == nil {
		out = []json.RawMessage{}
	}
	raw, err := json.Marshal(out)
	if err != nil {
		s.sendError(c, "marshal slice: "+err.Error())
		return
	}
	s.respond(c, pkt, raw)
}

func (s *Server) opSubscribe(c *conn, pkt *packet) {
	key := storeKey(pkt.Scope, pkt.Location)
	s.mu.Lock()
	if s.subs[key] == nil {
		s.subs[key] = map[*conn]bool{}
	}
	s.subs[key][c] = true
	c.subs[key] = subLoc{pkt.Scope, pkt.Location}
	if pkt.Nick != "" {
		c.nick = pkt.Nick
	}
	if pkt.System != "" {
		c.system = pkt.System
	}
	s.mu.Unlock()
	if !c.web && c.nick != "" {
		s.store.seeName(c.nick, s.now())
	}
	// Notify other subscribers that someone joined.
	joinData, _ := json.Marshal(map[string]string{"nick": c.nick, "system": c.system})
	s.fanOut(c, pkt, "SUBSCRIBE", joinData)
	s.respond(c, pkt, json.RawMessage(`true`))
}

func (s *Server) opUnsubscribe(c *conn, pkt *packet) {
	key := storeKey(pkt.Scope, pkt.Location)
	s.mu.Lock()
	if m, ok := s.subs[key]; ok {
		delete(m, c)
		if len(m) == 0 {
			delete(s.subs, key)
		}
	}
	delete(c.subs, key)
	s.mu.Unlock()
	leaveData, _ := json.Marshal(map[string]string{"nick": c.nick, "system": c.system})
	s.fanOut(c, pkt, "UNSUBSCRIBE", leaveData)
	s.respond(c, pkt, json.RawMessage(`true`))
}

func (s *Server) opWho(c *conn, pkt *packet) {
	key := storeKey(pkt.Scope, pkt.Location)
	s.mu.RLock()
	subs := s.subs[key]
	// Synchronet's json-db answers WHO with {id, nick, system} per
	// subscriber; the same shape here, without the id (it's the peer's
	// address in this server).
	type whoEntry struct {
		Nick   string `json:"nick"`
		System string `json:"system,omitempty"`
	}
	out := make([]whoEntry, 0, len(subs))
	for sub := range subs {
		who := sub.nick
		if who == "" {
			who = "guest"
		}
		out = append(out, whoEntry{Nick: who, System: sub.system})
	}
	s.mu.RUnlock()
	raw, _ := json.Marshal(out)
	s.respond(c, pkt, raw)
}

// fanOut sends an UPDATE packet to every subscriber of pkt.Location except
// the originating connection.
func (s *Server) fanOut(origin *conn, pkt *packet, oper string, data json.RawMessage) {
	key := storeKey(pkt.Scope, pkt.Location)
	s.mu.RLock()
	targets := make([]*conn, 0, len(s.subs[key]))
	for sub := range s.subs[key] {
		if sub == origin {
			continue
		}
		targets = append(targets, sub)
	}
	s.mu.RUnlock()

	upd := &packet{
		Scope:    pkt.Scope,
		Func:     "UPDATE",
		Oper:     oper,
		Location: pkt.Location,
		Data:     data,
	}
	for _, t := range targets {
		t.send(upd)
	}
}

// dropAllSubs removes a closed connection's subscriptions and tells each
// location's other subscribers it left, the UNSUBSCRIBE update they'd get
// had it unsubscribed itself (json-db does the same when a client goes).
// No notice goes where another connection with the same nick and system
// is still subscribed: the same user in a second window or door hasn't
// left.
func (s *Server) dropAllSubs(c *conn) {
	var left []subLoc
	s.mu.Lock()
	for key, loc := range c.subs {
		m := s.subs[key]
		delete(m, c)
		if len(m) == 0 {
			delete(s.subs, key)
		}
		twin := false
		for other := range m {
			if strings.EqualFold(other.nick, c.nick) && strings.EqualFold(other.system, c.system) {
				twin = true
				break
			}
		}
		if !twin {
			left = append(left, loc)
		}
	}
	c.subs = map[string]subLoc{}
	s.mu.Unlock()

	if !c.web && c.nick != "" {
		s.store.seeName(c.nick, s.now())
	}
	if len(left) == 0 {
		return
	}
	leaveData, _ := json.Marshal(map[string]string{"nick": c.nick, "system": c.system})
	for _, loc := range left {
		s.fanOut(c, &packet{Scope: loc.scope, Location: loc.location}, "UNSUBSCRIBE", leaveData)
	}
}

// allow is flood control: whether pkt may go ahead. A chat message, a
// WRITE to channels.<x>.messages, takes a token from the connection's
// bucket and pays for the next two PUSHes to a channels.<y>.history (the
// history copy, and the sender's own copy of a web PM), so a message
// costs one token however a client stores it. A PUSH with nothing paid
// for takes a token of its own. Over the limit the client gets one
// ERROR per message and the op is dropped; a refused WRITE's PUSH is
// dropped without a second ERROR. Everything else (SUBSCRIBE, WHO, SLICE,
// KEYS, TV tuner and room registry writes) is never limited, and TCP
// connections only with RateTCP.
func (s *Server) allow(c *conn, pkt *packet) bool {
	if s.RateBurst <= 0 || s.RatePerSec <= 0 || (!c.web && !s.RateTCP) || !strings.HasPrefix(pkt.Location, "channels.") {
		return true
	}
	switch {
	case pkt.Oper == "WRITE" && strings.HasSuffix(pkt.Location, ".messages"):
		if !c.take(s.now(), s.RateBurst, s.RatePerSec) {
			c.pushCredit, c.dropPush = 0, true
			s.sendError(c, slowDown)
			return false
		}
		c.pushCredit, c.dropPush = 2, false
	case pkt.Oper == "PUSH" && strings.HasSuffix(pkt.Location, ".history"):
		switch {
		case c.dropPush:
			c.dropPush = false
			return false
		case c.pushCredit > 0:
			c.pushCredit--
		case !c.take(s.now(), s.RateBurst, s.RatePerSec):
			s.sendError(c, slowDown)
			return false
		}
	}
	return true
}

// take takes a token from c's bucket, refilled at perSec up to burst.
func (c *conn) take(now time.Time, burst int, perSec float64) bool {
	if c.lastRefill.IsZero() {
		c.tokens = float64(burst)
	} else if d := now.Sub(c.lastRefill); d > 0 {
		c.tokens += d.Seconds() * perSec
		if c.tokens > float64(burst) {
			c.tokens = float64(burst)
		}
	}
	c.lastRefill = now
	if c.tokens < 1 {
		return false
	}
	c.tokens--
	return true
}

// BBSName reports whether a door or bridge (a TCP connection, not the web
// front end) has nick right now, or used it within NameRetention. Web
// name claims refuse such a name to a browser that doesn't already hold
// it. Names compare without case.
func (s *Server) BBSName(nick string) bool {
	nick = strings.TrimSpace(nick)
	if nick == "" {
		return false
	}
	s.mu.RLock()
	for c := range s.connections {
		if !c.web && strings.EqualFold(c.nick, nick) {
			s.mu.RUnlock()
			return true
		}
	}
	s.mu.RUnlock()
	return s.NameRetention > 0 && s.store.nameSeenSince(nick, s.now().Add(-s.NameRetention))
}

func (s *Server) respond(c *conn, pkt *packet, data json.RawMessage) {
	c.send(&packet{
		Scope:    pkt.Scope,
		Func:     "RESPONSE",
		Oper:     pkt.Oper,
		Location: pkt.Location,
		Data:     data,
	})
}

func (s *Server) sendError(c *conn, desc string) {
	body, _ := json.Marshal(map[string]string{"description": desc})
	c.send(&packet{Func: "ERROR", Data: body})
}

func (c *conn) send(pkt *packet) {
	c.closeMu.Lock()
	if c.closed {
		c.closeMu.Unlock()
		return
	}
	c.closeMu.Unlock()

	body, err := json.Marshal(pkt)
	if err != nil {
		return
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.wire.WritePacket(body); err != nil {
		c.markClosed()
	}
}

// markClosed stops further writes and drops the connection, so its read
// loop ends and its subscriptions go.
func (c *conn) markClosed() {
	c.closeMu.Lock()
	c.closed = true
	c.closeMu.Unlock()
	_ = c.wire.Close()
}

// helpers used by tests
var _ io.Reader = (*bufio.Reader)(nil)

// Keys lists the immediate child keys at scope.location, as the KEYS verb
// answers them. The web front end reads the public_channels registry
// through it.
func (s *Server) Keys(scope, location string) []string {
	return s.store.Keys(scope, location)
}
