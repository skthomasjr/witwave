package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/witwave-ai/witwave/clients/ww/internal/conversation"
	"github.com/witwave-ai/witwave/clients/ww/internal/k8s"
	"github.com/witwave-ai/witwave/clients/ww/internal/portforward"
)

// conversationFlags is the persistent-flag set for the `ww conversation`
// subtree — namespace + cluster-wide. Mirrors the operator subtree's
// flag pattern so users get consistent muscle memory between operator
// and tenant subtrees.
type conversationFlags struct {
	namespace     string
	allNamespaces bool
}

func newConversationCmd() *cobra.Command {
	f := &conversationFlags{}
	cmd := &cobra.Command{
		Use:   "conversation",
		Short: "Inspect agent conversation transcripts (LLM exchanges)",
		Long: "Read the LLM exchange (prompt, reply, tool calls, session metadata) from\n" +
			"any agent's harness via its `/conversations` endpoint. The CLI handles\n" +
			"port-forward + auth automatically; you only need RBAC for kubeconfig\n" +
			"access to the agent's namespace.\n\n" +
			"Subcommands:\n" +
			"  list   — table of recent sessions (one row per (agent, session))\n" +
			"  show   — full transcript of one session by id\n" +
			"\n" +
			"Auth: each harness expects a bearer token (CONVERSATIONS_AUTH_TOKEN). The\n" +
			"CLI reads the token from each agent's <agent>-claude Secret when available;\n" +
			"override with --token. Harnesses with the auth-disabled escape hatch\n" +
			"(CONVERSATIONS_AUTH_DISABLED=true) accept unauthenticated requests.",
	}
	cmd.PersistentFlags().StringVarP(&f.namespace, "namespace", "n", "",
		"Namespace scope (default: kubeconfig context's namespace, falling back to 'witwave')")
	cmd.PersistentFlags().BoolVarP(&f.allNamespaces, "all-namespaces", "A", false,
		"Fan out across every namespace the user has RBAC for")

	cmd.AddCommand(newConversationListCmd(f))
	cmd.AddCommand(newConversationShowCmd(f))
	return cmd
}

// resolveTarget mirrors the operator subtree's helper so this subtree
// builds its REST config + namespace through the same kubeconfig
// loader. -A overrides namespace at fan-out time so we keep the
// resolver consistent with other tenant-scoped commands.
func (f *conversationFlags) resolveTarget(ctx context.Context) (*k8s.Target, *k8s.Resolver, error) {
	kc := K8sFromCtx(ctx)
	r, err := k8s.NewResolver(k8s.Options{
		KubeconfigPath: kc.Kubeconfig,
		Context:        kc.Context,
		Namespace:      f.namespace,
	})
	if err != nil {
		return nil, nil, err
	}
	return r.Target(), r, nil
}

// =====================================================================
// list
// =====================================================================

