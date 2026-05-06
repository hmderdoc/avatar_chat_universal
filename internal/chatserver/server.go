package chatserver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"
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
		return fmt.Errorf("chatserver: listen %s: %w", s.Addr, err)
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
			return fmt.Errorf("chatserver: accept: %w", err)
		}
		go s.handleConn(ctx, c)
	}
}

// conn wraps a single TCP connection. Writes are serialized through writeMu.
type conn struct {
	server  *Server
	tcp     net.Conn
	writeMu sync.Mutex
	writer  *bufio.Writer

	id     string // remote addr, used in logs and as a default nick
	nick   string
	system string

	subs map[string]bool

	closed bool
	closeMu sync.Mutex
}

func (s *Server) handleConn(ctx context.Context, tcp net.Conn) {
	c := &conn{
		server: s,
		tcp:    tcp,
		writer: bufio.NewWriter(tcp),
		id:     tcp.RemoteAddr().String(),
		subs:   map[string]bool{},
	}
	s.mu.Lock()
	s.connections[c] = true
	s.mu.Unlock()

	defer func() {
		_ = tcp.Close()
		s.dropAllSubs(c)
		s.mu.Lock()
		delete(s.connections, c)
		s.mu.Unlock()
	}()

	r := bufio.NewReaderSize(tcp, MaxPacket)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		line, err := r.ReadBytes('\n')
		if err != nil {
			return
		}
		for len(line) > 0 && (line[len(line)-1] == '\n' || line[len(line)-1] == '\r') {
			line = line[:len(line)-1]
		}
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
	if _, err := c.writer.Write(body); err != nil {
		c.markClosed()
		return
	}
	if _, err := c.writer.WriteString("\r\n"); err != nil {
		c.markClosed()
		return
	}
	if err := c.writer.Flush(); err != nil {
		c.markClosed()
	}
	_ = c.tcp.SetWriteDeadline(time.Now().Add(10 * time.Second))
}

func (c *conn) markClosed() {
	c.closeMu.Lock()
	c.closed = true
	c.closeMu.Unlock()
}

// helpers used by tests
var _ io.Reader = (*bufio.Reader)(nil)
