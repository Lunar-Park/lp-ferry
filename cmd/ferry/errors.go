package main

// The error contract an agent reads (docs: AGENT-CLI or `ferry --help`). A program calling
// `ferry --json` gets one shape on stderr whatever went wrong:
//
//	{"error": {"code": "usage", "message": "ferry plan: accepts 1 arg(s), received 0"}}
//
// and a process exit code that says which kind of thing it was, so a caller never has to read
// English to decide whether to retry:
//
//	0  ok
//	1  refused: the daemon said no, or the work failed (domain errors, a policy refusal,
//	   a blocking plan, a transfer that did not complete)
//	2  usage: the command was written wrong (unknown flag, missing or extra argument)
//	3  environment: no daemon at the socket, or a config that could not be read
//
// `ferry verify` keeps its own documented codes 0-4 (it is a cron check, not a CLI call), and
// carries them through unchanged.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// The exit codes of the contract above.
const (
	exitOK      = 0
	exitRefused = 1
	exitUsage   = 2
	exitEnv     = 3
)

// cliError is an error that already knows its code and machine name.
type cliError struct {
	Code int
	Kind string
	Err  error
}

func (e *cliError) Error() string { return e.Err.Error() }
func (e *cliError) Unwrap() error { return e.Err }

// usagef is an error in how the command was written: exit 2.
func usagef(format string, a ...any) error {
	return &cliError{Code: exitUsage, Kind: "usage", Err: fmt.Errorf(format, a...)}
}

// envError is something about this machine, not the command: exit 3.
func envError(kind string, err error) error {
	return &cliError{Code: exitEnv, Kind: kind, Err: err}
}

// errorBody is the JSON an agent parses.
type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// classify says what an error is and which exit code it earns. A command that raised its own
// code (verify, doctor) keeps it; everything else is read from the shape of the error.
func classify(err error) (code int, kind string) {
	var ee *exitError
	if errors.As(err, &ee) {
		return ee.code, ee.kindName()
	}
	var ce *cliError
	if errors.As(err, &ce) {
		return ce.Code, ce.Kind
	}
	if usageShaped(err) {
		return exitUsage, "usage"
	}
	return exitRefused, "refused"
}

// usageShaped recognises the errors cobra itself raises for arguments it validated: they are
// plain formatted strings, and they are the only ones that mean "the command was written wrong".
func usageShaped(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, prefix := range []string{
		"unknown flag", "unknown shorthand flag", "unknown command", "flag needs an argument",
		"invalid argument", "accepts ", "requires at least", "requires between",
		"requires exactly", "required flag", "arg(s), received",
	} {
		if strings.HasPrefix(msg, prefix) || strings.HasPrefix(msg, "ferry: "+prefix) {
			return true
		}
	}
	return false
}

// kindForCode is the machine name for a bare exit code a command chose without naming one.
func kindForCode(code int) string {
	switch code {
	case exitOK:
		return "ok"
	case exitRefused:
		return "refused"
	case exitUsage:
		return "usage"
	case exitEnv:
		return "unreachable"
	default:
		return "failed"
	}
}

// writeError prints the failure the way the caller asked for it: one JSON object on stderr
// with --json, the sentence a person reads otherwise.
func writeError(w io.Writer, asJSON bool, code int, kind, message string) {
	if !asJSON {
		if message != "" {
			fmt.Fprintln(w, "ferry:", message)
		}
		return
	}
	var body errorBody
	body.Error.Code = kind
	if body.Error.Code == "" {
		body.Error.Code = kindForCode(code)
	}
	body.Error.Message = message
	if body.Error.Message == "" {
		body.Error.Message = "ferry " + body.Error.Code
	}
	enc := json.NewEncoder(w)
	_ = enc.Encode(body)
}

// wantsJSON says whether --json was asked for, read from the arguments themselves: a flag the
// root command could not parse still has to produce the JSON the caller asked for.
func wantsJSON(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if a == "--json" {
			return true
		}
		if value, ok := strings.CutPrefix(a, "--json="); ok {
			return value != "false" && value != "0"
		}
	}
	return false
}

// actorName turns --actor (or FERRY_ACTOR / PARK_ACTOR) into what the daemon expects: "human",
// or "agent:<name>". A bare name is an agent's, which is what makes an agent's ferry calls
// answer to the agent policy rather than to D's own allowances.
func actorName(raw string) string {
	name := strings.TrimSpace(raw)
	if name == "" || name == "human" {
		return "human"
	}
	if strings.HasPrefix(name, "agent:") {
		return name
	}
	return "agent:" + name
}