func newConversationListCmd(f *conversationFlags) *cobra.Command {
	var (
		agent    string
		since    time.Duration
		limit    int
		token    string
		quiet    bool
		expand   bool
		fullText bool
		follow   bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "Show recent agent conversation sessions",
		Long: "Lists conversation sessions across the agents in scope. Default scope is\n" +
			"the kubeconfig context's namespace; pass -A to fan out cluster-wide, or\n" +
			"--agent <name> to narrow to one peer.\n\n" +
			"Output columns: NAMESPACE (when -A), SESSION ID, AGENT, STARTED, LAST\n" +
			"ACTIVITY, TURNS, SOURCE. Sorted by last-activity descending.\n\n" +
			"One agent unreachable (port-forward fails, harness mid-roll, RBAC denies\n" +
			"the secret read) is reported in a footer rather than aborting the list.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			_, resolver, err := f.resolveTarget(ctx)
			if err != nil {
				return err
			}
			cfg, err := resolver.REST()
			if err != nil {
				return err
			}
			ns := f.namespace
			if !f.allNamespaces {
				ns = logAndResolveNamespace(f.namespace, resolver.Target().Namespace)
			} else if !quiet {
				fmt.Fprintln(os.Stderr, "Scope: cluster-wide (-A)")
			}

			targets, err := conversation.DiscoverAgents(ctx, cfg, ns, f.allNamespaces)
			if err != nil {
				return err
			}
			if agent != "" {
				targets = filterTargetsByAgent(targets, agent)
				if len(targets) == 0 {
					return fmt.Errorf("no WitwaveAgent named %q found in scope", agent)
				}
			}
			if len(targets) == 0 {
				fmt.Println("No WitwaveAgents found in scope.")
				return nil
			}

			opts := conversation.ListOptions{Limit: limit}
			if since > 0 {
				opts.Since = time.Now().Add(-since).UTC().Format(time.RFC3339)
			}

			tokenFn := makeTokenLookup(ctx, cfg, token)
			results := conversation.FanOutList(ctx, cfg, targets, opts, tokenFn)
			summaries, unreachable := conversation.MergeAndSummarize(results)
			if expand {
				// Build the per-session entry map so renderListExpanded
				// can attach the inline transcript under each summary
				// header. Each FanOutResult's Entries cover ALL sessions
				// for that agent; group by SessionID.
				bySession := make(map[string][]conversation.Entry)
				for _, r := range results {
					if r.Err != nil {
						continue
					}
					for _, e := range r.Entries {
						if e.SessionID == "" {
							continue
						}
						bySession[e.SessionID] = append(bySession[e.SessionID], e)
					}
				}
				renderListExpanded(os.Stdout, summaries, bySession, fullText)
			} else {
				renderListTable(os.Stdout, summaries, f.allNamespaces)
			}
			renderUnreachableFooter(os.Stderr, unreachable)

			if follow {
				return followAllSessions(ctx, cfg, summaries, results, tokenFn)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&agent, "agent", "",
		"Filter to one agent within the scope")
	cmd.Flags().DurationVar(&since, "since", 0,
		"Only list sessions with activity newer than this duration (e.g. 1h, 30m)")
	cmd.Flags().IntVar(&limit, "limit", 0,
		"Per-agent cap on entries returned by the harness (0 = harness default)")
	cmd.Flags().StringVar(&token, "token", "",
		"Override the per-agent bearer token (default: read from each agent's credentials Secret)")
	cmd.Flags().BoolVar(&quiet, "quiet", false,
		"Suppress informational stderr output (scope banner, unreachable footer)")
	cmd.Flags().BoolVar(&expand, "expand", false,
		"Render each session as a boxed card with the transcript inline (Option-A combined view)")
	cmd.Flags().BoolVar(&fullText, "full-text", false,
		"With --expand: don't truncate per-entry text at 500 chars")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false,
		"After printing the initial view, live-tail every session via SSE — appends new turns inline as they arrive (Ctrl-C exits cleanly)")
	return cmd
}

