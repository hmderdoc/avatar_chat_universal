// Package telnetvision is a consumer client for the telnetvision fanout
// service (github.com/hmderdoc/telnetvision): it subscribes to a channel and
// exposes the latest video frame for the door to render behind the chat (the
// "TV lounge" mode). Drop-to-latest: a slow renderer just gets the newest
// frame, never a backlog.
//
// Wire format (telnetvision/wire.py): each message is uint32 big-endian length
// + payload; payload[0] is the type.
//
//	HELLO_CONSUMER 0x02: 0x02 | u8 chanLen | channel
//	                     | [u8 caps]  (v2: bit0 = wants AUDIO)
//	FRAME          0x10: 0x10 | u16 cols | u16 rows | u8 mode | u8 ramp
//	                     | u16 capLen | caption[capLen] | pixels[2*rows*cols*3]
//	                     RGB, row-major; 2*rows pixel rows (top+bottom per cell).
//	AUDIO          0x11: 0x11 | u8 codec | u8 channels | u32 rate | u32 seq
//	                     | u64 pts_ms | u32 dur_ms | payload  (v2)
package telnetvision

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/apcaudio"
)

const (
	msgHelloConsumer = 0x02
	msgFrame         = 0x10
	msgAudio         = 0x11
	capAudio         = 0x01 // hello caps bit: forward AUDIO to this consumer
	// audioQueueMax bounds held chunks if the render loop stalls; the
	// player's own ledger is the real backpressure.
	audioQueueMax = 64
	maxMsg           = 16 << 20 // RGB frames are tens of KiB; cap defensively
	reconnectDelay   = 3 * time.Second
)

// Frame is one decoded video frame.
type Frame struct {
	Cols, Rows int
	Mode, Ramp byte
	Caption    string
	Pixels     []byte // len == 2*Rows*Cols*3, RGB row-major (top+bottom per cell)
}

// Consumer subscribes to one telnetvision channel and keeps the latest frame.
type Consumer struct {
	addr    string
	channel string
	// WantAudio asks the relay for the channel's AUDIO messages (set before
	// Start). Off, the relay never sends them.
	WantAudio bool

	mu     sync.Mutex
	latest *Frame
	audio  []apcaudio.Chunk
	conn   net.Conn
	closed bool
	connErr error
}

// NewConsumer builds (but does not start) a consumer for host:port / channel.
func NewConsumer(host string, port int, channel string) *Consumer {
	return &Consumer{addr: net.JoinHostPort(host, strconv.Itoa(port)), channel: channel}
}

// Start dials and begins the read loop in the background, reconnecting until
// Close is called. Safe to call once.
func (c *Consumer) Start() { go c.run() }

func (c *Consumer) run() {
	for {
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return
		}
		c.mu.Unlock()

		conn, err := net.DialTimeout("tcp", c.addr, 10*time.Second)
		if err != nil {
			c.setErr(err)
			if c.sleepOrDone(reconnectDelay) {
				return
			}
			continue
		}
		c.mu.Lock()
		c.conn = conn
		c.connErr = nil
		closed := c.closed
		c.mu.Unlock()
		if closed {
			conn.Close()
			return
		}
		if err := helloConsumer(conn, c.channel, c.WantAudio); err != nil {
			c.setErr(err)
			conn.Close()
			if c.sleepOrDone(reconnectDelay) {
				return
			}
			continue
		}
		c.readLoop(conn)
		conn.Close()
		if c.sleepOrDone(reconnectDelay) {
			return
		}
	}
}

func (c *Consumer) readLoop(conn net.Conn) {
	for {
		payload, err := readMsg(conn)
		if err != nil {
			c.setErr(err)
			return
		}
		if m, ok := decodeAudio(payload); ok {
			if c.WantAudio {
				c.mu.Lock()
				c.audio = append(c.audio, m)
				if len(c.audio) > audioQueueMax {
					c.audio = c.audio[len(c.audio)-audioQueueMax:]
				}
				c.mu.Unlock()
			}
			continue
		}
		fr, ok := decodeFrame(payload)
		if !ok {
			continue
		}
		c.mu.Lock()
		c.latest = fr
		c.mu.Unlock()
	}
}

func (c *Consumer) sleepOrDone(d time.Duration) (done bool) {
	t := time.NewTimer(d)
	defer t.Stop()
	<-t.C
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *Consumer) setErr(err error) {
	c.mu.Lock()
	c.connErr = err
	c.mu.Unlock()
}

// Latest returns the most recent frame, or nil if none yet.
func (c *Consumer) Latest() *Frame {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.latest
}

// TakeAudio returns the AUDIO chunks received since the last call, oldest
// first.
func (c *Consumer) TakeAudio() []apcaudio.Chunk {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.audio
	c.audio = nil
	return out
}

// Err returns the last connection error (nil while connected/healthy).
func (c *Consumer) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connErr
}

// Close stops the consumer and its read loop.
func (c *Consumer) Close() {
	c.mu.Lock()
	c.closed = true
	conn := c.conn
	c.mu.Unlock()
	if conn != nil {
		conn.Close()
	}
}

func helloConsumer(conn net.Conn, channel string, wantAudio bool) error {
	ch := []byte(channel)
	if len(ch) > 255 {
		return fmt.Errorf("telnetvision: channel name too long")
	}
	payload := append([]byte{msgHelloConsumer, byte(len(ch))}, ch...)
	if wantAudio {
		payload = append(payload, capAudio) // v1 relays ignore the trailing byte
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(payload)))
	_, err := conn.Write(append(hdr[:], payload...))
	return err
}

func readMsg(r io.Reader) ([]byte, error) {
	var lenbuf [4]byte
	if _, err := io.ReadFull(r, lenbuf[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(lenbuf[:])
	if n == 0 || n > maxMsg {
		return nil, fmt.Errorf("telnetvision: bad message length %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// decodeAudio parses an AUDIO payload.
func decodeAudio(p []byte) (apcaudio.Chunk, bool) {
	const hdr = 1 + 1 + 1 + 4 + 4 + 8 + 4
	if len(p) < hdr || p[0] != msgAudio {
		return apcaudio.Chunk{}, false
	}
	return apcaudio.Chunk{
		Codec:      p[1],
		Channels:   p[2],
		SampleRate: binary.BigEndian.Uint32(p[3:7]),
		Seq:        binary.BigEndian.Uint32(p[7:11]),
		PtsMs:      binary.BigEndian.Uint64(p[11:19]),
		DurMs:      binary.BigEndian.Uint32(p[19:23]),
		Payload:    p[23:],
	}, true
}

// decodeFrame parses a FRAME payload. Returns (nil,false) for non-frame or
// malformed messages.
func decodeFrame(p []byte) (*Frame, bool) {
	if len(p) < 9 || p[0] != msgFrame {
		return nil, false
	}
	cols := int(binary.BigEndian.Uint16(p[1:3]))
	rows := int(binary.BigEndian.Uint16(p[3:5]))
	mode := p[5]
	ramp := p[6]
	capLen := int(binary.BigEndian.Uint16(p[7:9]))
	rest := p[9:]
	if capLen > len(rest) {
		return nil, false
	}
	caption := string(rest[:capLen])
	pixels := rest[capLen:]
	if cols <= 0 || rows <= 0 || len(pixels) < 2*rows*cols*3 {
		return nil, false
	}
	return &Frame{Cols: cols, Rows: rows, Mode: mode, Ramp: ramp, Caption: caption, Pixels: pixels[:2*rows*cols*3]}, true
}
