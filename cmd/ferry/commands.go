package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/Lunar-Park/lp-ferry/internal/daemon"
	"github.com/Lunar-Park/lp-ferry/internal/engine"
	"github.com/Lunar-Park/lp-ferry/internal/ipc"
	"github.com/Lunar-Park/lp-ferry/internal/mcp"
	"github.com/Lunar-Park/lp-ferry/internal/model"
	"github.com/Lunar-Park/lp-ferry/internal/planner"
)

func addCommands(root *cobra.Command, g *globalFlags) {
	root.AddCommand(daemonCmd(g), mcpCmd(g), planCmd(g), approveCmd(g), sendCmd(g), statusCmd(g),
		simpleCmd(g, "cancel TRANSFER", "Cancel a running transfer (verified files and the partial receipt are kept)", "transfer.cancel", "transfer_id"),
		simpleCmd(g, "resume TRANSFER", "Resume a stopped transfer: reconcile its crash windows and finish what is left", "transfer.resume", "transfer_id"),
		peerCmd(g), driveCmd(g), destCmd(g), presetCmd(g), cameraCmd(g), receiptCmd(g), doctorCmd(g), eventsCmd(g))
}

// simpleCmd maps "ferry X ARG" to one RPC with one string parameter.
func simpleCmd(g *globalFlags, use, short, method, param string) *cobra.Command {
	return &cobra.Command{Use: use, Short: short, Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return g.call(cmd, method, map[string]string{param: args[0]}, nil)
	}}
}

// sdNotify tells systemd (Type=notify) the daemon is ready.
func sdNotify(state string) {
	sock := os.Getenv("NOTIFY_SOCKET")
	if sock == "" {
		return
	}
	c, err := net.Dial("unixgram", sock) //nolint:gosec // NOTIFY_SOCKET is set by systemd for this process
	if err != nil {
		return
	}
	defer func() { _ = c.Close() }()
	_, _ = c.Write([]byte(state))
}

func daemonCmd(g *globalFlags) *cobra.Command {
	var listen string
	var noP2P, noMDNS bool
	cmd := &cobra.Command{Use: "daemon", Short: "Run the Ferry engine (socket IPC, P2P listener, transfers)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, paths, err := g.resolve()
			if err != nil {
				return err
			}
			opts := daemon.Options{Config: cfg, Paths: paths, Version: version, MDNS: !noMDNS}
			if !noP2P {
				if listen == "" {
					listen = ":" + strconv.Itoa(cfg.Daemon.ListenPort)
				}
				opts.P2PAddrs = []string{listen}
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			d, err := daemon.New(ctx, opts)
			if err != nil {
				return err
			}
			errc := make(chan error, 1)
			go func() { errc <- d.Serve(ctx) }()
			sdNotify("READY=1")
			fmt.Fprintf(cmd.ErrOrStderr(), "ferry daemon %s listening on %s\n", version, paths.Socket)
			select {
			case <-ctx.Done():
			case err = <-errc:
			}
			sdNotify("STOPPING=1")
			d.Close()
			return err
		}}
	cmd.Flags().StringVar(&listen, "listen", "", "P2P listen address (default :<daemon.listen_port>)")
	cmd.Flags().BoolVar(&noP2P, "no-p2p", false, "disable the P2P listener")
	cmd.Flags().BoolVar(&noMDNS, "no-mdns", false, "do not advertise over mDNS")
	return cmd
}

func mcpCmd(g *globalFlags) *cobra.Command {
	var agent string
	cmd := &cobra.Command{Use: "mcp", Short: "Serve the Model Context Protocol over stdio (talks to the daemon)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := g.dial(cmd.Context())
			if err != nil {
				return err
			}
			defer func() { _ = c.Close() }()
			s := &mcp.Server{Daemon: c, Version: version}
			if agent != "" {
				s.Agent = "agent:" + agent
			}
			return s.Serve(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout())
		}}
	cmd.Flags().StringVar(&agent, "agent", "", "agent name for attribution (default: the MCP client's name)")
	return cmd
}

type planFlags struct {
	to, roles      []string
	preset, policy string
	label          string
}

