package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/skthomasjr/witwave/clients/ww/internal/agent"
	"github.com/skthomasjr/witwave/clients/ww/internal/k8s"
)

// agentFlags carries the namespace flag shared across every `ww agent *`
// subcommand. Per DESIGN.md KC-6 (namespace-per-subtree) + NS-1 (default
// to context's namespace) + NS-2 (always print resolved ns).
//
// Cluster-identity flags (--kubeconfig, --context) live on the root
// command per DESIGN.md KC-5 and reach us via K8sFromCtx.
type agentFlags struct {
	namespace     string
	allNamespaces bool

	// Mutating-command flags. Not every subcommand wires both — see
	// bindMutatingAgentFlags below.
	assumeYes bool
	dryRun    bool
}

func bindAgentFlags(cmd *cobra.Command, f *agentFlags) {
	cmd.PersistentFlags().StringVarP(&f.namespace, "namespace", "n", "",
		"Namespace for the agent (defaults to the kubeconfig context's namespace, then \"default\")")
}

func bindAgentMutatingFlags(cmd *cobra.Command, f *agentFlags) {
	cmd.Flags().BoolVarP(&f.assumeYes, "yes", "y", false,
		"Skip the preflight confirmation prompt (or set WW_ASSUME_YES=true)")
	cmd.Flags().BoolVar(&f.dryRun, "dry-run", false,
		"Print the plan and exit without applying any changes")
}

// resolveTarget runs the kubeconfig loader and returns the populated
// Target + REST config. Cluster-identity flags come from the root
// command via K8sFromCtx (DESIGN.md KC-5); namespace is the agent
// subtree's own persistent flag (KC-6), defaulted via
// agent.ResolveNamespace when the caller left -n empty.
func (f *agentFlags) resolveTarget(ctx context.Context) (*k8s.Target, *k8s.Resolver, error) {
	kc := K8sFromCtx(ctx)
	r, err := k8s.NewResolver(k8s.Options{
		KubeconfigPath: kc.Kubeconfig,
		Context:        kc.Context,
		// Pass the raw flag value; the resolver hydrates the context
		// namespace for us when this is empty. We then re-resolve for
		// display via agent.ResolveNamespace so the user sees which
		// namespace we actually picked.
		Namespace: f.namespace,
	})
	if err != nil {
		return nil, nil, err
	}
	return r.Target(), r, nil
}

// newAgentCmd is the parent command for `ww agent *`.
func newAgentCmd() *cobra.Command {
	f := &agentFlags{}
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Manage WitwaveAgent custom resources on a Kubernetes cluster",
		Long: "Create, list, inspect, and delete WitwaveAgent CRs. The witwave-operator\n" +
			"reconciles each CR into a running agent pod with harness + backend sidecars.\n\n" +
			"Prerequisite: the operator must already be installed on the target cluster\n" +
			"(see `ww operator install`). Every `ww agent *` command honours the ambient\n" +
			"kubeconfig and current-context (override via the root --kubeconfig / --context\n" +
			"flags). Use --namespace / -n to target a specific namespace; omit to use the\n" +
			"kubeconfig context's namespace (falling back to \"default\").",
	}
	bindAgentFlags(cmd, f)

	cmd.AddCommand(newAgentCreateCmd(f))
	cmd.AddCommand(newAgentListCmd(f))
	cmd.AddCommand(newAgentStatusCmd(f))
	cmd.AddCommand(newAgentDeleteCmd(f))
	cmd.AddCommand(newAgentSendCmd(f))
	cmd.AddCommand(newAgentLogsCmd(f))
	cmd.AddCommand(newAgentEventsCmd(f))
	cmd.AddCommand(newAgentScaffoldCmd())
	return cmd
}

// ---------------------------------------------------------------------------
// scaffold
// ---------------------------------------------------------------------------
//
// scaffold is deliberately *not* a cluster-touching verb — it materialises
// a ww-conformant agent directory structure on a remote git repo so a
// future `ww agent git add` can wire a deployed agent to that directory.
// It therefore doesn't share the agentFlags parent (which carries -n +
// cluster preflight); it owns its own flag set.

