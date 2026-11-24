package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Lunar-Park/lp-ferry/internal/config"
	"github.com/Lunar-Park/lp-ferry/internal/ipc"
	"github.com/Lunar-Park/lp-ferry/internal/model"
)

// resolve loads config and paths with the global flag overrides.
func (g *globalFlags) resolve() (config.Config, config.Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return config.Config{}, config.Paths{}, err
	}
	def := config.DefaultPaths(home, runtime.GOOS)
	path, explicit := def.Config, false
	if g.config != "" {
		path, explicit = g.config, true
	}
	cfg, err := config.Load(path, explicit)
	if err != nil {
		return config.Config{}, config.Paths{}, err
	}
	paths := config.ResolvePaths(def, cfg, os.Getenv)
	paths.Config = path
	if g.socket != "" {
		paths.Socket = g.socket
	}
	return cfg, paths, nil
}

// dial connects to the running daemon.
func (g *globalFlags) dial(ctx context.Context) (*ipc.Client, error) {
	_, paths, err := g.resolve()
	if err != nil {
		return nil, err
	}
	c, err := ipc.Dial(ctx, paths.Socket)
	if err != nil {
		return nil, fmt.Errorf("the ferry daemon is not reachable at %s (start it with `ferry daemon`): %w", paths.Socket, err)
	}
	return c, nil
}

// call runs one RPC and prints the result as JSON (with --json) or via
// human when given.
func (g *globalFlags) call(cmd *cobra.Command, method string, params any, human func(io.Writer, json.RawMessage) error) error {
	c, err := g.dial(cmd.Context())
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	var raw json.RawMessage
	if err := c.Call(cmd.Context(), method, params, &raw); err != nil {
		return err
	}
	return g.print(cmd.OutOrStdout(), raw, human)
}

func (g *globalFlags) print(w io.Writer, raw json.RawMessage, human func(io.Writer, json.RawMessage) error) error {
	if g.json || human == nil {
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return err
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	return human(w, raw)
}

// parseSource reads "path", or "peer_id:/path" for a folder on a peer.
func parseSource(s, label string) model.SourceSpec {
	peer, path := splitPeer(s)
	return model.SourceSpec{Location: model.Location{PeerID: peer, Path: path}, Label: label}
}

// parseDest reads "path", "peer_id:/path", or "@destination_id".
func parseDest(s string) model.DestinationSpec {
	if id, ok := strings.CutPrefix(s, "@"); ok {
		return model.DestinationSpec{Location: model.Location{DestinationID: id}}
	}
	peer, path := splitPeer(s)
	return model.DestinationSpec{Location: model.Location{PeerID: peer, Path: path}}
}

func splitPeer(s string) (string, string) {
	if strings.HasPrefix(s, "fry_") {
		if peer, path, ok := strings.Cut(s, ":"); ok {
			return peer, path
		}
	}
	return model.LocalPeer, absPath(s)
}

func absPath(p string) string {
	if strings.HasPrefix(p, "/") {
		return p
	}
	wd, err := os.Getwd()
	if err != nil {
		return p
	}
	return wd + "/" + p
}
