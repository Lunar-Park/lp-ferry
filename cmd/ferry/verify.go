package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/Lunar-Park/lp-ferry/internal/receipt"
	"github.com/Lunar-Park/lp-ferry/internal/verify"
)

// verifyKind names this command's failures in JSON: they are a check's outcome, not the CLI
// contract's four codes, so a caller reads the category rather than guessing from a number.
const verifyKind = "verify"

func verifyKindFor(rep *verify.Report) string {
	switch rep.ExitCode {
	case 0:
		return "ok"
	case verify.ExitMissing:
		return "verify_missing"
	case verify.ExitModified:
		return "verify_modified"
	case verify.ExitAdded:
		return "verify_added"
	default:
		return verifyKind
	}
}

func addVerifyCmd(root *cobra.Command, g *globalFlags) {
	var receiptPath, rootOverride, target, transferID, receiptID string
	cmd := &cobra.Command{
		Use:   "verify (--receipt FILE | --transfer ID | --receipt-id ID)",
		Short: "Re-hash landed files against a receipt with physical disk readback",
		Long: `Re-reads every file a receipt says landed, bypassing the page cache, and
compares it with the recorded hash. Built for cron:

  0  clean
  1  missing   (a landed file is gone)
  2  modified  (a landed file's bytes changed)
  3  added     (a new file appeared in a folder the transfer wrote)
  4  could not verify (unreadable file, unreachable root, invalid or edited receipt)

When several categories apply the lowest non-zero code above 0 wins, except
4, which outranks everything: an incomplete check proves nothing.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if transferID != "" || receiptID != "" {
				// Through the daemon: local targets and targets on peers.
				c, err := g.dial(cmd.Context())
				if err != nil {
					return &exitError{code: verify.ExitUnreadable, kind: verifyKind, err: err}
				}
				defer func() { _ = c.Close() }()
				var rep verify.Report
				if err := c.Call(cmd.Context(), "verify.run", map[string]string{"transfer_id": transferID, "receipt_id": receiptID, "target_id": target}, &rep); err != nil {
					return &exitError{code: verify.ExitUnreadable, kind: verifyKind, err: err}
				}
				return report(cmd, g, &rep)
			}
			if receiptPath == "" {
				return &exitError{code: verify.ExitUnreadable, kind: verifyKind, err: errors.New("--receipt, --transfer or --receipt-id is required")}
			}
			data, err := os.ReadFile(receiptPath)
			if err != nil {
				return &exitError{code: verify.ExitUnreadable, kind: verifyKind, err: err}
			}
			rc, err := receipt.Parse(data)
			if err != nil {
				return &exitError{code: verify.ExitUnreadable, kind: verifyKind, err: err}
			}
			rep, err := verify.Verify(cmd.Context(), rc, verify.Options{TargetID: target, Root: rootOverride})
			if err != nil {
				return &exitError{code: verify.ExitUnreadable, kind: verifyKind, err: err}
			}
			return report(cmd, g, rep)
		},
	}
	cmd.Flags().StringVar(&receiptPath, "receipt", "", "ferry-receipt-v2 JSON file")
	cmd.Flags().StringVar(&rootOverride, "root", "", "verify against this folder instead of the recorded root (one target)")
	cmd.Flags().StringVar(&target, "target", "", "verify only this target ID")
	cmd.Flags().StringVar(&transferID, "transfer", "", "verify a transfer's latest receipt through the daemon (local and peer targets)")
	cmd.Flags().StringVar(&receiptID, "receipt-id", "", "verify a stored receipt through the daemon")
	root.AddCommand(cmd)
}

func printReport(w io.Writer, rep *verify.Report) {
	verdict := "clean"
	if !rep.Clean {
		verdict = fmt.Sprintf("NOT clean (exit %d)", rep.ExitCode)
	}
	fmt.Fprintf(w, "receipt %s · %s · readback %s · %s\n", rep.ReceiptID, rep.ChecksumAlgo, rep.ReadbackMode, verdict)
	for _, t := range rep.Targets {
		fmt.Fprintf(w, "  %s (%s): %d checked, %d missing, %d modified, %d added, %d unreadable\n",
			t.Label, t.Root, t.Checked, len(t.Missing), len(t.Modified), len(t.Added), len(t.Unreadable))
		for _, p := range t.Missing {
			fmt.Fprintf(w, "    missing   %s\n", p)
		}
		for _, p := range t.Modified {
			fmt.Fprintf(w, "    modified  %s\n", p)
		}
		for _, p := range t.Added {
			fmt.Fprintf(w, "    added     %s\n", p)
		}
		for _, p := range t.Unreadable {
			fmt.Fprintf(w, "    unreadable %s: %s\n", p.Path, p.Error)
		}
	}
}

func report(cmd *cobra.Command, g *globalFlags, rep *verify.Report) error {
	out := cmd.OutOrStdout()
	if g.json {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			return err
		}
	} else {
		printReport(out, rep)
	}
	if rep.ExitCode != 0 {
		return &exitError{code: rep.ExitCode, kind: verifyKindFor(rep)}
	}
	return nil
}
