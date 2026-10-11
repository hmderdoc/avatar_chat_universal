package webchat

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
)

// roomList answers whether a room is public: the default room, or a name
// in the chat's public_channels registry (the one the web chat and the
// door read for their room lists). Locally that's the store; upstream it
// is read over a short-lived TCP connection and kept for ttl. Rooms a web
// user just created count at once.
type roomList struct {
	srv *Server
	ttl time.Duration

	refreshMu sync.Mutex // one upstream read at a time

	mu      sync.Mutex
	names   map[string]bool
	fetched time.Time
	added   map[string]bool
}

func (l *roomList) add(name string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.added == nil {
		l.added = map[string]bool{}
	}
	l.added[strings.ToLower(name)] = true
}

func (l *roomList) allowed(name string) bool {
	key := strings.ToLower(name)
	if strings.EqualFold(name, l.srv.cfg.DefaultChannel) {
		return true
	}
	if l.srv.cfg.Upstream == "" && l.srv.Chat != nil {
		for _, k := range l.srv.Chat.Keys("chat", "public_channels") {
			if strings.ToLower(k) == key {
				return true
			}
		}
		l.mu.Lock()
		defer l.mu.Unlock()
		return l.added[key]
	}
	l.mu.Lock()
	stale := l.srv.now().Sub(l.fetched) > l.ttl
	l.mu.Unlock()
	if stale {
		l.refresh()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.names[key] || l.added[key]
}

// refresh reads the upstream registry. On failure the last good list
// stays (and is retried after ttl).
func (l *roomList) refresh() {
	l.refreshMu.Lock()
	defer l.refreshMu.Unlock()
	l.mu.Lock()
	fresh := l.srv.now().Sub(l.fetched) <= l.ttl
	l.mu.Unlock()
	if fresh {
		return // another browser refreshed it while this one waited
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	keys, err := l.fetchUpstream(ctx)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fetched = l.srv.now()
	if err != nil {
		l.srv.Logger.Printf("web: reading the room list from %s: %v", l.srv.cfg.Upstream, err)
		return
	}
	l.names = map[string]bool{}
	for _, k := range keys {
		l.names[strings.ToLower(k)] = true
	}
}

func (l *roomList) fetchUpstream(ctx context.Context) ([]string, error) {
	c := chat.NewClient(l.srv.cfg.Upstream)
	c.RequestTimeout = 6 * time.Second
	if err := c.Connect(ctx); err != nil {
		return nil, err
	}
	defer c.Close()
	return c.Keys("chat", "public_channels")
}
