package chatserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// WebSocket transport. Browsers can't open raw TCP, so with WSAddr set the
// server also accepts WebSocket upgrades at / and /chat. Each text frame
// carries one JSON packet, exactly what a TCP line carries (a trailing
// newline is allowed, a frame holding several newline-separated packets
// is read as several, and binary frames are read the same way); every
// outgoing packet is one text frame with no line ending.
//
// The deny list is checked against the socket's own remote address.
// X-Forwarded-For is not trusted: behind a reverse proxy every client
// looks like the proxy, so filter at the proxy instead.

const (
	wsPingEvery = 30 * time.Second // keepalive ping interval
	wsPongWait  = 75 * time.Second // dropped if nothing (pong or packet) arrives in this long
	wsWriteWait = 10 * time.Second
)

// wsTimers are the keepalive intervals, shortened by tests.
func (s *Server) wsTimers() (ping, wait time.Duration) {
	if s.wsPing > 0 {
		return s.wsPing, s.wsWait
	}
	return wsPingEvery, wsPongWait
}

type wsWire struct{ ws *websocket.Conn }

func (w *wsWire) writePacket(body []byte) error {
	_ = w.ws.SetWriteDeadline(time.Now().Add(wsWriteWait))
	return w.ws.WriteMessage(websocket.TextMessage, body)
}

func (w *wsWire) Close() error { return w.ws.Close() }

// ListenAndServeWS blocks serving WebSocket clients on s.WSAddr until ctx
// is canceled or the listener errors.
func (s *Server) ListenAndServeWS(ctx context.Context) error {
	lc := net.ListenConfig{}
	l, err := lc.Listen(ctx, "tcp", s.WSAddr)
	if err != nil {
		return fmt.Errorf("chatserver: listen ws %s: %v", s.WSAddr, err)
	}
	s.Logger.Printf("chatserver: websocket on %s", l.Addr())
	hs := &http.Server{Handler: s.WSHandler(ctx), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		_ = hs.Close()
	}()
	if err := hs.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("chatserver: ws: %v", err)
	}
	return nil
}

// WSHandler upgrades requests for / and /chat; connections end with ctx.
func (s *Server) WSHandler(ctx context.Context) http.Handler {
	up := websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		CheckOrigin:     s.originAllowed,
		// websocketservice.js only completes a handshake that offers
		// "binary" or "plain" (or none); offering one here too lets the
		// same browser code use either server. Frames out stay text.
		Subprotocols: []string{"binary", "plain"},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/chat" {
			http.NotFound(w, r)
			return
		}
		if s.Deny.Denied(r.RemoteAddr) {
			s.Logger.Printf("chatserver: refused ws %s (deny list)", r.RemoteAddr)
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		ws, err := up.Upgrade(w, r, nil) // writes its own error reply
		if err != nil {
			s.Logger.Printf("chatserver: ws upgrade from %s: %v", r.RemoteAddr, err)
			return
		}
		s.serveWS(ctx, ws, r.RemoteAddr)
	})
}

func (s *Server) serveWS(ctx context.Context, ws *websocket.Conn, remote string) {
	ws.SetReadLimit(MaxPacket)
	pingEvery, pongWait := s.wsTimers()
	alive := func() { _ = ws.SetReadDeadline(time.Now().Add(pongWait)) }
	alive()
	ws.SetPongHandler(func(string) error { alive(); return nil })

	done := make(chan struct{})
	defer close(done)
	go func() {
		t := time.NewTicker(pingEvery)
		defer t.Stop()
		for {
			select {
			case <-done:
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

	c := &conn{
		server: s,
		wire:   &wsWire{ws: ws},
		id:     remote,
		subs:   map[string]bool{},
	}
	s.serve(ctx, c, func() ([]byte, error) {
		for {
			mt, data, err := ws.ReadMessage()
			if err != nil {
				return nil, err
			}
			alive()
			if mt == websocket.TextMessage || mt == websocket.BinaryMessage {
				return data, nil
			}
		}
	})
}

// originAllowed is the upgrade's origin check: anything when WSOrigins is
// empty or the request has no Origin (not a browser); otherwise the
// origin must match an entry, as a full origin or by host name.
func (s *Server) originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if len(s.WSOrigins) == 0 || origin == "" {
		return true
	}
	o := strings.ToLower(strings.TrimRight(origin, "/"))
	u, err := url.Parse(o)
	if err != nil {
		return false
	}
	for _, e := range s.WSOrigins {
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
