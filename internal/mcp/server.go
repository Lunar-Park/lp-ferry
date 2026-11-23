// Package mcp exposes Ferry to agents as a Model Context Protocol server
// over stdio. Every tool goes through the daemon socket, so an agent's
// transfer appears live in the app with its name attached, and the same
// policy, approvals and safety rules apply. There is deliberately no tool
// that deletes, moves or overwrites anything.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/Lunar-Park/lp-ferry/internal/ipc"
	"github.com/Lunar-Park/lp-ferry/internal/model"
	"github.com/Lunar-Park/lp-ferry/internal/planner"
)

// Caller is the daemon client (ipc.Client satisfies it).
type Caller interface {
	Call(ctx context.Context, method string, params, out any) error
}

// Server is one MCP stdio session.
type Server struct {
	Daemon Caller
	// Agent overrides the actor; by default it is "agent:<clientInfo.name>".
	Agent   string
	Version string

	mu    sync.Mutex
	actor string
}

// Supported protocol versions, newest first.
var protocolVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

type rpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *ipc.Error      `json:"error,omitempty"`
}

// Serve runs the session until in ends.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	enc := json.NewEncoder(out)
	var wmu sync.Mutex
	var wg sync.WaitGroup
	defer wg.Wait()
	write := func(v any) {
		wmu.Lock()
		defer wmu.Unlock()
		_ = enc.Encode(v)
	}
	for sc.Scan() {
		var req rpcReq
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			write(rpcResp{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &ipc.Error{Code: ipc.CodeParse, Message: err.Error()}})
			continue
		}
		if len(req.ID) == 0 {
			continue // notifications (initialized, cancelled) need no reply
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := s.dispatch(ctx, req)
			resp := rpcResp{JSONRPC: "2.0", ID: req.ID, Result: res}
			if err != nil {
				var ie *ipc.Error
				if !errors.As(err, &ie) {
					ie = &ipc.Error{Code: ipc.CodeInternal, Message: err.Error()}
				}
				resp.Result, resp.Error = nil, ie
			}
			write(resp)
		}()
	}
	return sc.Err()
}

func (s *Server) actorName() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Agent != "" {
		return s.Agent
	}
	if s.actor != "" {
		return s.actor
	}
	return "agent:mcp"
}

// sanitizeAgent turns an MCP client name into an actor-safe token.
func sanitizeAgent(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('-')
		}
		if b.Len() >= 48 {
			break
		}
	}
	if b.Len() == 0 {
		return "agent:mcp"
	}
	return "agent:" + b.String()
}

func (s *Server) dispatch(ctx context.Context, req rpcReq) (any, error) {
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
			ClientInfo      struct {
				Name string `json:"name"`
			} `json:"clientInfo"`
		}
		_ = json.Unmarshal(req.Params, &p)
		s.mu.Lock()
		s.actor = sanitizeAgent(p.ClientInfo.Name)
		s.mu.Unlock()
		version := protocolVersions[0]
		for _, v := range protocolVersions {
			if v == p.ProtocolVersion {
				version = v
			}
		}
		return map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "ferry", "version": s.Version},
			"instructions": "Ferry copies and verifies files; it never deletes, moves or overwrites. Plan with ferry_plan, " +
				"inspect with ferry_plan_tree, then start with ferry_transfer_start using the plan's exact fingerprint. " +
				"Large or unsaved-destination transfers wait for a human (status awaiting_approval).",
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": Tools()}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &ipc.Error{Code: ipc.CodeInvalidParams, Message: err.Error()}
		}
		return s.call(ctx, p.Name, p.Arguments), nil
	}
	return nil, &ipc.Error{Code: ipc.CodeMethodNotFound, Message: "unknown method " + req.Method}
}

// Tool is one MCP tool definition.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations map[string]any `json:"annotations,omitempty"`
}

