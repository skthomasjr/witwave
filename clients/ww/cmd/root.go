// Package cmd wires the ww CLI's cobra command tree.
package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/skthomasjr/autonomous-agent/clients/ww/internal/client"
	"github.com/skthomasjr/autonomous-agent/clients/ww/internal/config"
	"github.com/skthomasjr/autonomous-agent/clients/ww/internal/output"
	"github.com/spf13/cobra"
)

// Version info — overwritten at build time via -ldflags.
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

// Globals populated by PersistentPreRunE so subcommands can reach them
// without re-parsing flags.
type contextKey struct{ name string }

var (
	ctxKeyClient = &contextKey{"client"}
	ctxKeyOut    = &contextKey{"out"}
	ctxKeyCfg    = &contextKey{"cfg"}
)

// ClientFromCtx retrieves the configured HTTP client or panics.
func ClientFromCtx(ctx context.Context) *client.Client {
	c, _ := ctx.Value(ctxKeyClient).(*client.Client)
	if c == nil {
		panic("no client in context")
	}
	return c
}

// OutFromCtx retrieves the configured output writer.
func OutFromCtx(ctx context.Context) *output.Writer {
	o, _ := ctx.Value(ctxKeyOut).(*output.Writer)
	if o == nil {
		panic("no output writer in context")
	}
	return o
}

// CfgFromCtx retrieves the resolved config.
func CfgFromCtx(ctx context.Context) config.Resolved {
	c, _ := ctx.Value(ctxKeyCfg).(config.Resolved)
	return c
}

// persistent flag values
type rootFlags struct {
	configPath string
	profile    string
	baseURL    string
	token      string
	runToken   string
	timeout    time.Duration
	verbose    int
	jsonOut    bool
	compact    bool
}

// Execute runs the root command. Returns the intended exit code.
func Execute() int {
	root, rf := newRoot()
	root.AddCommand(newVersionCmd())
	root.AddCommand(newStatusCmd())
	root.AddCommand(newTailCmd())
	root.AddCommand(newSendCmd())
	root.AddCommand(newJobsCmd())
	root.AddCommand(newTasksCmd())
	root.AddCommand(newHeartbeatCmd())
	root.AddCommand(newTriggersCmd())
	root.AddCommand(newContinuationsCmd())
	root.AddCommand(newValidateCmd())

	if err := root.Execute(); err != nil {
		// Errors already printed by runWithExit helper; fall back here
		// if cobra reports a flag parse error or similar.
		var ce *commandErr
		if ok := extract(err, &ce); ok {
			return ce.code
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		return client.ExitLogical
	}
	_ = rf // quiet unused warning for release builds with small command sets
	return 0
}

func newRoot() (*cobra.Command, *rootFlags) {
	rf := &rootFlags{}
	cmd := &cobra.Command{
		Use:   "ww",
		Short: "witwave CLI — operate the Witwave agent platform",
		Long: "ww is the command-line companion for the Witwave / witwave agent platform.\n" +
			"It talks to a harness over the shared event + REST surface: tail the live\n" +
			"event stream, send A2A prompts, inspect jobs / tasks / triggers, and\n" +
			"validate scheduler files — all without a browser.",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cc *cobra.Command, args []string) error {
			resolved, err := config.Load(rf.configPath, config.FlagOverrides{
				Profile:  rf.profile,
				BaseURL:  rf.baseURL,
				Token:    rf.token,
				RunToken: rf.runToken,
				Timeout:  rf.timeout,
			}, os.Getenv)
			if err != nil {
				return transportErr(err)
			}
			var logger io.Writer
			if rf.verbose > 0 {
				logger = os.Stderr
			}
			hc := client.New(client.Config{
				BaseURL:  resolved.BaseURL,
				Token:    resolved.Token,
				RunToken: resolved.RunToken,
				Timeout:  resolved.Timeout,
				Verbose:  rf.verbose,
				Logger:   logger,
			})
			out := output.New(os.Stdout, os.Stderr, rf.jsonOut, rf.compact)
			if rf.verbose > 0 {
				resolved.Dump(os.Stderr)
			}
			ctx := cc.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			ctx = context.WithValue(ctx, ctxKeyClient, hc)
			ctx = context.WithValue(ctx, ctxKeyOut, out)
			ctx = context.WithValue(ctx, ctxKeyCfg, resolved)
			cc.SetContext(ctx)
			return nil
		},
	}
	p := cmd.PersistentFlags()
	p.StringVar(&rf.configPath, "config", "", "path to ww config (default: $XDG_CONFIG_HOME/ww/config.toml)")
	p.StringVar(&rf.profile, "profile", "", "config profile to use (default: default)")
	p.StringVar(&rf.baseURL, "base-url", "", "harness base URL (overrides config)")
	p.StringVar(&rf.token, "token", "", "bearer token for conversations/events endpoints")
	p.StringVar(&rf.runToken, "run-token", "", "bearer token for ad-hoc run endpoints")
	p.DurationVar(&rf.timeout, "timeout", 0, "per-request timeout (stream commands ignore)")
	p.CountVarP(&rf.verbose, "verbose", "v", "increase verbosity (-v: requests, -vv: bodies)")
	p.BoolVar(&rf.jsonOut, "json", false, "emit JSON (pretty for snapshots, line-delimited for streams)")
	p.BoolVar(&rf.compact, "compact", false, "with --json, emit compact single-line JSON for snapshots")
	return cmd, rf
}

// commandErr is a sentinel returned by subcommand RunE when we want to
// carry a specific exit code up through cobra.
type commandErr struct {
	code int
	msg  string
}

func (e *commandErr) Error() string { return e.msg }

// extract returns true if err is (or wraps) *commandErr and stashes it.
func extract(err error, out **commandErr) bool {
	for err != nil {
		if ce, ok := err.(*commandErr); ok {
			*out = ce
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func transportErr(err error) error {
	return &commandErr{code: client.ExitTransport, msg: err.Error()}
}

func logicalErr(err error) error {
	return &commandErr{code: client.ExitLogical, msg: err.Error()}
}

// handleErr renders err via out and returns a wrapped commandErr cobra
// will propagate to Execute. Transport errors (network, timeout) get
// exit 2; HTTP 4xx and logic errors get exit 1.
func handleErr(out *output.Writer, err error) error {
	if err == nil {
		return nil
	}
	if he, ok := client.IsHTTPError(err); ok {
		out.Errorf("%s", he.Error())
		if he.StatusCode >= 500 {
			return transportErr(err)
		}
		return logicalErr(err)
	}
	out.Errorf("%v", err)
	return transportErr(err)
}
