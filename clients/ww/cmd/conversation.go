package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
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
		agent string
		since time.Duration
		limit int
		token string
		quiet bool
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
			renderListTable(os.Stdout, summaries, f.allNamespaces)
			renderUnreachableFooter(os.Stderr, unreachable)
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
	return cmd
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
		if showNamespace {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%d\t%s\n",
				s.Namespace, s.SessionID, s.Agent, s.Started, s.LastActivity, s.Turns, s.Source)
		} else {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\n",
				s.SessionID, s.Agent, s.Started, s.LastActivity, s.Turns, s.Source)
		}
	}
	_ = tw.Flush()
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
	)
	cmd := &cobra.Command{
		Use:   "show <session-id>",
		Short: "Show the full transcript of one conversation session",
		Long: "Fetches every entry for the given session id from whichever agent owns\n" +
			"it and prints the conversation. Session ids are globally unique UUIDs;\n" +
			"the CLI walks each agent in the namespace (or cluster, with -A) to find\n" +
			"the owning agent so you don't have to specify it.\n\n" +
			"Format options:\n" +
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
				return renderSession(os.Stdout, match, format, r.Target, sessionID)
			}
			return fmt.Errorf("session %s not found in any agent's conversation log within the current scope", sessionID)
		},
	}
	cmd.Flags().StringVar(&format, "format", "text",
		"Output format: text | json | jsonl")
	cmd.Flags().StringVar(&token, "token", "",
		"Override the per-agent bearer token (default: read from each agent's credentials Secret)")
	return cmd
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
			fmt.Fprintf(w, "Range: %s → %s\n\n", entries[0].TS, entries[len(entries)-1].TS)
		}
		for i := range entries {
			e := &entries[i]
			label := e.Role
			if e.Model != nil && *e.Model != "" {
				label = fmt.Sprintf("%s (%s)", e.Role, *e.Model)
			}
			fmt.Fprintf(w, "[%s] %s:\n", e.TS, label)
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