func (f *planFlags) bind(cmd *cobra.Command) {
	cmd.Flags().StringArrayVar(&f.to, "to", nil, "destination: PATH, peer_id:/path, or @saved_destination_id (repeatable)")
	cmd.Flags().StringVar(&f.preset, "preset", "", "organization preset id (default: the destination's pinned preset, else preserve-source-structure)")
	cmd.Flags().StringVar(&f.policy, "conflict", "", "conflict policy override: keep_both | skip_identical | needs_review")
	cmd.Flags().StringVar(&f.label, "label", "", "source label (single source; default: folder name)")
	cmd.Flags().StringArrayVar(&f.roles, "role", nil, "camera role for a source: LABEL=ROLE (repeatable)")
	_ = cmd.MarkFlagRequired("to")
}

func (f *planFlags) request(args []string) (model.PlanRequest, error) {
	req := model.PlanRequest{PresetID: f.preset, ConflictPolicy: f.policy, Actor: "human"}
	for _, a := range args {
		req.Sources = append(req.Sources, parseSource(a, ""))
	}
	if f.label != "" && len(req.Sources) == 1 {
		req.Sources[0].Label = f.label
	}
	for _, t := range f.to {
		req.Destinations = append(req.Destinations, parseDest(t))
	}
	for _, r := range f.roles {
		label, role, ok := strings.Cut(r, "=")
		if !ok {
			return req, fmt.Errorf("--role wants LABEL=ROLE, got %q", r)
		}
		if req.CameraRoles == nil {
			req.CameraRoles = map[string]string{}
		}
		req.CameraRoles[label] = role
	}
	return req, nil
}

func printPlan(w io.Writer, raw json.RawMessage) error {
	var p daemon.PlanSummary
	if err := json.Unmarshal(raw, &p); err != nil {
		return err
	}
	fmt.Fprintf(w, "plan %s (%s) by %s\nfingerprint %s\n", p.ID, p.Status, p.Actor, p.Fingerprint)
	for _, d := range p.Destinations {
		fmt.Fprintf(w, "  → %s  %s  preset %s r%d  %s  needs %d of %d bytes free\n", d.Label, d.RootPath, d.PresetID, d.PresetRev,
			d.PublishStrategy, d.RequiredBytes, d.FreeBytes)
	}
	fmt.Fprintf(w, "  entries: %v  (%d bytes to copy)\n", p.Counts, p.TotalBytes)
	for _, f := range p.Findings {
		fmt.Fprintf(w, "  [%s] %s %s %s\n", f.Severity, f.Kind, f.SourceRel, f.Message)
	}
	if p.Blocking > 0 {
		fmt.Fprintf(w, "  %d blocking item(s): resolve before approval\n", p.Blocking)
	}
	return nil
}

func planCmd(g *globalFlags) *cobra.Command {
	f := &planFlags{}
	cmd := &cobra.Command{Use: "plan SOURCE... --to DEST", Short: "Build a reviewable plan (nothing is copied)", Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			req, err := f.request(args)
			if err != nil {
				return err
			}
			return g.call(cmd, "plan.create", req, printPlan)
		}}
	f.bind(cmd)
	return cmd
}

func approveCmd(g *globalFlags) *cobra.Command {
	var fp string
	cmd := &cobra.Command{Use: "approve PLAN --fingerprint FP", Short: "Approve a plan exactly as reviewed (starts it if an agent asked for it)",
		Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			return g.call(cmd, "plan.approve", map[string]string{"plan_id": args[0], "fingerprint": fp}, nil)
		}}
	cmd.Flags().StringVar(&fp, "fingerprint", "", "the fingerprint shown by `ferry plan`")
	_ = cmd.MarkFlagRequired("fingerprint")
	return cmd
}