// followAllSessions opens one port-forward per agent in scope, then a
// session-stream goroutine per session in the rendered list, and pipes
// every incoming envelope to stdout under a shared mutex so line
// boundaries don't interleave. Append-style rendering (no redraw) so
// the initial table/cards stay anchored at the top of the buffer and
// new turns scroll in below — like a multi-channel chat window.
//
// Lifecycle:
//   - One port-forward per agent (NOT per session) — N session streams
//     multiplex over each agent's existing tunnel.
//   - Streams started concurrently; each runs until the session
//     emits a stream.overrun OR ctx cancels (Ctrl-C).
//   - On Ctrl-C: ctx cancellation propagates to every StreamSession
//     goroutine and to every Forward via its own ctx-watcher; all
//     close cleanly. Returns nil (Ctrl-C is success).
func followAllSessions(
	ctx context.Context,
	cfg *rest.Config,
	summaries []conversation.SessionSummary,
	results []conversation.FanOutResult,
	tokenFn func(conversation.AgentTarget) string,
) error {
	if len(summaries) == 0 {
		fmt.Fprintln(os.Stderr, "(no sessions to follow)")
		return nil
	}

	// Build session_id → AgentTarget so we know which port-forward
	// each session needs.
	sessionToTarget := make(map[string]conversation.AgentTarget)
	for _, r := range results {
		if r.Err != nil {
			continue
		}
		for _, e := range r.Entries {
			if e.SessionID != "" {
				sessionToTarget[e.SessionID] = r.Target
			}
		}
	}

	// One port-forward per unique agent — N sessions multiplex over it.
	type agentKey struct{ ns, name string }
	forwards := make(map[agentKey]*portforward.Forward)
	defer func() {
		for _, fwd := range forwards {
			fwd.Close()
		}
	}()
	for _, target := range sessionToTarget {
		k := agentKey{target.Namespace, target.Agent}
		if _, ok := forwards[k]; ok {
			continue
		}
		fwd, err := portforward.OpenPort(ctx, cfg, target.Namespace, target.Agent, portforward.BackendHTTPPort)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ww conversation list --follow: skipping %s/%s (port-forward to backend failed: %v)\n",
				target.Namespace, target.Agent, err)
			continue
		}
		forwards[k] = fwd
	}
	if len(forwards) == 0 {
		return fmt.Errorf("could not port-forward to any backend in scope; nothing to follow")
	}

	fmt.Fprintln(os.Stdout, "")
	fmt.Fprintln(os.Stdout, "─── live (Ctrl-C to exit) ───")
	fmt.Fprintln(os.Stdout, "")

	// Mutex around stdout so multi-line envelope renders stay atomic.
	var writeMu sync.Mutex
	emit := func(s conversation.SessionSummary, env conversation.StreamEnvelope) {
		writeMu.Lock()
		defer writeMu.Unlock()
		renderLiveEnvelope(os.Stdout, s, env)
	}

	var wg sync.WaitGroup
	for _, s := range summaries {
		target, ok := sessionToTarget[s.SessionID]
		if !ok {
			continue
		}
		fwd, ok := forwards[agentKey{target.Namespace, target.Agent}]
		if !ok {
			continue // forward failed earlier; skip this session
		}
		wg.Add(1)
		s := s
		token := tokenFn(target)
		go func() {
			defer wg.Done()
			stream, err := conversation.StreamSession(ctx, fwd.BaseURL, token, s.SessionID, func(err error) {
				fmt.Fprintf(os.Stderr, "stream %s/%s: %v\n", target.Agent, shortSessionID(s.SessionID), err)
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "ww conversation list --follow: skipping session %s/%s (stream open failed: %v)\n",
					target.Agent, shortSessionID(s.SessionID), err)
				return
			}
			for env := range stream {
				emit(s, env)
				if env.Type == "stream.overrun" {
					return
				}
			}
		}()
	}
	wg.Wait()
	return nil
}

// renderLiveEnvelope writes one streamed envelope to w with a session-
// origin prefix so the user can tell which session is updating in the
// multi-stream output. Format mirrors `ww conversation show --follow`'s
// per-entry shape with an extra `↻ <short-id> <agent>` line above so the
// ownership is clear at a glance.
func renderLiveEnvelope(
	w *os.File,
	summary conversation.SessionSummary,
	env conversation.StreamEnvelope,
) {
	ts := conversation.FormatTSCompact(env.TS)
	short := shortSessionID(summary.SessionID)
	switch env.Type {
	case "session.message":
		role, _ := env.Payload["role"].(string)
		text, _ := env.Payload["text"].(string)
		model, _ := env.Payload["model"].(string)
		label := role
		if model != "" {
			label = fmt.Sprintf("%s (%s)", role, model)
		}
		fmt.Fprintf(w, "↻ %s · %s · [%s] %s:\n", short, summary.Agent, ts, label)
		for _, ln := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
			fmt.Fprintf(w, "    %s\n", ln)
		}
		fmt.Fprintln(w)
	default:
		fmt.Fprintf(w, "↻ %s · %s · [%s] (%s)\n", short, summary.Agent, ts, env.Type)
	}
}

func filterTargetsByAgent(targets []conversation.AgentTarget, name string) []conversation.AgentTarget {
	out := make([]conversation.AgentTarget, 0, 1)
	for _, t := range targets {
		if t.Agent == name {
			out = append(out, t)
		}
	}
	return out
}

func renderListTable(w *os.File, summaries []conversation.SessionSummary, showNamespace bool) {
	if len(summaries) == 0 {
		fmt.Fprintln(w, "No conversation sessions found.")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if showNamespace {
		fmt.Fprintln(tw, "NAMESPACE\tSESSION ID\tAGENT\tSTARTED\tLAST ACTIVITY\tTURNS\tSOURCE")
	} else {
		fmt.Fprintln(tw, "SESSION ID\tAGENT\tSTARTED\tLAST ACTIVITY\tTURNS\tSOURCE")
	}
	for _, s := range summaries {
		started := conversation.FormatTS(s.Started)
		last := conversation.FormatTS(s.LastActivity)
		if showNamespace {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%d\t%s\n",
				s.Namespace, shortSessionID(s.SessionID), s.Agent, started, last, s.Turns, s.Source)
		} else {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\n",
				shortSessionID(s.SessionID), s.Agent, started, last, s.Turns, s.Source)
		}
	}
	_ = tw.Flush()
}

