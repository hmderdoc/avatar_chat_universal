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
}

func newStore() *store {
	return &store{
		scalars: map[string]json.RawMessage{},
		arrays:  map[string][]json.RawMessage{},
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
	s.mu.Unlock()
}

// Read returns the scalar at scope.location, or nil if absent or if the path
// holds an array.
func (s *store) Read(scope, location string) json.RawMessage {
	k := storeKey(scope, location)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if v, ok := s.scalars[k]; ok {
		return append(json.RawMessage(nil), v...)
	}
	return nil
}

// Push appends raw to the array at scope.location, creating it if needed.
// Returns the new length of the array.
func (s *store) Push(scope, location string, raw json.RawMessage) int {
	k := storeKey(scope, location)
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.scalars, k)
	a := s.arrays[k]
	a = append(a, append(json.RawMessage(nil), raw...))
	s.arrays[k] = a
	return len(a)
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
