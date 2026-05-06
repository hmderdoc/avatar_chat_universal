// Package chat implements a Go client for Synchronet's JSON-over-TCP chat
// protocol, compatible with the JS door at /sbbs/exec/load/json-{sock,client,chat}.js
// and the chat server it talks to (default futureland.today:10088).
//
// Wire format:
//   - TCP, plain text, line-delimited JSON, "\r\n" terminator.
//   - Max packet size: 131072 bytes (json-sock.js:9).
//   - Server PINGs every 60s; we respond with PONG (json-sock.js:55-72).
//
// Outbound query packet shape (json-client.js):
//   {"scope":"chat","func":"QUERY","oper":"WRITE","location":"channels.main.messages",
//    "data":<msg>,"lock":2,"timeout":-1}
//
// Inbound packet shape:
//   {"scope":...,"func":"RESPONSE"|"UPDATE"|"PING"|"PONG"|"ERROR",
//    "location":...,"oper":...,"data":...}
package chat

import "encoding/json"

// Lock values (json-client.js:36-38).
const (
	LockRead   = 1
	LockWrite  = 2
	LockUnlock = -1
)

// Packet is the union of outbound and inbound packets. Fields are optional;
// json:",omitempty" keeps unused fields out of the wire. Data is RawMessage
// so we can lazily decode it based on the packet's oper.
type Packet struct {
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

// Nick identifies a chat sender. The Avatar field is the base64-encoded
// 120-byte avatar payload — the universal door always populates this so the
// JS door's avatar_chat renders our messages with a portrait.
type Nick struct {
	Name   string `json:"name"`
	Host   string `json:"host,omitempty"`
	IP     string `json:"ip,omitempty"`
	Avatar string `json:"avatar,omitempty"`
}

// Message is the chat envelope. Time is unix milliseconds.
//
// Private is a client-side flag set when this message was received via or
// sent through a personal mailbox channel (channels.<username>.messages),
// not the main public channel. It's not serialized to the wire — the JS
// door's PRIV_COLOR distinction is purely a renderer hint.
type Message struct {
	Nick *Nick  `json:"nick"`
	Str  string `json:"str"`
	Time int64  `json:"time"`

	Private bool `json:"-"`
}

// ErrorPayload is the shape of `data` when func == ERROR.
type ErrorPayload struct {
	Description string `json:"description"`
}

// WhoEntry is one item in a WHO response. The server returns
// {id, nick, system} per subscriber (json-db.js:1075-1081). The `id` field
// can be a number or string depending on which server you're talking to,
// so we don't bother decoding it -- we only care about the human-friendly
// nick and the BBS system name.
type WhoEntry struct {
	Nick   string `json:"nick"`
	System string `json:"system"`
}