func newAgentScaffoldCmd() *cobra.Command {
	var (
		repo          string
		group         string
		backend       string
		branch        string
		commitMessage string
		cloneTo       string
		noPush        bool
		dryRun        bool
		force         bool
		noHeartbeat   bool
	)
	cmd := &cobra.Command{
		Use:   "scaffold <name>",
		Short: "Create a ww-conformant agent directory on a remote git repo",
		Long: "Scaffolds the directory structure for a new agent on a remote git repo so it\n" +
			"can later be wired up via gitSync. Default layout:\n\n" +
			"  <repo>/.agents/<name>/\n" +
			"    ├── README.md\n" +
			"    ├── .witwave/backend.yaml\n" +
			"    └── .<backend>/\n" +
			"        ├── agent-card.md\n" +
			"        └── <CLAUDE|AGENTS|GEMINI>.md   (LLM backends only)\n\n" +
			"Pass --group to nest under `.agents/<group>/<name>/`. The scaffolder uses\n" +
			"your machine's git credentials (credential helper, SSH agent, or\n" +
			"GITHUB_TOKEN env) so whatever `git push` against this remote already\n" +
			"works — `ww agent scaffold` works too. Empty remote repos are\n" +
			"supported: the scaffolder initialises the first commit and pushes\n" +
			"with --set-upstream semantics.\n\n" +
			"Dormant subsystems (heartbeat, jobs, tasks, triggers, continuations,\n" +
			"webhooks) are NOT pre-created — per DESIGN.md SUB-1..4, the absence of\n" +
			"their content IS how you express \"this agent doesn't use that feature.\"\n" +
			"Future `ww agent add-job` / `add-task` verbs will materialise them on\n" +
			"demand.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentScaffold(cmd.Context(), args[0], agent.ScaffoldOptions{
				Repo:          repo,
				Group:         group,
				Backend:       backend,
				Branch:        branch,
				CommitMessage: commitMessage,
				CloneTo:       cloneTo,
				NoPush:        noPush,
				DryRun:        dryRun,
				Force:         force,
				NoHeartbeat:   noHeartbeat,
				CLIVersion:    Version,
				Out:           os.Stdout,
			})
		},
	}
	cmd.Flags().StringVar(&repo, "repo", "",
		"Remote repo (owner/repo, github.com/owner/repo, full URL, or git@host:owner/repo) — required")
	_ = cmd.MarkFlagRequired("repo")
	cmd.Flags().StringVar(&group, "group", "",
		"Optional group segment — `.agents/<group>/<name>/` when set; flat `.agents/<name>/` otherwise")
	cmd.Flags().StringVar(&backend, "backend", agent.DefaultBackend,
		fmt.Sprintf("Backend type (one of: %s)", strings.Join(agent.KnownBackends(), ", ")))
	cmd.Flags().StringVar(&branch, "branch", "",
		"Git branch to push to. Unspecified: detects the remote's default "+
			"(via HEAD symref) and falls back to \"main\" on empty repos")
	cmd.Flags().StringVar(&commitMessage, "commit-message", "",
		"Commit message (default: \"Scaffold agent <name>\")")
	cmd.Flags().StringVar(&cloneTo, "clone-to", "",
		"Persist the clone at this path instead of using a temp dir; directory must be empty")
	cmd.Flags().BoolVar(&noPush, "no-push", false,
		"Stop after the commit is created; don't push to origin")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"Print the plan + file list and exit without touching the remote or disk")
	cmd.Flags().BoolVar(&force, "force", false,
		"Overwrite scaffold files that have drifted from the template. "+
			"Never touches files outside the skeleton list (user-added jobs/tasks/etc. are safe either way)")
	cmd.Flags().BoolVar(&noHeartbeat, "no-heartbeat", false,
		"Skip writing .witwave/HEARTBEAT.md (scaffold defaults to an hourly heartbeat)")
	return cmd
}

