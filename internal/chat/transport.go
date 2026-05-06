package chat

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

const (
	// MaxPacket matches Socket.prototype.max_recv in json-sock.js (131072 bytes).
	MaxPacket = 131072
	// PingInterval is how often we send PINGs ourselves (defensive: server
	// also pings us). json-client.js:81 uses 60s.
	PingInterval = 60 * time.Second
)

// Conn is a JSON-RPC line transport over a single TCP connection. It owns
// the read goroutine; callers receive parsed packets via Updates() and reply
// matches via the synchronous request methods on Client (built on top).
type Conn struct {
	addr string

	mu     sync.Mutex
	tcp    net.Conn
	wbuf   *bufio.Writer
	closed bool

	// Read pipeline. Set up at Open, torn down on Close.
	responses chan *Packet // RESPONSE / ERROR for in-flight requests
	updates   chan *Packet // UPDATE for asynchronous events
	readErr   chan error   // surfaces read-loop fatal errors
}

// NewConn builds an unconnected Conn. Call Open(ctx) to dial.
//
// The responses buffer is sized to hold many in-flight server responses so
// fire-and-forget acks (WRITE/PUSH) don't overflow before a query operation
// has a chance to drain them.
func NewConn(addr string) *Conn {
	return &Conn{
		addr:      addr,
		responses: make(chan *Packet, 64),
		updates:   make(chan *Packet, 256),
		readErr:   make(chan error, 1),
	}
}

// Open dials the server and starts the read goroutine. If called on a Conn
// that was previously open and then died (closed channels, dropped socket),
// fresh internal state is allocated so callers don't have to instantiate a
// new Conn -- they just call Open again. Note that any existing channel
// reference (returned by an earlier Updates()) becomes stale after reopen;
// callers must re-fetch via Updates() after a successful Open.
func (c *Conn) Open(ctx context.Context) error {
	d := net.Dialer{Timeout: 10 * time.Second}
	tcp, err := d.DialContext(ctx, "tcp", c.addr)
	if err != nil {
		return fmt.Errorf("chat: dial %s: %w", c.addr, err)
	}
	c.mu.Lock()
	if c.tcp != nil && c.closed {
		// Reopen path: previous connection is dead. Allocate fresh
		// channels so old subscribers can detect closure (their reads
		// return !ok) while new subscribers see a live pipeline.
		c.responses = make(chan *Packet, 64)
		c.updates = make(chan *Packet, 256)
		c.readErr = make(chan error, 1)
	}
	c.tcp = tcp
	c.wbuf = bufio.NewWriter(tcp)
	c.closed = false
	c.mu.Unlock()

	go c.readLoop()
	return nil
}

// IsAlive reports whether the underlying socket is currently connected.
func (c *Conn) IsAlive() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tcp != nil && !c.closed
}

// Close terminates the connection and unblocks any read.
func (c *Conn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	if c.tcp != nil {
		return c.tcp.Close()
	}
	return nil
}

// Send writes a single JSON packet followed by "\r\n". Concurrent sends are
// serialized by the connection mutex.
func (c *Conn) Send(p *Packet) error {
	data, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("chat: marshal: %w", err)
	}
	if len(data)+2 > MaxPacket {
		return fmt.Errorf("chat: packet too large (%d bytes; max %d)", len(data)+2, MaxPacket)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.wbuf == nil {
		return fmt.Errorf("chat: connection closed")
	}
	if _, err := c.wbuf.Write(data); err != nil {
		return err
	}
	if _, err := c.wbuf.WriteString("\r\n"); err != nil {
		return err
	}
	return c.wbuf.Flush()
}

// Updates returns the channel of asynchronous UPDATE packets. The chat layer
// drains it from its main poll loop.
func (c *Conn) Updates() <-chan *Packet { return c.updates }

// readLoop is the single goroutine that owns the read side of c.tcp. It
// classifies inbound packets and dispatches them to responses/updates, or
// auto-replies to PINGs.
func (c *Conn) readLoop() {
	defer func() {
		// On exit, close updates so the chat layer's range loop terminates.
		close(c.updates)
		close(c.responses)
	}()

	r := bufio.NewReaderSize(c.tcp, MaxPacket)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			select {
			case c.readErr <- err:
			default:
			}
			return
		}
		// Strip trailing \r\n
		for len(line) > 0 && (line[len(line)-1] == '\n' || line[len(line)-1] == '\r') {
			line = line[:len(line)-1]
		}
		if len(line) == 0 {
			continue
		}
		var pkt Packet
		if err := json.Unmarshal(line, &pkt); err != nil {
			// Malformed packet — log and skip. Don't kill the connection.
			continue
		}
		c.dispatch(&pkt)
	}
}

func (c *Conn) dispatch(pkt *Packet) {
	switch pkt.Func {
	case "PING":
		_ = c.sendPong()
		return
	case "PONG":
		// No latency tracking yet; just drop it.
		return
	case "RESPONSE", "ERROR":
		// Hand to whoever's waiting. If the queue is full, drop the
		// oldest entry to make room — newest is most likely to be the
		// one we care about.
		select {
		case c.responses <- pkt:
		default:
			select {
			case <-c.responses:
			default:
			}
			select {
			case c.responses <- pkt:
			default:
			}
		}
		return
	case "UPDATE":
		select {
		case c.updates <- pkt:
		default:
			// Updates buffer full; drop oldest. (TODO: log / instrument)
			select {
			case <-c.updates:
			default:
			}
			c.updates <- pkt
		}
		return
	}
	// Unknown packet — drop quietly.
}

func (c *Conn) sendPong() error {
	now := time.Now().UnixMilli()
	data, _ := json.Marshal(now)
	return c.Send(&Packet{Scope: "SOCKET", Func: "PONG", Data: data})
}

// DrainResponses discards any RESPONSE/ERROR packets currently sitting in
// the queue. Call this before issuing a query whose response you intend to
// AwaitResponse — otherwise a previous fire-and-forget WRITE/PUSH could
// leave a stale RESPONSE that gets returned instead of yours.
func (c *Conn) DrainResponses() {
	for {
		select {
		case <-c.responses:
		default:
			return
		}
	}
}

// AwaitResponse blocks until a RESPONSE matching expectedOper arrives, the
// timeout elapses, or the connection breaks. ERROR packets always match.
// Pass an empty expectedOper to match any RESPONSE.
//
// The oper-matching is the trick that makes fire-and-forget WRITE/PUSH
// coexist with query ops like SLICE: the response echoes the request's
// oper, so a stale RESPONSE for PUSH won't be returned when the caller
// asked for SLICE.
func (c *Conn) AwaitResponse(expectedOper string, timeout time.Duration) (*Packet, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case pkt, ok := <-c.responses:
			if !ok {
				return nil, io.EOF
			}
			if pkt.Func == "ERROR" {
				var ep ErrorPayload
				if err := json.Unmarshal(pkt.Data, &ep); err == nil && ep.Description != "" {
					return nil, fmt.Errorf("chat: %s", ep.Description)
				}
				return nil, fmt.Errorf("chat: server error")
			}
			if expectedOper != "" && !equalFold(pkt.Oper, expectedOper) {
				continue
			}
			return pkt, nil
		case err := <-c.readErr:
			return nil, err
		case <-timer.C:
			return nil, fmt.Errorf("chat: timeout waiting for %s response", expectedOper)
		}
	}
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 32
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 32
		}
		if ca != cb {
			return false
		}
	}
	return true
}