func sendCmd(g *globalFlags) *cobra.Command {
	f := &planFlags{}
	var yes, wait bool
	cmd := &cobra.Command{Use: "send SOURCE... --to DEST", Short: "Plan, approve and run a verified copy in one step", Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			req, err := f.request(args)
			if err != nil {
				return err
			}
			c, err := g.dial(cmd.Context())
			if err != nil {
				return err
			}
			defer func() { _ = c.Close() }()
			var raw json.RawMessage
			if err := c.Call(cmd.Context(), "plan.create", req, &raw); err != nil {
				return err
			}
			var p daemon.PlanSummary
			_ = json.Unmarshal(raw, &p)
			out := cmd.OutOrStdout()
			if !g.json {
				_ = printPlan(out, raw)
			}
			if p.Blocking > 0 {
				return &exitError{code: 1, err: errors.New("the plan has blocking items; nothing was copied")}
			}
			if !yes {
				fmt.Fprintf(out, "review, then run: ferry approve %s --fingerprint %s   (or re-run with --yes)\n", p.ID, p.Fingerprint)
				return nil
			}
			if wait {
				if err := c.Call(cmd.Context(), "events.subscribe", map[string]any{"topics": []string{"transfer.*"}}, nil); err != nil {
					return err
				}
			}
			var started map[string]any
			if err := c.Call(cmd.Context(), "transfer.start", map[string]string{"plan_id": p.ID, "fingerprint": p.Fingerprint}, &started); err != nil {
				return err
			}
			id, _ := started["transfer_id"].(string)
			if !wait {
				return g.print(out, mustJSON(started), nil)
			}
			return follow(cmd.Context(), c, id, out, g.json)
		}}
	f.bind(cmd)
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "approve the plan as built and start it")
	cmd.Flags().BoolVar(&wait, "wait", true, "stream progress until the transfer finishes")
	return cmd
}

func mustJSON(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

var terminal = map[string]bool{model.TransferCompleted: true, model.TransferPartial: true, model.TransferFailed: true,
	model.TransferCanceled: true, model.TransferNeedsAttention: true}

// follow prints live progress for a transfer until it ends; exit code 0
// only for completed.
func follow(ctx context.Context, c *ipc.Client, id string, w io.Writer, asJSON bool) error {
	last := time.Time{}
	for ev := range c.Events() {
		b, _ := json.Marshal(ev.Data)
		switch ev.Topic {
		case "transfer.progress":
			var p engine.Progress
			_ = json.Unmarshal(b, &p)
			if p.TransferID != id || (!asJSON && time.Since(last) < time.Second) {
				continue
			}
			last = time.Now()
			if asJSON {
				fmt.Fprintln(w, string(b))
				continue
			}
			fmt.Fprintf(w, "%5.1f%%  %d/%d bytes verified  ETA %s\n", p.OverallFraction*100, p.BytesVerified, p.TotalBytes, eta(p.ETASeconds))
		case "transfer.state":
			var s map[string]string
			_ = json.Unmarshal(b, &s)
			if s["transfer_id"] != id || !terminal[s["status"]] {
				continue
			}
			var res map[string]any
			_ = c.Call(ctx, "transfer.get", map[string]string{"transfer_id": id}, &res)
			if asJSON {
				fmt.Fprintln(w, string(mustJSON(res)))
			} else {
				fmt.Fprintf(w, "transfer %s %s  receipt %v\n", id, s["status"], res["receipt_path"])
			}
			if s["status"] != model.TransferCompleted {
				return &exitError{code: 1}
			}
			return nil
		}
	}
	return errors.New("daemon connection closed before the transfer finished")
}

func eta(s float64) string {
	if s < 0 {
		return "unknown"
	}
	return (time.Duration(s) * time.Second).String()
}

func statusCmd(g *globalFlags) *cobra.Command {
	var watch bool
	cmd := &cobra.Command{Use: "status [TRANSFER]", Short: "Daemon status, or one transfer's status", Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return g.call(cmd, "daemon.status", nil, nil)
			}
			if !watch {
				return g.call(cmd, "transfer.get", map[string]string{"transfer_id": args[0]}, nil)
			}
			c, err := g.dial(cmd.Context())
			if err != nil {
				return err
			}
			defer func() { _ = c.Close() }()
			if err := c.Call(cmd.Context(), "events.subscribe", map[string]any{"topics": []string{"transfer.*"}}, nil); err != nil {
				return err
			}
			return follow(cmd.Context(), c, args[0], cmd.OutOrStdout(), g.json)
		}}
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "stream progress until the transfer ends")
	return cmd
}

func peerCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "peer", Short: "Pair and manage machines"}
	cmd.AddCommand(
		&cobra.Command{Use: "pair [CODE]", Short: "Without CODE: open a pairing window and print a code. With CODE: pair with the machine that printed it.",
			Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
				if len(args) == 0 {
					return g.call(cmd, "peer.pair.start", map[string]int{"ttl_seconds": 300}, func(w io.Writer, raw json.RawMessage) error {
						var m map[string]any
						_ = json.Unmarshal(raw, &m)
						fmt.Fprintf(w, "On the other machine run (valid 5 minutes, single use):\n  ferry peer pair '%s'\n", m["code"])
						return nil
					})
				}
				return g.call(cmd, "peer.pair.complete", map[string]string{"code": args[0]}, nil)
			}},
		&cobra.Command{Use: "list", Short: "Paired machines and their active path", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			return g.call(cmd, "peer.list", nil, nil)
		}},
		simpleCmd(g, "probe PEER", "Run Path Radar against a peer", "peer.probe", "peer_id"),
		simpleCmd(g, "remove PEER", "Unpin a peer; its key is refused from then on", "peer.remove", "peer_id"),
	)
	return cmd
}

func driveCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "drive", Short: "Mounted drives"}
	cmd.AddCommand(
		&cobra.Command{Use: "list", Short: "Drives with identity, free space and card detection", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			return g.call(cmd, "drive.list", nil, nil)
		}},
		&cobra.Command{Use: "watch", Short: "Print drive.mounted / drive.unmounted events", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			return watchEvents(cmd, g, []string{"drive.*"})
		}},
	)
	return cmd
}

func eventsCmd(g *globalFlags) *cobra.Command {
	return &cobra.Command{Use: "events [TOPIC...]", Short: "Stream daemon events (default: all)", RunE: func(cmd *cobra.Command, args []string) error {
		return watchEvents(cmd, g, args)
	}}
}

func watchEvents(cmd *cobra.Command, g *globalFlags, topics []string) error {
	c, err := g.dial(cmd.Context())
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	if err := c.Call(cmd.Context(), "events.subscribe", map[string]any{"topics": topics}, nil); err != nil {
		return err
	}
	for ev := range c.Events() {
		fmt.Fprintln(cmd.OutOrStdout(), string(mustJSON(ev)))
	}
	return nil
}

func destCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "dest", Short: "Saved destinations"}
	var req planner.SaveDestinationRequest
	save := &cobra.Command{Use: "save NAME PATH", Short: "Save a destination with its storage identity", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			req.Name, req.Path = args[0], absPath(args[1])
			return g.call(cmd, "dest.save", req, nil)
		}}
	save.Flags().StringVar(&req.PresetID, "preset", "", "pinned preset id")
	save.Flags().IntVar(&req.PresetRev, "preset-rev", 0, "pinned preset revision (default latest)")
	save.Flags().StringVar(&req.DefaultConflict, "conflict", "", "default conflict policy")
	save.Flags().Int64Var(&req.ReserveBytes, "reserve", 0, "bytes to keep free")
	var archived bool
	list := &cobra.Command{Use: "list", Short: "Saved destinations with live availability", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return g.call(cmd, "dest.list", map[string]bool{"include_archived": archived}, nil)
	}}
	list.Flags().BoolVar(&archived, "all", false, "include archived")
	cmd.AddCommand(save, list,
		simpleCmd(g, "resolve ID", "Is the saved destination available right now, and where?", "dest.resolve", "id"),
		&cobra.Command{Use: "rebind ID PATH", Short: "Bind a saved destination to a new folder (invalidates its pending plans)", Args: cobra.ExactArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				return g.call(cmd, "dest.rebind", map[string]string{"id": args[0], "path": absPath(args[1])}, nil)
			}},
		simpleCmd(g, "archive ID", "Hide a saved destination (history keeps it)", "dest.archive", "id"),
	)
	return cmd
}

func presetCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "preset", Short: "Organization presets"}
	var rev int
	var id string
	show := &cobra.Command{Use: "show ID", Args: cobra.ExactArgs(1), Short: "Show a preset revision", RunE: func(cmd *cobra.Command, args []string) error {
		return g.call(cmd, "preset.get", map[string]any{"id": args[0], "revision": rev}, nil)
	}}
	show.Flags().IntVar(&rev, "rev", 0, "revision (default latest)")
	export := &cobra.Command{Use: "export ID", Args: cobra.ExactArgs(1), Short: "Print portable preset JSON", RunE: func(cmd *cobra.Command, args []string) error {
		return g.call(cmd, "preset.export", map[string]any{"id": args[0], "revision": rev}, nil)
	}}
	export.Flags().IntVar(&rev, "rev", 0, "revision (default latest)")
	save := &cobra.Command{Use: "save FILE", Args: cobra.ExactArgs(1), Short: "Create a preset (or a new revision with --id) from JSON",
		RunE: func(cmd *cobra.Command, args []string) error {
			b, err := os.ReadFile(args[0])
			if err != nil {
				return err
			}
			return g.call(cmd, "preset.save", map[string]any{"id": id, "preset": json.RawMessage(b)}, nil)
		}}
	save.Flags().StringVar(&id, "id", "", "existing preset to revise")
	var label string
	preview := &cobra.Command{Use: "preview ID PATH...", Args: cobra.MinimumNArgs(2), Short: "Show where source-relative paths would route",
		RunE: func(cmd *cobra.Command, args []string) error {
			return g.call(cmd, "preset.preview", map[string]any{"id": args[0], "revision": rev, "source_label": label, "paths": args[1:]}, nil)
		}}
	preview.Flags().StringVar(&label, "label", "source", "source label to render")
	preview.Flags().IntVar(&rev, "rev", 0, "revision (default latest)")
	cmd.AddCommand(
		&cobra.Command{Use: "list", Short: "List presets", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			return g.call(cmd, "preset.list", nil, nil)
		}},
		show, save, export, preview,
		&cobra.Command{Use: "import FILE", Args: cobra.ExactArgs(1), Short: "Import portable preset JSON as a new preset", RunE: func(cmd *cobra.Command, args []string) error {
			b, err := os.ReadFile(args[0])
			if err != nil {
				return err
			}
			return g.call(cmd, "preset.import", map[string]any{"json": json.RawMessage(b)}, nil)
		}},
	)
	return cmd
}

func cameraCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "camera", Short: "Camera role bindings"}
	cmd.AddCommand(&cobra.Command{Use: "bind MAKE MODEL SERIAL ROLE", Short: "Remember a camera's role (e.g. ACAM)", Args: cobra.ExactArgs(4),
		RunE: func(cmd *cobra.Command, a []string) error {
			return g.call(cmd, "camera.bind", map[string]string{"make": a[0], "model": a[1], "serial": a[2], "role": a[3]}, nil)
		}})
	return cmd
}

func receiptCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "receipt", Short: "Transfer receipts"}
	var transfer string
	list := &cobra.Command{Use: "list", Short: "List receipts", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return g.call(cmd, "receipt.list", map[string]string{"transfer_id": transfer}, nil)
	}}
	list.Flags().StringVar(&transfer, "transfer", "", "only this transfer")
	cmd.AddCommand(list,
		simpleCmd(g, "show RECEIPT", "Print a receipt", "receipt.get", "receipt_id"),
		simpleCmd(g, "export RECEIPT", "Write a receipt's JSON file (retry a failed export)", "receipt.export", "receipt_id"))
	return cmd
}

func doctorCmd(g *globalFlags) *cobra.Command {
	return &cobra.Command{Use: "doctor", Short: "Check config, database, socket, identity and roots", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, paths, err := g.resolve()
			if err != nil {
				return &exitError{code: 1, err: fmt.Errorf("config: %w", err)}
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "config %s ok (%d roots)\n", paths.Config, len(cfg.Roots))
			var checks []daemon.Check
			if c, err := g.dial(cmd.Context()); err == nil {
				defer func() { _ = c.Close() }()
				if err := c.Call(cmd.Context(), "doctor.run", nil, &checks); err != nil {
					return err
				}
			} else {
				// No daemon: still check everything that does not need one.
				fmt.Fprintf(cmd.ErrOrStderr(), "daemon not running (%s); local checks only\n", paths.Socket)
				checks = daemon.LocalChecks(cfg, paths)
			}
			bad := 0
			for _, ch := range checks {
				if !ch.OK {
					bad++
				}
			}
			if g.json {
				_ = g.print(cmd.OutOrStdout(), mustJSON(checks), nil)
			} else {
				for _, ch := range checks {
					mark := "ok  "
					if !ch.OK {
						mark = "FAIL"
					}
					fmt.Fprintf(cmd.OutOrStdout(), "%s %-28s %s\n", mark, ch.Name, ch.Detail)
				}
			}
			if bad > 0 {
				return &exitError{code: 1}
			}
			return nil
		}}
}
