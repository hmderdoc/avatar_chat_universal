package webchat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/chatserver"
)

// relay connects one browser to Config.Upstream over its own TCP socket,
// the line-delimited JSON a door speaks, until either side goes. Packets
// from the browser have passed the policy; the upstream's SOCKET PINGs are
// answered here (a browser that's gone quiet is the WebSocket keepalive's
// business), everything else it sends goes to the browser as is.
func (s *Server) relay(ctx context.Context, out *wsOut, remote string, next func() ([]byte, error)) {
	d := net.Dialer{Timeout: 10 * time.Second}
	up, err := d.DialContext(ctx, "tcp", s.cfg.Upstream)
	if err != nil {
		s.Logger.Printf("web: %s: upstream %s: %v", remote, s.cfg.Upstream, err)
		_ = out.WritePacket(errorPacket("the chat server can't be reached right now"))
		return
	}
	defer up.Close()

	var upMu sync.Mutex
	upWrite := func(line []byte) error {
		upMu.Lock()
		defer upMu.Unlock()
		_ = up.SetWriteDeadline(time.Now().Add(wsWriteWait))
		_, err := up.Write(append(line, '\r', '\n'))
		return err
	}

	upDone := make(chan struct{})
	go func() {
		defer close(upDone)
		defer out.Close() // the browser's read fails, ending the loop below
		r := bufio.NewReaderSize(up, chatserver.MaxPacket+2)
		for {
			line, err := r.ReadSlice('\n')
			if errors.Is(err, bufio.ErrBufferFull) {
				s.Logger.Printf("web: %s: upstream sent a packet over %d bytes; closing", remote, chatserver.MaxPacket)
				return
			}
			if err != nil {
				return
			}
			line = bytes.TrimSpace(line)
			if len(line) == 0 {
				continue
			}
			if pong, ok := pingReply(line); ok {
				if upWrite(pong) != nil {
					return
				}
				continue
			}
			if out.WritePacket(append([]byte(nil), line...)) != nil {
				return
			}
		}
	}()

	for {
		chunk, err := next()
		if err != nil {
			break
		}
		failed := false
		for _, line := range bytes.Split(chunk, []byte{'\n'}) {
			if err := upWrite(line); err != nil {
				failed = true
				break
			}
		}
		if failed {
			break
		}
	}
	_ = up.Close()
	<-upDone
}

// pingReply answers a SOCKET PING with its PONG.
func pingReply(line []byte) ([]byte, bool) {
	if !bytes.Contains(line, []byte(`"PING"`)) {
		return nil, false
	}
	var p packet
	if json.Unmarshal(line, &p) != nil || p.Scope != "SOCKET" || p.Func != "PING" {
		return nil, false
	}
	b, _ := json.Marshal(packet{Scope: "SOCKET", Func: "PONG", Data: p.Data})
	return b, true
}
