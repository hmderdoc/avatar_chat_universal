package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// Client wraps Conn with the high-level QUERY verbs from json-client.js.
//
// Semantics match json-client.js (default TIMEOUT=-1):
//
//   - Fire-and-forget ops (Write, Push, Subscribe, Unsubscribe): send and
//     return immediately. The futureland.today server doesn't reliably ack
//     these, so awaiting would just time out.
//
//   - Query ops (Slice, Read, Who, Status, Keys): drain any stale responses
//     left over from prior fire-and-forget ops, send, then block until our
//     RESPONSE arrives. Serialized via queryMu so two queries don't race.
//
// Updates (subscribed-to events) are surfaced separately via Conn.Updates().
type Client struct {
	conn *Conn

	Nick   string
	System string

	RequestTimeout time.Duration

	queryMu sync.Mutex
}

func NewClient(addr string) *Client {
	return &Client{
		conn:           NewConn(addr),
		RequestTimeout: 10 * time.Second,
	}
}

func (c *Client) Connect(ctx context.Context) error { return c.conn.Open(ctx) }
func (c *Client) Close() error                      { return c.conn.Close() }
func (c *Client) Updates() <-chan *Packet           { return c.conn.Updates() }
func (c *Client) IsAlive() bool                     { return c.conn.IsAlive() }

// Reconnect drops the current TCP socket and dials again. Callers should
// re-issue any subscriptions they need after this returns.
func (c *Client) Reconnect(ctx context.Context) error {
	c.queryMu.Lock()
	defer c.queryMu.Unlock()
	_ = c.conn.Close()
	return c.conn.Open(ctx)
}

// Subscribe registers interest in updates at scope.location. Fire-and-forget.
func (c *Client) Subscribe(scope, location string) error {
	return c.conn.Send(&Packet{
		Scope:    scope,
		Func:     "QUERY",
		Oper:     "SUBSCRIBE",
		Location: location,
		Nick:     c.Nick,
		System:   c.System,
		Timeout:  -1,
	})
}

// Unsubscribe is the inverse of Subscribe. Fire-and-forget.
func (c *Client) Unsubscribe(scope, location string) error {
	return c.conn.Send(&Packet{
		Scope:    scope,
		Func:     "QUERY",
		Oper:     "UNSUBSCRIBE",
		Location: location,
		Timeout:  -1,
	})
}

// Write stores `value` at scope.location. Subscribers receive an UPDATE
// packet with this value. Fire-and-forget — matches the JS client.
func (c *Client) Write(scope, location string, value interface{}, lock int) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("chat: marshal write data: %v", err)
	}
	return c.conn.Send(&Packet{
		Scope:    scope,
		Func:     "QUERY",
		Oper:     "WRITE",
		Location: location,
		Data:     data,
		Lock:     lock,
		Timeout:  -1,
	})
}

// Push appends `value` to the array at scope.location. Fire-and-forget.
func (c *Client) Push(scope, location string, value interface{}, lock int) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("chat: marshal push data: %v", err)
	}
	return c.conn.Send(&Packet{
		Scope:    scope,
		Func:     "QUERY",
		Oper:     "PUSH",
		Location: location,
		Data:     data,
		Lock:     lock,
		Timeout:  -1,
	})
}

// Slice returns array[start:end] at scope.location. Negative indices count
// from the end (JS Array.slice semantics). end == nil means "to end".
func (c *Client) Slice(scope, location string, start int, end *int, lock int, target interface{}) error {
	type sliceArgs struct {
		Start int  `json:"start"`
		End   *int `json:"end,omitempty"`
	}
	data, _ := json.Marshal(sliceArgs{Start: start, End: end})
	pkt, err := c.query(&Packet{
		Scope:    scope,
		Func:     "QUERY",
		Oper:     "SLICE",
		Location: location,
		Data:     data,
		Lock:     lock,
		Timeout:  -1,
	})
	if err != nil {
		return err
	}
	if pkt.Data == nil {
		return nil
	}
	return json.Unmarshal(pkt.Data, target)
}

// Who returns the full subscriber list at scope.location, one WhoEntry per
// active subscriber, with id/nick/system fields populated as the server
// provides. We accept either the canonical Synchronet shape ([]WhoEntry) or
// a bare []string for compat with simpler servers.
func (c *Client) Who(scope, location string) ([]WhoEntry, error) {
	pkt, err := c.query(&Packet{
		Scope:    scope,
		Func:     "QUERY",
		Oper:     "WHO",
		Location: location,
		Timeout:  -1,
	})
	if err != nil {
		return nil, err
	}
	if pkt.Data == nil {
		return nil, nil
	}

	var asEntries []WhoEntry
	if err := json.Unmarshal(pkt.Data, &asEntries); err == nil {
		return asEntries, nil
	}

	var asStrings []string
	if err := json.Unmarshal(pkt.Data, &asStrings); err == nil {
		out := make([]WhoEntry, 0, len(asStrings))
		for _, s := range asStrings {
			out = append(out, WhoEntry{Nick: s})
		}
		return out, nil
	}

	return nil, fmt.Errorf("chat: unrecognized WHO response shape: %s", pkt.Data)
}

// query serializes the request, sends, and awaits a RESPONSE whose oper
// matches the request. Fire-and-forget acks for other operations don't
// satisfy AwaitResponse — they get filtered out.
func (c *Client) query(p *Packet) (*Packet, error) {
	c.queryMu.Lock()
	defer c.queryMu.Unlock()
	if err := c.conn.Send(p); err != nil {
		return nil, err
	}
	return c.conn.AwaitResponse(p.Oper, c.RequestTimeout)
}