// shortSessionID returns the first 8 chars of a UUID-shaped session id
// — same convention git uses for short SHAs. Full id is still printed
// in the boxed transcript header (--expand) and in `ww conversation
// show` output, so users can copy-paste the full thing when they need it.
func shortSessionID(id string) string {
	if len(id) < 8 {
		return id
	}
	return id[:8]
}

// renderListExpanded renders each session as an Option-A box-drawn card:
//
//	┌─ <short-id> — <agent> · <turns> turns · <start> → <last> · <source>
//	│
//	│  HH:MM:SS  role (model):
//	│    ...wrapped text...
//	│
//	└─────────────────────────────────────────────────────────────
//
// Per-entry text wraps at ~80 cols with continuation lines indented
// under the role label. Each entry caps at 500 chars + ellipsis unless
// fullText is set.
func renderListExpanded(
	w *os.File,
	summaries []conversation.SessionSummary,
	allEntries map[string][]conversation.Entry, // keyed by SessionID
	fullText bool,
) {
	if len(summaries) == 0 {
		fmt.Fprintln(w, "No conversation sessions found.")
		return
	}
	for i, s := range summaries {
		if i > 0 {
			fmt.Fprintln(w)
		}
		started := conversation.FormatTSCompact(s.Started)
		last := conversation.FormatTSCompact(s.LastActivity)
		header := fmt.Sprintf("┌─ %s — %s · %d turns · %s → %s · %s",
			shortSessionID(s.SessionID), s.Agent, s.Turns, started, last, s.Source)
		fmt.Fprintln(w, header)
		fmt.Fprintln(w, "│")
		entries := allEntries[s.SessionID]
		for _, e := range entries {
			ts := conversation.FormatTSCompact(e.TS)
			label := e.Role
			if e.Model != nil && *e.Model != "" {
				label = fmt.Sprintf("%s (%s)", e.Role, *e.Model)
			}
			fmt.Fprintf(w, "│  %s  %s:\n", ts, label)
			if e.Text != nil {
				text := *e.Text
				const cap = 500
				if !fullText && len(text) > cap {
					text = text[:cap] + fmt.Sprintf(" […+%d chars]", len(*e.Text)-cap)
				}
				for _, ln := range wrapLines(text, 76) {
					fmt.Fprintf(w, "│    %s\n", ln)
				}
			}
			fmt.Fprintln(w, "│")
		}
		fmt.Fprintln(w, "└─────────────────────────────────────────────────────────────────────────────")
	}
}

// wrapLines splits text at width-col boundaries on word boundaries
// (best-effort — falls back to mid-word splits when a single word
// exceeds width). Preserves explicit newlines from the source.
func wrapLines(text string, width int) []string {
	var out []string
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		if line == "" {
			out = append(out, "")
			continue
		}
		for len(line) > width {
			brk := width
			if i := strings.LastIndex(line[:brk], " "); i > 0 {
				brk = i
			}
			out = append(out, line[:brk])
			line = strings.TrimLeft(line[brk:], " ")
		}
		out = append(out, line)
	}
	return out
}

func renderUnreachableFooter(w *os.File, unreachable []conversation.FanOutResult) {
	if len(unreachable) == 0 {
		return
	}
	names := make([]string, 0, len(unreachable))
	for _, r := range unreachable {
		names = append(names, fmt.Sprintf("%s/%s", r.Target.Namespace, r.Target.Agent))
	}
	fmt.Fprintf(w, "\n(%d agent(s) unreachable: %s)\n",
		len(unreachable), strings.Join(names, ", "))
	for _, r := range unreachable {
		fmt.Fprintf(w, "  %s/%s: %v\n", r.Target.Namespace, r.Target.Agent, r.Err)
	}
}

// =====================================================================
// show
// =====================================================================

