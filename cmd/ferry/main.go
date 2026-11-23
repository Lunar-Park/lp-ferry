// Command ferry is the single Ferry binary: daemon, CLI client and MCP
// server. Subcommands land phase by phase (see docs/GO-BACKEND-REWRITE-PLAN.md).
package main

import (
	"errors"
	"fmt"
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
	root.PersistentFlags().BoolVar(&g.json, "json", false, "machine-readable JSON output")

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
	return root
}

// exitError carries a specific process exit code out of a command.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string {
	if e.err == nil {
		return fmt.Sprintf("exit %d", e.code)
	}
	return e.err.Error()
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	root := newRootCmd()
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		var ee *exitError
		if errors.As(err, &ee) {
			if ee.err != nil {
				fmt.Fprintln(os.Stderr, "ferry:", ee.err)
			}
			return ee.code
		}
		fmt.Fprintln(os.Stderr, "ferry:", err)
		return 4
	}
	return 0
}