func runAgentScaffold(ctx context.Context, name string, opts agent.ScaffoldOptions) error {
	opts.Name = name
	return agent.Scaffold(ctx, opts)
}

// ---------------------------------------------------------------------------
// create
// ---------------------------------------------------------------------------

func newAgentCreateCmd(f *agentFlags) *cobra.Command {
	var (
		backend string
		noWait  bool
		timeout time.Duration
	)
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a WitwaveAgent CR (defaults to the echo backend)",
		Long: "Creates a WitwaveAgent with a single backend sidecar. With no flags, deploys\n" +
			"the echo backend — a zero-dependency stub that requires no API keys — so you\n" +
			"can exercise an agent end-to-end with \"access to a Kubernetes cluster and the\n" +
			"CLI\" as the only prerequisites.\n\n" +
			"After the CR is applied, waits up to --timeout for the operator to report the\n" +
			"agent as Ready. Pass --no-wait to skip the readiness wait.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentCreate(cmd.Context(), f, args[0], backend, !noWait, timeout)
		},
	}
	bindAgentMutatingFlags(cmd, f)
	cmd.Flags().StringVar(&backend, "backend", agent.DefaultBackend,
		fmt.Sprintf("Backend type to deploy (one of: %s)", strings.Join(agent.KnownBackends(), ", ")))
	cmd.Flags().BoolVar(&noWait, "no-wait", false,
		"Return as soon as the CR is accepted; skip the readiness wait")
	cmd.Flags().DurationVar(&timeout, "timeout", 2*time.Minute,
		"Maximum time to wait for the agent to report Ready (ignored with --no-wait)")
	return cmd
}

func runAgentCreate(ctx context.Context, f *agentFlags, name, backend string, wait bool, timeout time.Duration) error {
	target, resolver, err := f.resolveTarget(ctx)
	if err != nil {
		return err
	}
	cfg, err := resolver.REST()
	if err != nil {
		return err
	}
	ns := agent.ResolveNamespace(f.namespace, target.Namespace)
	// NS-2: always echo the resolved namespace. If the user didn't pass
	// -n, this is their signal for where the CR landed.
	if f.namespace == "" {
		fmt.Fprintf(os.Stdout, "Using namespace: %s (from kubeconfig context)\n", ns)
	}

	assumeYes := f.assumeYes || os.Getenv("WW_ASSUME_YES") == "true"
	return agent.Create(ctx, target, cfg, resolver.ConfigFlags(), agent.CreateOptions{
		Name:       name,
		Namespace:  ns,
		Backend:    backend,
		CLIVersion: Version,
		CreatedBy:  fmt.Sprintf("ww agent create %s", name),
		AssumeYes:  assumeYes,
		DryRun:     f.dryRun,
		Wait:       wait,
		Timeout:    timeout,
		Out:        os.Stdout,
		In:         os.Stdin,
	})
}

// ---------------------------------------------------------------------------
// list
// ---------------------------------------------------------------------------

func newAgentListCmd(f *agentFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List WitwaveAgent CRs in the target namespace (or all with -A)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentList(cmd.Context(), f)
		},
	}
	cmd.Flags().BoolVarP(&f.allNamespaces, "all-namespaces", "A", false,
		"List agents across every namespace the caller has access to")
	return cmd
}

func runAgentList(ctx context.Context, f *agentFlags) error {
	target, resolver, err := f.resolveTarget(ctx)
	if err != nil {
		return err
	}
	cfg, err := resolver.REST()
	if err != nil {
		return err
	}
	ns := agent.ResolveNamespace(f.namespace, target.Namespace)
	if !f.allNamespaces && f.namespace == "" {
		fmt.Fprintf(os.Stdout, "Using namespace: %s (from kubeconfig context)\n", ns)
	}
	return agent.List(ctx, cfg, agent.ListOptions{
		Namespace:     ns,
		AllNamespaces: f.allNamespaces,
		Out:           os.Stdout,
	})
}

// ---------------------------------------------------------------------------
// status
// ---------------------------------------------------------------------------