func newConversationShowCmd(f *conversationFlags) *cobra.Command {
	var (
		format string
		token  string
		follow bool
	)
	cmd := &cobra.Command{
		Use:   "show <session-id>",
		Short: "Show the full transcript of one conversation session",
		Long: "Fetches every entry for the given session id from whichever agent owns\n" +
			"it and prints the conversation. Session ids are globally unique UUIDs;\n" +
			"the CLI walks each agent in the namespace (or cluster, with -A) to find\n" +
			"the owning agent so you don't have to specify it.\n\n" +
			"With --follow / -f, prints the existing transcript and then live-tails\n" +
			"new entries as they arrive (Server-Sent Events from the backend's\n" +
			"/api/sessions/<id>/stream endpoint, mirroring `kubectl logs -f` shape).\n\n" +
			"Format options (one-shot only; --follow is text-only):\n" +
			"  text   — human-readable (default)\n" +
			"  json   — single JSON document with the entries array\n" +
			"  jsonl  — line-delimited JSON, one entry per line (script-friendly)",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			sessionID := args[0]
			_, resolver, err := f.resolveTarget(ctx)
			if err != nil {
				return err
			}
			cfg, err := resolver.REST()
			if err != nil {
				return err
			}
			ns := f.namespace
			if !f.allNamespaces {
				ns = logAndResolveNamespace(f.namespace, resolver.Target().Namespace)
			}

			targets, err := conversation.DiscoverAgents(ctx, cfg, ns, f.allNamespaces)
			if err != nil {
				return err
			}
			if len(targets) == 0 {
				return fmt.Errorf("no WitwaveAgents in scope to search for session %s", sessionID)
			}

			tokenFn := makeTokenLookup(ctx, cfg, token)
			// Wide list (no since/limit), then filter client-side. The
			// harness doesn't expose a per-session endpoint; this is
			// the same path the dashboard takes.
			results := conversation.FanOutList(ctx, cfg, targets, conversation.ListOptions{}, tokenFn)
			for _, r := range results {
				if r.Err != nil {
					continue
				}
				match := conversation.FilterSession(r.Entries, sessionID)
				if len(match) == 0 {
					continue
				}
				if err := renderSession(os.Stdout, match, format, r.Target, sessionID); err != nil {
					return err
				}
				if follow {
					if format != "text" && format != "" {
						return fmt.Errorf("--follow requires --format=text (got %q)", format)
					}
					return followSession(ctx, cfg, r.Target, sessionID, tokenFn(r.Target))
				}
				return nil
			}
			return fmt.Errorf("session %s not found in any agent's conversation log within the current scope", sessionID)
		},
	}
	cmd.Flags().StringVar(&format, "format", "text",
		"Output format: text | json | jsonl")
	cmd.Flags().StringVar(&token, "token", "",
		"Override the per-agent bearer token (default: read from each agent's credentials Secret)")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false,
		"After printing the transcript, live-tail new entries via SSE (mirrors `kubectl logs -f`)")
	return cmd
}

// followSession opens an SSE stream against the OWNING agent's backend
// container (port 8001 — different from harness's 8000 used by list/
// show one-shots) and prints each envelope as it arrives. Returns when
// the stream closes (server overrun event, connection drop) or ctx
// cancels (Ctrl-C). Treats Ctrl-C as success — the user said done.
func followSession(
	ctx context.Context,
	cfg *rest.Config,
	target conversation.AgentTarget,
	sessionID, token string,
) error {
	fwd, err := portforward.OpenPort(ctx, cfg, target.Namespace, target.Agent, portforward.BackendHTTPPort)
	if err != nil {
		return fmt.Errorf("port-forward to backend for SSE: %w", err)
	}
	defer fwd.Close()

	stream, err := conversation.StreamSession(ctx, fwd.BaseURL, token, sessionID, func(err error) {
		fmt.Fprintf(os.Stderr, "ww conversation watch: %v\n", err)
	})
	if err != nil {
		return err
	}

	fmt.Fprintln(os.Stdout, "─── live ───")
	for env := range stream {
		renderEnvelope(os.Stdout, env)
		if env.Type == "stream.overrun" {
			fmt.Fprintln(os.Stderr, "ww conversation watch: server reported stream.overrun — disconnecting")
			break
		}
	}
	if ctx.Err() != nil {
		// Ctrl-C is the normal exit; don't surface it as failure.
		return nil
	}
	return nil
}