func obj(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

var str = map[string]any{"type": "string"}

func readOnly() map[string]any { return map[string]any{"readOnlyHint": true, "destructiveHint": false} }

// Tools is the complete catalog. Nothing here deletes, moves or
// overwrites; ferry_transfer_start only ever copies and never replaces.
func Tools() []Tool {
	location := obj(map[string]any{"path": str, "peer_id": str, "label": str})
	dest := obj(map[string]any{"path": str, "peer_id": str, "destination_id": str, "label": str})
	return []Tool{
		{"ferry_status", "Daemon health, active transfers with live progress, and plans awaiting human approval.", obj(nil), readOnly()},
		{"ferry_drives", "Mounted drives with volume identity, free space, matched saved destinations and camera-card detection.", obj(nil), readOnly()},
		{"ferry_peers", "Paired machines and the best path (transport badge) currently in use to each.", obj(nil), readOnly()},
		{"ferry_destinations", "Saved destinations with live availability and pinned presets.", obj(nil), readOnly()},
		{"ferry_presets", "Organization and renaming presets.", obj(nil), readOnly()},
		{"ferry_plan", "Build a concrete copy plan from sources to one or more destinations (local folders, saved destinations or folders on paired peers). Returns the plan id, fingerprint, findings and counts. Nothing is copied.",
			obj(map[string]any{
				"sources":      map[string]any{"type": "array", "items": location, "minItems": 1},
				"destinations": map[string]any{"type": "array", "items": dest, "minItems": 1},
				"preset_id":    str,
				"camera_roles": map[string]any{"type": "object", "additionalProperties": str, "description": "source label -> camera role (e.g. ACAM)"},
			}, "sources", "destinations"), map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": false}},
		{"ferry_plan_tree", "Inspect the Before -> After destination folder tree of a plan, paged.",
			obj(map[string]any{"plan_id": str, "destination_id": str, "parent_path": str, "depth": map[string]any{"type": "integer"},
				"limit": map[string]any{"type": "integer"}, "cursor": str}, "plan_id", "destination_id"), readOnly()},
		{"ferry_transfer_start", "Start a reviewed plan by its exact fingerprint. Copies and verifies only; never deletes or overwrites. Policy may hold it as awaiting_approval for a human.",
			obj(map[string]any{"plan_id": str, "fingerprint": str}, "plan_id", "fingerprint"),
			map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": false}},
		{"ferry_transfer_status", "Live progress, throughput, transport path, honest ETA and per-target status of a transfer.",
			obj(map[string]any{"transfer_id": str}, "transfer_id"), readOnly()},
		{"ferry_transfer_cancel", "Cancel a running transfer. Verified files and the partial receipt are kept; it can be resumed.",
			obj(map[string]any{"transfer_id": str}, "transfer_id"), map[string]any{"readOnlyHint": false, "destructiveHint": false}},
		{"ferry_receipt", "The cryptographic receipt of a transfer: per-file hashes, targets, exclusions and digest.",
			obj(map[string]any{"transfer_id": str, "receipt_id": str}), readOnly()},
		{"ferry_verify", "Re-hash landed files with physical disk readback against a receipt, locally or on the peer that holds them.",
			obj(map[string]any{"transfer_id": str, "receipt_id": str, "target_id": str}), readOnly()},
	}
}

type toolResult struct {
	Content           []map[string]string `json:"content"`
	StructuredContent any                 `json:"structuredContent,omitempty"`
	IsError           bool                `json:"isError"`
}

func ok(v any) toolResult {
	b, _ := json.MarshalIndent(v, "", "  ")
	sc := v
	if _, isObj := v.(map[string]any); !isObj {
		sc = map[string]any{"result": v}
	}
	return toolResult{Content: []map[string]string{{"type": "text", "text": string(b)}}, StructuredContent: sc}
}

func fail(err error) toolResult {
	return toolResult{Content: []map[string]string{{"type": "text", "text": err.Error()}}, IsError: true}
}

func strict(raw json.RawMessage, v any) error {
	if len(raw) == 0 || string(raw) == "null" {
		raw = []byte("{}")
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func (s *Server) passthrough(ctx context.Context, method string, params any) toolResult {
	var out any
	if err := s.Daemon.Call(ctx, method, params, &out); err != nil {
		return fail(err)
	}
	if m, isMap := out.(map[string]any); isMap {
		return ok(m)
	}
	return ok(out)
}

func (s *Server) call(ctx context.Context, name string, args json.RawMessage) toolResult {
	actor := s.actorName()
	switch name {
	case "ferry_status":
		return s.passthrough(ctx, "daemon.status", nil)
	case "ferry_drives":
		return s.passthrough(ctx, "drive.list", nil)
	case "ferry_peers":
		return s.passthrough(ctx, "peer.list", nil)
	case "ferry_destinations":
		return s.passthrough(ctx, "dest.list", nil)
	case "ferry_presets":
		return s.passthrough(ctx, "preset.list", nil)
	case "ferry_plan":
		var a struct {
			Sources []struct {
				Path   string `json:"path"`
				PeerID string `json:"peer_id"`
				Label  string `json:"label"`
			} `json:"sources"`
			Destinations []struct {
				Path          string `json:"path"`
				PeerID        string `json:"peer_id"`
				DestinationID string `json:"destination_id"`
				Label         string `json:"label"`
			} `json:"destinations"`
			PresetID    string            `json:"preset_id"`
			CameraRoles map[string]string `json:"camera_roles"`
		}
		if err := strict(args, &a); err != nil {
			return fail(err)
		}
		req := model.PlanRequest{PresetID: a.PresetID, CameraRoles: a.CameraRoles, Actor: actor}
		for _, s := range a.Sources {
			req.Sources = append(req.Sources, model.SourceSpec{Location: model.Location{PeerID: s.PeerID, Path: s.Path}, Label: s.Label})
		}
		for _, d := range a.Destinations {
			req.Destinations = append(req.Destinations, model.DestinationSpec{Location: model.Location{PeerID: d.PeerID, Path: d.Path,
				DestinationID: d.DestinationID}, Label: d.Label})
		}
		return s.passthrough(ctx, "plan.create", req)
	case "ferry_plan_tree":
		var q planner.TreeQuery
		if err := strict(args, &q); err != nil {
			return fail(err)
		}
		return s.passthrough(ctx, "plan.tree", q)
	case "ferry_transfer_start":
		var a struct {
			PlanID      string `json:"plan_id"`
			Fingerprint string `json:"fingerprint"`
		}
		if err := strict(args, &a); err != nil {
			return fail(err)
		}
		return s.passthrough(ctx, "transfer.start", map[string]string{"plan_id": a.PlanID, "fingerprint": a.Fingerprint, "actor": actor})
	case "ferry_transfer_status", "ferry_transfer_cancel":
		var a struct {
			TransferID string `json:"transfer_id"`
		}
		if err := strict(args, &a); err != nil {
			return fail(err)
		}
		method := "transfer.get"
		if name == "ferry_transfer_cancel" {
			method = "transfer.cancel"
		}
		return s.passthrough(ctx, method, a)
	case "ferry_receipt":
		var a struct {
			TransferID string `json:"transfer_id,omitempty"`
			ReceiptID  string `json:"receipt_id,omitempty"`
		}
		if err := strict(args, &a); err != nil {
			return fail(err)
		}
		return s.passthrough(ctx, "receipt.get", a)
	case "ferry_verify":
		var a struct {
			TransferID string `json:"transfer_id,omitempty"`
			ReceiptID  string `json:"receipt_id,omitempty"`
			TargetID   string `json:"target_id,omitempty"`
		}
		if err := strict(args, &a); err != nil {
			return fail(err)
		}
		return s.passthrough(ctx, "verify.run", a)
	}
	return fail(fmt.Errorf("unknown tool %q", name))
}
