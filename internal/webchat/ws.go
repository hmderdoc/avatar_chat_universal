package webchat

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/hmderdoc/avatar_chat_universal/internal/chatserver"
)

// /chat: each WebSocket frame carries one JSON packet, as a TCP line does
// (a trailing newline is allowed and a frame holding several
// newline-separated packets is read as several; text and binary frames are
// read alike). Every packet out is one text frame with no line ending.
// The server pings every pingEvery and drops a browser that sends nothing,
// not even a pong, for pongWait. Packets over chatserver.MaxPacket close
// the socket with 1009.

const wsWriteWait = 10 * time.Second

func (s *Server) upgrader() *websocket.Upgrader {
	return &websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		CheckOrigin:     s.originAllowed,
		// websocketservice.js only completes a handshake offering "binary"
		// or "plain"; accepting them here lets one page use either.
		Subprotocols: []string{"binary", "plain"},
	}
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	if !websocket.IsWebSocketUpgrade(r) {
		http.Error(w, "this is the chat's WebSocket; open / in a browser", http.StatusBadRequest)
		return
	}
	if !s.originAllowed(r) {
		s.Logger.Printf("web: refused /chat from %s: origin %q", r.RemoteAddr, r.Header.Get("Origin"))
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}
	ip := remoteIP(r)
	release, why := s.acquire(ip)
	if release == nil {
		http.Error(w, why, http.StatusServiceUnavailable)
		return
	}
	defer release()
	ws, err := s.upgrader().Upgrade(w, r, nil) // writes its own error reply
	if err != nil {
		s.Logger.Printf("web: upgrade from %s: %v", r.RemoteAddr, err)
		return
	}
	s.serveWS(r.Context(), ws, r.RemoteAddr, ip)
}

// wsOut writes packets to one browser. Replies the policy makes on the
// read side and packets from the chat side may race, hence the lock.
type wsOut struct {
	ws   *websocket.Conn
	seen *seenURLs
	mu   sync.Mutex
}

func (o *wsOut) WritePacket(body []byte) error {
	o.seen.scanPacket(body)
	o.mu.Lock()
	defer o.mu.Unlock()
	_ = o.ws.SetWriteDeadline(time.Now().Add(wsWriteWait))
	return o.ws.WriteMessage(websocket.TextMessage, body)
}

func (o *wsOut) Close() error { return o.ws.Close() }

func (s *Server) serveWS(ctx context.Context, ws *websocket.Conn, remote, ip string) {
	ws.SetReadLimit(chatserver.MaxPacket)
	alive := func() { _ = ws.SetReadDeadline(time.Now().Add(s.pongWait)) }
	alive()
	ws.SetPongHandler(func(string) error { alive(); return nil })

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		t := time.NewTicker(s.pingEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				_ = ws.Close()
				return
			case <-t.C:
				// WriteControl is safe beside the packet writer.
				if err := ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(wsWriteWait)); err != nil {
					_ = ws.Close()
					return
				}
			}
		}
	}()

	out := &wsOut{ws: ws, seen: s.seen}
	pol := &policy{srv: s, ip: ip}
	// next returns the packets of the browser's next frame that pass the
	// policy, newline-joined; the policy's own replies go straight back.
	next := func() ([]byte, error) {
		for {
			mt, data, err := ws.ReadMessage()
			if err != nil {
				return nil, err
			}
			alive()
			if mt != websocket.TextMessage && mt != websocket.BinaryMessage {
				continue
			}
			var fwd [][]byte
			for _, line := range bytes.Split(data, []byte{'\n'}) {
				line = bytes.TrimSpace(line)
				if len(line) == 0 {
					continue
				}
				pass, reply := pol.filter(line)
				if reply != nil {
					if err := out.WritePacket(reply); err != nil {
						return nil, err
					}
				}
				if pass != nil {
					fwd = append(fwd, pass)
				}
			}
			if len(fwd) > 0 {
				return bytes.Join(fwd, []byte{'\n'}), nil
			}
		}
	}

	if s.cfg.Upstream != "" {
		s.relay(ctx, out, remote, next)
		return
	}
	s.Chat.ServeConn(ctx, out, remote, next)
}

// originAllowed: requests without an Origin (not a browser) and pages
// from this same host are let in; other pages only when Config.Origins
// lists them, as a full origin or a bare host name.
func (s *Server) originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	o := strings.ToLower(strings.TrimRight(origin, "/"))
	u, err := url.Parse(o)
	if err != nil || u.Host == "" {
		return false
	}
	if strings.EqualFold(u.Host, r.Host) {
		return true
	}
	for _, e := range s.cfg.Origins {
		e = strings.ToLower(strings.TrimRight(strings.TrimSpace(e), "/"))
		switch {
		case e == "":
		case strings.Contains(e, "://"):
			if e == o {
				return true
			}
		case e == u.Host || e == u.Hostname():
			return true
		}
	}
	return false
}

// errorPacket is the chat protocol's ERROR packet.
func errorPacket(desc string) []byte {
	body, _ := json.Marshal(map[string]string{"description": desc})
	b, _ := json.Marshal(map[string]json.RawMessage{"func": json.RawMessage(`"ERROR"`), "data": body})
	return b
}
