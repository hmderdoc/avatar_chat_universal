// Package chatserver implements a JSON-RPC chat server compatible with the
// universal door's chat client and the Synchronet JS door at
// /sbbs/exec/load/json-{sock,client,chat}.js.
//
// Wire protocol matches json-sock.js / json-client.js exactly:
//
//   - line-delimited JSON, "\r\n" terminator, max 131072 bytes per packet
//   - inbound query: {"scope":"chat","func":"QUERY","oper":"WRITE",
//                     "location":"channels.main.messages","data":...,
//                     "lock":2,"timeout":-1,"nick":...,"system":...}
//   - inbound ping:  {"scope":"SOCKET","func":"PING","data":<ts>}
//   - outbound response: {"scope":"chat","func":"RESPONSE","oper":"WRITE",
//                         "location":...,"data":...}
//   - outbound update:   {"scope":"chat","func":"UPDATE","oper":"WRITE",
//                         "location":...,"data":...}
//   - outbound error:    {"func":"ERROR","data":{"description":"..."}}
//
// Designed to be a drop-in alternative to futureland.today:10088 for sysops
// who want to run their own chat without an internet uplink.
package chatserver

import (
	"encoding/json"
	"strings"
	"sync"
	"time"
)

// store is a dot-notated key/value blob with first-class array support for
// the PUSH / SLICE / WRITE-list operations used by chat history.
//
// All values are stored as json.RawMessage so we don't need to know the
// chat-specific schema. Arrays are stored as []json.RawMessage.
type store struct {
	mu      sync.RWMutex
	scalars map[string]json.RawMessage
	arrays  map[string][]json.RawMessage
	// names: lowercased nicks doors and bridges (TCP connections) have
	// used, with when each was last seen (unix seconds). Web name claims
	// may not take them (Server.BBSName).
	names map[string]int64

	gen     uint64        // bumped by every change; persist.go saves when it moved
	changed chan struct{} // signaled (never blocking) on every change
}

func newStore() *store {
	return &store{
		scalars: map[string]json.RawMessage{},
		arrays:  map[string][]json.RawMessage{},
		names:   map[string]int64{},
		changed: make(chan struct{}, 1),
	}
}

// touch records a change. Callers hold mu for writing.
func (s *store) touch() {
	s.gen++
	select {
	case s.changed <- struct{}{}:
	default:
	}
}

// key combines scope + dot-path into a single map key. Both args are
// case-sensitive to match how the JS chat builds locations.
func storeKey(scope, location string) string {
	return scope + "." + location
}

// Write replaces the value at scope.location with raw.
func (s *store) Write(scope, location string, raw json.RawMessage) {
	k := storeKey(scope, location)
	s.mu.Lock()
	delete(s.arrays, k)
	s.scalars[k] = append(s.scalars[k][:0], raw...)
	s.touch()
	s.mu.Unlock()
}

// Read returns the value at scope.location, a PUSH-built array as a JSON
// array, or nil if absent. (The JS door READs a history to check it is an
// array before PUSHing, and WRITEs [] over it when it isn't.)
func (s *store) Read(scope, location string) json.RawMessage {
	k := storeKey(scope, location)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if v, ok := s.scalars[k]; ok {
		return append(json.RawMessage(nil), v...)
	}
	if a, ok := s.arrays[k]; ok {
		b, err := json.Marshal(a)
		if err == nil {
			return b
		}
	}
	return nil
}

// Push appends raw to the array at scope.location, creating it if needed.
// With max > 0 the array keeps only its newest max entries. Returns the
// new length of the array.
func (s *store) Push(scope, location string, raw json.RawMessage, max int) int {
	k := storeKey(scope, location)
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.scalars, k)
	a := s.arrays[k]
	a = append(a, append(json.RawMessage(nil), raw...))
	if max > 0 && len(a) > max {
		a = append(a[:0], a[len(a)-max:]...)
	}
	s.arrays[k] = a
	s.touch()
	return len(a)
}

// seeName records that a door or bridge used nick (lowercased) at t.
func (s *store) seeName(nick string, t time.Time) {
	lower := strings.ToLower(strings.TrimSpace(nick))
	if lower == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.names[lower] == t.Unix() {
		return
	}
	s.names[lower] = t.Unix()
	s.touch()
}

// nameSeenSince reports whether a door or bridge used nick at or after t.
func (s *store) nameSeenSince(nick string, t time.Time) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	last, ok := s.names[strings.ToLower(strings.TrimSpace(nick))]
	return ok && last >= t.Unix()
}

// Slice returns array[start:end]. JavaScript-style negative indices are
// supported: start = -200 means "200 from the end". end == nil means
// "to end of array".
func (s *store) Slice(scope, location string, start int, end *int) []json.RawMessage {
	k := storeKey(scope, location)
	s.mu.RLock()
	defer s.mu.RUnlock()
	a := s.arrays[k]
	n := len(a)
	if n == 0 {
		return nil
	}
	resolve := func(i int) int {
		if i < 0 {
			i = n + i
		}
		if i < 0 {
			i = 0
		}
		if i > n {
			i = n
		}
		return i
	}
	lo := resolve(start)
	hi := n
	if end != nil {
		hi = resolve(*end)
	}
	if hi < lo {
		hi = lo
	}
	out := make([]json.RawMessage, hi-lo)
	for i := lo; i < hi; i++ {
		out[i-lo] = append(json.RawMessage(nil), a[i]...)
	}
	return out
}

// Keys lists immediate children of scope.location, treating the path as a
// container of dot-separated subkeys. (Used rarely; included for
// json-client.js's KEYS verb compatibility.)
func (s *store) Keys(scope, location string) []string {
	prefix := storeKey(scope, location) + "."
	seen := map[string]bool{}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for k := range s.scalars {
		if strings.HasPrefix(k, prefix) {
			tail := k[len(prefix):]
			if i := strings.IndexByte(tail, '.'); i >= 0 {
				tail = tail[:i]
			}
			seen[tail] = true
		}
	}
	for k := range s.arrays {
		if strings.HasPrefix(k, prefix) {
			tail := k[len(prefix):]
			if i := strings.IndexByte(tail, '.'); i >= 0 {
				tail = tail[:i]
			}
			seen[tail] = true
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	return out
}
