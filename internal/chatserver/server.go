package chatserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/denylist"
)

// MaxPacket matches Socket.prototype.max_recv in json-sock.js.
const MaxPacket = 131072

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

	store *store

	mu          sync.RWMutex
	subs        map[string]map[*conn]bool // scope+location → connections
	connections map[*conn]bool
}

func New(addr string) *Server {
	return &Server{
		Addr:        addr,
		Logger:      log.Default(),
		store:       newStore(),
		subs:        map[string]map[*conn]bool{},
		connections: map[*conn]bool{},
	}
}

// ListenAndServe blocks accepting connections on s.Addr until ctx is canceled
// or the listener errors.
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
	nick   string
	system string

	subs map[string]bool

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

func (s *Server) handleConn(ctx context.Context, tcp net.Conn) {
	c := &conn{
		server: s,
		wire:   &tcpWire{c: tcp, w: bufio.NewWriter(tcp)},
		id:     tcp.RemoteAddr().String(),
		subs:   map[string]bool{},
	}
	r := bufio.NewReaderSize(tcp, MaxPacket)
	s.serve(ctx, c, func() ([]byte, error) { return r.ReadBytes('\n') })
}

// ServeConn runs a client that arrived some other way than the TCP
// listener (the web front end's WebSocket) until next fails or ctx ends.
// id names it in WHO when it never gives a nick. next returns the next
// chunk off the wire; a chunk may hold several newline-separated packets.
func (s *Server) ServeConn(ctx context.Context, w Wire, id string, next func() ([]byte, error)) {
	s.serve(ctx, &conn{server: s, wire: w, id: id, subs: map[string]bool{}}, next)
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
	n := s.store.Push(pkt.Scope, pkt.Location, pkt.Data)
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
	c.subs[key] = true
	s.mu.Unlock()
	if pkt.Nick != "" {
		c.nick = pkt.Nick
	}
	if pkt.System != "" {
		c.system = pkt.System
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
	out := make([]string, 0, len(subs))
	for sub := range subs {
		who := sub.nick
		if who == "" {
			who = sub.id
		}
		out = append(out, who)
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

func (s *Server) dropAllSubs(c *conn) {
	s.mu.Lock()
	for key := range c.subs {
		if m, ok := s.subs[key]; ok {
			delete(m, c)
			if len(m) == 0 {
				delete(s.subs, key)
			}
		}
	}
	c.subs = map[string]bool{}
	s.mu.Unlock()
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
