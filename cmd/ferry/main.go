// Command ferry is the single Ferry binary: daemon, CLI client and MCP
// server. Subcommands land phase by phase (see docs/GO-BACKEND-REWRITE-PLAN.md).
package main

import (
	"fmt"
	"io"
	"os"
	"runtime"

	"github.com/spf13/cobra"
)

// version is stamped at build time with -ldflags "-X main.version=...".
var version = "dev"

// globalFlags are shared by every subcommand.
type globalFlags struct {
	socket string
	config string
	json   bool
	// actor is who this call is from: "human" (D), or an agent's name, which the daemon
	// answers to policy as `agent:<name>`. FERRY_ACTOR, then PARK_ACTOR, then nothing (human).
	actor string
}

func newRootCmd() *cobra.Command {
	g := &globalFlags{}
	root := &cobra.Command{
		Use:           "ferry",
		Short:         "Verified, local-first file transport",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&g.socket, "socket", "", "daemon socket path (default ~/.ferry/ferry.sock)")
	root.PersistentFlags().StringVar(&g.config, "config", "", "config file (default ~/.ferry/ferry.toml)")
	root.PersistentFlags().BoolVar(&g.json, "json", false, "machine-readable JSON output (errors too: {\"error\":{\"code\",\"message\"}} on stderr)")
	root.PersistentFlags().StringVar(&g.actor, "actor", defaultActor(),
		"who this call is from: human, or an agent's name (FERRY_ACTOR, then PARK_ACTOR)")
	// A flag cobra could not parse is a usage error, not a refusal.
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usagef("%s", err) })

	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print the ferry version",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if g.json {
				_, err := fmt.Fprintf(cmd.OutOrStdout(), "{\"version\":%q,\"go\":%q,\"os\":%q,\"arch\":%q}\n",
					version, runtime.Version(), runtime.GOOS, runtime.GOARCH)
				return err
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "ferry %s (%s %s/%s)\n", version, runtime.Version(), runtime.GOOS, runtime.GOARCH)
			return err
		},
	})
	addVerifyCmd(root, g)
	addCommands(root, g)
	return root
}

// exitError carries a specific process exit code out of a command.
type exitError struct {
	code int
	kind string
	err  error
}

func (e *exitError) Error() string {
	if e.err == nil {
		return fmt.Sprintf("exit %d", e.code)
	}
	return e.err.Error()
}

// kindName is the machine name of this failure: the one the command gave it, or one read from
// its code.
func (e *exitError) kindName() string {
	if e.kind != "" {
		return e.kind
	}
	return kindForCode(e.code)
}

// defaultActor is who a call is from when nothing said: the agent's own name from its shell's
// environment (so an agent's ferry calls answer to the agent policy), else D.
func defaultActor() string {
	for _, key := range []string{"FERRY_ACTOR", "PARK_ACTOR"} {
		if v := os.Getenv(key); v != "" {
			return v
		}
	}
	return "human"
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	asJSON := wantsJSON(args)
	root := newRootCmd()
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	if err := root.Execute(); err != nil {
		code, kind := classify(err)
		writeError(stderr, asJSON, code, kind, err.Error())
		return code
	}
	return exitOK
}
