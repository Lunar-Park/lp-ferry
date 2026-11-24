// Package ipc is the local control channel between the daemon and its
// clients (CLI, MCP bridge, SwiftUI app): newline-delimited JSON-RPC 2.0
// over a user-only Unix socket, plus server-pushed event notifications.
// The protocol is documented in docs/IPC-PROTOCOL.md.
package ipc

import (
	"encoding/json"
	"fmt"
	"time"
)

// ProtocolVersion is reported by daemon.status; bumped on breaking change.
const ProtocolVersion = 1

// Request is a JSON-RPC 2.0 call.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Error is a JSON-RPC error object.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *Error) Error() string { return fmt.Sprintf("ipc error %d: %s", e.Code, e.Message) }

// Error codes. -32xxx are JSON-RPC's; Ferry's start at -32000.
const (
	CodeParse          = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternal       = -32603
	CodeFailed         = -32000 // the operation ran and failed
	CodeStale          = -32001 // stale or invalidated approval
	CodeBlocked        = -32002 // blocking findings or policy refusal
	CodeNotFound       = -32004
)

// Response is a JSON-RPC 2.0 reply.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// Event is a server-pushed notification payload.
type Event struct {
	Topic string    `json:"topic"`
	Time  time.Time `json:"time"`
	Data  any       `json:"data"`
}

// Notification wraps an Event as a JSON-RPC notification ("method":"event").
type Notification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  Event  `json:"params"`
}