func newAgentStatusCmd(f *agentFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status <name>",
		Short: "Show phase, backends, and reconcile history for a WitwaveAgent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentStatus(cmd.Context(), f, args[0])
		},
	}
	return cmd
}

func runAgentStatus(ctx context.Context, f *agentFlags, name string) error {
	target, resolver, err := f.resolveTarget(ctx)
	if err != nil {
		return err
	}
	cfg, err := resolver.REST()
	if err != nil {
		return err
	}
	ns := agent.ResolveNamespace(f.namespace, target.Namespace)
	if f.namespace == "" {
		fmt.Fprintf(os.Stdout, "Using namespace: %s (from kubeconfig context)\n\n", ns)
	}
	return agent.Status(ctx, cfg, agent.StatusOptions{
		Name:      name,
		Namespace: ns,
		Out:       os.Stdout,
	})
}

// ---------------------------------------------------------------------------
// delete
// ---------------------------------------------------------------------------

func newAgentDeleteCmd(f *agentFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a WitwaveAgent CR (operator cascades pod cleanup)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentDelete(cmd.Context(), f, args[0])
		},
	}
	bindAgentMutatingFlags(cmd, f)
	return cmd
}

func runAgentDelete(ctx context.Context, f *agentFlags, name string) error {
	target, resolver, err := f.resolveTarget(ctx)
	if err != nil {
		return err
	}
	cfg, err := resolver.REST()
	if err != nil {
		return err
	}
	ns := agent.ResolveNamespace(f.namespace, target.Namespace)
	if f.namespace == "" {
		fmt.Fprintf(os.Stdout, "Using namespace: %s (from kubeconfig context)\n", ns)
	}
	assumeYes := f.assumeYes || os.Getenv("WW_ASSUME_YES") == "true"
	return agent.Delete(ctx, target, cfg, agent.DeleteOptions{
		Name:      name,
		Namespace: ns,
		AssumeYes: assumeYes,
		DryRun:    f.dryRun,
		Out:       os.Stdout,
		In:        os.Stdin,
	})
}

// ---------------------------------------------------------------------------
// send
// ---------------------------------------------------------------------------

func newAgentSendCmd(f *agentFlags) *cobra.Command {
	var (
		messageID string
		timeout   time.Duration
		rawJSON   bool
	)
	cmd := &cobra.Command{
		Use:   "send <name> <prompt>",
		Short: "Send an A2A prompt to an agent via the Kubernetes apiserver Service proxy",
		Long: "Makes a single A2A message/send round-trip against the agent's harness\n" +
			"Service. Uses the apiserver's built-in Service proxy so no local port-forward\n" +
			"or external LoadBalancer is required — any ClusterIP Service works.\n\n" +
			"Not suited for streaming or very large payloads (apiserver proxy has size\n" +
			"caps); ww agent logs -f is the right tool for live observation. Use --raw\n" +
			"to print the full JSON-RPC envelope for debugging.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentSend(cmd.Context(), f, args[0], args[1], messageID, timeout, rawJSON)
		},
	}
	cmd.Flags().StringVar(&messageID, "message-id", "",
		"Explicit A2A messageId (default: ww-send-<timestamp>)")
	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Second,
		"Round-trip timeout through the apiserver Service proxy")
	cmd.Flags().BoolVar(&rawJSON, "raw", false,
		"Print the raw JSON-RPC response envelope instead of extracting the agent text")
	return cmd
}

func runAgentSend(ctx context.Context, f *agentFlags, name, prompt, messageID string, timeout time.Duration, rawJSON bool) error {
	target, resolver, err := f.resolveTarget(ctx)
	if err != nil {
		return err
	}
	cfg, err := resolver.REST()
	if err != nil {
		return err
	}
	ns := agent.ResolveNamespace(f.namespace, target.Namespace)
	if f.namespace == "" {
		fmt.Fprintf(os.Stdout, "Using namespace: %s (from kubeconfig context)\n", ns)
	}
	return agent.Send(ctx, cfg, agent.SendOptions{
		Agent:     name,
		Namespace: ns,
		Prompt:    prompt,
		MessageID: messageID,
		Timeout:   timeout,
		RawJSON:   rawJSON,
		Out:       os.Stdout,
	})
}