// renderEnvelope prints one streamed SSE envelope in the same compact
// shape the one-shot show output uses (timestamp + role + indented
// text), so a `--follow` run reads continuously with the prior history.
//
// Different envelope.Type values represent different things in the
// session-stream protocol; the v1 implementation handles
// `session.message` (the conversation entry shape) and falls back to a
// generic dump for anything else so the user can see what's happening.
func renderEnvelope(w *os.File, env conversation.StreamEnvelope) {
	ts := conversation.FormatTSCompact(env.TS)
	switch env.Type {
	case "session.message":
		role, _ := env.Payload["role"].(string)
		text, _ := env.Payload["text"].(string)
		model, _ := env.Payload["model"].(string)
		label := role
		if model != "" {
			label = fmt.Sprintf("%s (%s)", role, model)
		}
		fmt.Fprintf(w, "[%s] %s:\n", ts, label)
		for _, ln := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
			fmt.Fprintf(w, "  %s\n", ln)
		}
		fmt.Fprintln(w)
	default:
		// Surface non-message envelopes as a one-line annotation —
		// useful for debugging stream contents but not noisy.
		fmt.Fprintf(w, "[%s] (%s)\n", ts, env.Type)
	}
}

func renderSession(w *os.File, entries []conversation.Entry, format string, target conversation.AgentTarget, sessionID string) error {
	switch format {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"session_id": sessionID,
			"agent":      target.Agent,
			"namespace":  target.Namespace,
			"entries":    entries,
		})
	case "jsonl":
		enc := json.NewEncoder(w)
		for i := range entries {
			if err := enc.Encode(entries[i]); err != nil {
				return err
			}
		}
		return nil
	case "text", "":
		fmt.Fprintf(w, "Session %s — %s/%s — %d entries\n", sessionID, target.Namespace, target.Agent, len(entries))
		if len(entries) > 0 {
			fmt.Fprintf(w, "Range: %s → %s\n\n",
				conversation.FormatTS(entries[0].TS),
				conversation.FormatTS(entries[len(entries)-1].TS))
		}
		for i := range entries {
			e := &entries[i]
			label := e.Role
			if e.Model != nil && *e.Model != "" {
				label = fmt.Sprintf("%s (%s)", e.Role, *e.Model)
			}
			fmt.Fprintf(w, "[%s] %s:\n", conversation.FormatTS(e.TS), label)
			if e.Text != nil {
				lines := strings.Split(strings.TrimRight(*e.Text, "\n"), "\n")
				for _, ln := range lines {
					fmt.Fprintf(w, "  %s\n", ln)
				}
			}
			fmt.Fprintln(w)
		}
		return nil
	default:
		return fmt.Errorf("unknown format %q (expected text | json | jsonl)", format)
	}
}

// =====================================================================
// token lookup
// =====================================================================

// makeTokenLookup returns a per-target token resolver. When the user
// passes --token, every target uses that override. Otherwise we read
// the CONVERSATIONS_AUTH_TOKEN key from each agent's <agent>-claude
// Secret. Unreadable secrets fall back to no token, which the harness
// will reject with 503 (auth not configured) — the error message
// surfaces the missing-secret path so the user knows what to fix.
func makeTokenLookup(ctx context.Context, cfg *rest.Config, override string) func(conversation.AgentTarget) string {
	if override != "" {
		return func(_ conversation.AgentTarget) string { return override }
	}
	k8sClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		// If we can't even build the client, every per-target lookup
		// falls back to empty. Caller's HTTP request will get a 503
		// with a clear "auth not configured" message.
		return func(_ conversation.AgentTarget) string { return "" }
	}
	return func(t conversation.AgentTarget) string {
		secretName := t.Agent + "-claude"
		s, err := k8sClient.CoreV1().Secrets(t.Namespace).Get(ctx, secretName, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) || apierrors.IsForbidden(err) {
				return ""
			}
			return ""
		}
		if v, ok := s.Data["CONVERSATIONS_AUTH_TOKEN"]; ok {
			return string(v)
		}
		return ""
	}
}

// _ = portforward.HarnessHTTPPort — keep portforward import live for
// godoc readers; the helper is used transitively via fanout.FanOutList.
var _ = portforward.HarnessHTTPPort