// ---------------------------------------------------------------------------
// logs
// ---------------------------------------------------------------------------

func newAgentLogsCmd(f *agentFlags) *cobra.Command {
	var (
		container string
		tail      int64
		since     time.Duration
		noFollow  bool
		pod       string
	)
	cmd := &cobra.Command{
		Use:   "logs <name>",
		Short: "Tail logs from a WitwaveAgent's pod(s)",
		Long: "Streams logs from every pod matching the agent's label selector. Defaults\n" +
			"to the harness container; pass -c <backend-name> to tail a specific backend\n" +
			"(echo, claude, codex, gemini) or any other sidecar.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentLogs(cmd.Context(), f, args[0], agent.LogsOptions{
				Container: container,
				Follow:    !noFollow,
				TailLines: tail,
				Since:     since,
				Pod:       pod,
				Out:       os.Stdout,
			})
		},
	}
	cmd.Flags().StringVarP(&container, "container", "c", "",
		"Container name within the agent pod (default: harness)")
	cmd.Flags().Int64Var(&tail, "tail", 100,
		"Number of recent log lines to emit before following (0 = full history)")
	cmd.Flags().DurationVar(&since, "since", 0,
		"Lookback duration, e.g. 1h or 30m (empty = no limit)")
	cmd.Flags().BoolVar(&noFollow, "no-follow", false,
		"Print current log contents and exit without streaming")
	cmd.Flags().StringVar(&pod, "pod", "",
		"Target a specific pod by name instead of all pods matching the agent label")
	return cmd
}

func runAgentLogs(ctx context.Context, f *agentFlags, name string, opts agent.LogsOptions) error {
	target, resolver, err := f.resolveTarget(ctx)
	if err != nil {
		return err
	}
	cfg, err := resolver.REST()
	if err != nil {
		return err
	}
	ns := agent.ResolveNamespace(f.namespace, target.Namespace)
	if f.namespace == "" {
		fmt.Fprintf(os.Stdout, "Using namespace: %s (from kubeconfig context)\n", ns)
	}
	opts.Agent = name
	opts.Namespace = ns
	return agent.Logs(ctx, cfg, opts)
}

// ---------------------------------------------------------------------------
// events
// ---------------------------------------------------------------------------

func newAgentEventsCmd(f *agentFlags) *cobra.Command {
	var (
		warnings bool
		since    time.Duration
	)
	cmd := &cobra.Command{
		Use:   "events <name>",
		Short: "Show Kubernetes events for a WitwaveAgent and its owned pods",
		Long: "One-shot event snapshot scoped to a single agent: CR-level events (reconcile\n" +
			"actions, validation failures) plus pod-level events (scheduling, pulls, crash\n" +
			"loops). For live streaming, `ww agent logs -f` is usually the better signal.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentEvents(cmd.Context(), f, args[0], agent.EventsOptions{
				WarningsOnly: warnings,
				Since:        since,
				Out:          os.Stdout,
			})
		},
	}
	cmd.Flags().BoolVar(&warnings, "warnings", false,
		"Only show events of type Warning")
	cmd.Flags().DurationVar(&since, "since", time.Hour,
		"Lookback window for the initial listing, e.g. 10m or 6h")
	return cmd
}

func runAgentEvents(ctx context.Context, f *agentFlags, name string, opts agent.EventsOptions) error {
	target, resolver, err := f.resolveTarget(ctx)
	if err != nil {
		return err
	}
	cfg, err := resolver.REST()
	if err != nil {
		return err
	}
	ns := agent.ResolveNamespace(f.namespace, target.Namespace)
	if f.namespace == "" {
		fmt.Fprintf(os.Stdout, "Using namespace: %s (from kubeconfig context)\n", ns)
	}
	opts.Agent = name
	opts.Namespace = ns
	return agent.Events(ctx, cfg, opts)
}
