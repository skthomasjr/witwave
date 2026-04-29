package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/witwave-ai/witwave/clients/ww/internal/agent"
	"github.com/witwave-ai/witwave/clients/ww/internal/k8s"
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
		fmt.Sprintf("Namespace for the agent (defaults to the kubeconfig context's namespace, then %q)", agent.DefaultAgentNamespace))
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

// logAndResolveNamespace resolves the namespace from flag / context /
// ww default, prints a one-line notice to stdout when the flag was
// omitted (so the user sees where the CR actually landed), and returns
// the resolved value. The note distinguishes the source so
// "(from kubeconfig context)" never misrepresents a ww-default fallback.
// Keeps DESIGN.md NS-2 (always echo the resolved namespace) consistent
// across every `ww agent *` verb.
func logAndResolveNamespace(flagValue, contextNS string) string {
	ns, source := agent.ResolveNamespaceWithSource(flagValue, contextNS)
	if source == agent.NamespaceFromFlag {
		return ns
	}
	var why string
	switch source {
	case agent.NamespaceFromContext:
		why = "from kubeconfig context"
	case agent.NamespaceFromDefault:
		why = "ww default"
	}
	fmt.Fprintf(os.Stdout, "Using namespace: %s (%s)\n", ns, why)
	return ns
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
			"kubeconfig context's namespace (falling back to \"" + agent.DefaultAgentNamespace + "\").\n\n" +
			"Use `ww agent create --create-namespace` to provision the namespace on first use.",
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
	cmd.AddCommand(newAgentGitCmd(f))
	cmd.AddCommand(newAgentBackendCmd(f))
	cmd.AddCommand(newAgentTeamCmd(f))
	return cmd
}

// ---------------------------------------------------------------------------
// team (team membership via the witwave.ai/team label)
// ---------------------------------------------------------------------------

func newAgentTeamCmd(f *agentFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "team",
		Short: "Join, leave, list, or show agent team membership",
		Long: "Manages the `witwave.ai/team` label on WitwaveAgent CRs. The\n" +
			"operator reconciles one `witwave-manifest-<team>` ConfigMap per\n" +
			"team and mounts it into every member's pod at\n" +
			"`/home/agent/manifest.json`, so harnesses discover their teammates'\n" +
			"URLs at runtime. Agents without the team label share one\n" +
			"namespace-wide manifest.\n\n" +
			"Team membership is a pure label patch — no CRD schema change, no\n" +
			"pod restart. Members see a new peer within one operator reconcile\n" +
			"cycle (seconds).",
	}
	cmd.AddCommand(newAgentTeamJoinCmd(f))
	cmd.AddCommand(newAgentTeamLeaveCmd(f))
	cmd.AddCommand(newAgentTeamListCmd(f))
	cmd.AddCommand(newAgentTeamShowCmd(f))
	return cmd
}

func newAgentTeamJoinCmd(f *agentFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "join <agent> <team>",
		Short: "Add an agent to a team (sets witwave.ai/team=<team>)",
		Long: "Sets the `witwave.ai/team` label on the named WitwaveAgent. The\n" +
			"operator re-reconciles the per-team manifest ConfigMap on the next\n" +
			"cycle; every member (including the newcomer) picks up the updated\n" +
			"peer list within seconds.\n\n" +
			"Idempotent when the agent is already in the target team.\n" +
			"Allowed when the agent is already in a *different* team — the\n" +
			"banner shows the transition explicitly (was → now) so the user\n" +
			"can see they're moving, not joining.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentTeamJoin(cmd.Context(), f, args[0], args[1])
		},
	}
	bindAgentMutatingFlags(cmd, f)
	return cmd
}

func runAgentTeamJoin(ctx context.Context, f *agentFlags, name, team string) error {
	target, resolver, err := f.resolveTarget(ctx)
	if err != nil {
		return err
	}
	cfg, err := resolver.REST()
	if err != nil {
		return err
	}
	_ = target
	ns := logAndResolveNamespace(f.namespace, target.Namespace)
	assumeYes := f.assumeYes || os.Getenv("WW_ASSUME_YES") == "true"
	return agent.TeamJoin(ctx, cfg, agent.TeamJoinOptions{
		Agent:     name,
		Namespace: ns,
		Team:      team,
		AssumeYes: assumeYes,
		DryRun:    f.dryRun,
		Out:       os.Stdout,
		In:        os.Stdin,
	})
}

func newAgentTeamLeaveCmd(f *agentFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "leave <agent>",
		Short: "Remove an agent from its team (drops witwave.ai/team label)",
		Long: "Removes the `witwave.ai/team` label from the agent. The operator\n" +
			"reconciles the agent back into the namespace-wide manifest (the\n" +
			"bucket every label-less agent shares). No-ops cleanly when the\n" +
			"agent wasn't in a team to begin with.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentTeamLeave(cmd.Context(), f, args[0])
		},
	}
	bindAgentMutatingFlags(cmd, f)
	return cmd
}

func runAgentTeamLeave(ctx context.Context, f *agentFlags, name string) error {
	target, resolver, err := f.resolveTarget(ctx)
	if err != nil {
		return err
	}
	cfg, err := resolver.REST()
	if err != nil {
		return err
	}
	ns := logAndResolveNamespace(f.namespace, target.Namespace)
	assumeYes := f.assumeYes || os.Getenv("WW_ASSUME_YES") == "true"
	return agent.TeamLeave(ctx, cfg, agent.TeamLeaveOptions{
		Agent:     name,
		Namespace: ns,
		AssumeYes: assumeYes,
		DryRun:    f.dryRun,
		Out:       os.Stdout,
		In:        os.Stdin,
	})
}

func newAgentTeamListCmd(f *agentFlags) *cobra.Command {
	var team string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List teams in a namespace (all teams by default, one team with --team)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentTeamList(cmd.Context(), f, team)
		},
	}
	cmd.Flags().StringVar(&team, "team", "",
		"Only list members of this team (default: list every team in the namespace)")
	return cmd
}

func runAgentTeamList(ctx context.Context, f *agentFlags, team string) error {
	target, resolver, err := f.resolveTarget(ctx)
	if err != nil {
		return err
	}
	cfg, err := resolver.REST()
	if err != nil {
		return err
	}
	ns := logAndResolveNamespace(f.namespace, target.Namespace)
	return agent.TeamList(ctx, cfg, agent.TeamListOptions{
		Namespace: ns,
		Team:      team,
		Out:       os.Stdout,
	})
}

func newAgentTeamShowCmd(f *agentFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <agent>",
		Short: "Show which team an agent is in + its teammates",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentTeamShow(cmd.Context(), f, args[0])
		},
	}
	return cmd
}

func runAgentTeamShow(ctx context.Context, f *agentFlags, name string) error {
	target, resolver, err := f.resolveTarget(ctx)
	if err != nil {
		return err
	}
	cfg, err := resolver.REST()
	if err != nil {
		return err
	}
	ns := logAndResolveNamespace(f.namespace, target.Namespace)
	return agent.TeamShow(ctx, cfg, agent.TeamShowOptions{
		Agent:     name,
		Namespace: ns,
		Out:       os.Stdout,
	})
}

// ---------------------------------------------------------------------------
// backend (attach / detach individual backends on an existing agent)
// ---------------------------------------------------------------------------

func newAgentBackendCmd(f *agentFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backend",
		Short: "Add, remove, or rename backends on an existing WitwaveAgent",
		Long: "Manipulates the `spec.backends[]` array on a running agent's CR.\n" +
			"Covers the full lifecycle:\n\n" +
			"  ww agent backend add    <agent> <name>[:<type>]   append a backend\n" +
			"  ww agent backend remove <agent> <name>            drop a backend\n" +
			"  ww agent backend rename <agent> <old> <new>       rename a backend\n\n" +
			"Each verb updates the CR, regenerates the inline backend.yaml when\n" +
			"ww owns it, and — for agents with a single gitSync wired — optionally\n" +
			"mirrors the change into the repo's `.agents/<…>/.<name>/` folder.",
	}
	cmd.AddCommand(newAgentBackendAddCmd(f))
	cmd.AddCommand(newAgentBackendRemoveCmd(f))
	cmd.AddCommand(newAgentBackendRenameCmd(f))
	return cmd
}

func newAgentBackendAddCmd(f *agentFlags) *cobra.Command {
	var (
		authProfile   string
		authFromEnv   string
		authSecret    string
		authSet       []string
		noRepoFolder  bool
		commitMessage string
	)
	cmd := &cobra.Command{
		Use:   "add <agent> <name>[:<type>]",
		Short: "Add a backend to an existing WitwaveAgent",
		Long: "Appends a backend to an existing agent's `spec.backends[]`. Port\n" +
			"is auto-assigned to the first free slot in the 8001..8050 range.\n" +
			"When the agent has a single gitSync wired, also scaffolds\n" +
			"`.agents/<…>/.<name>/agent-card.md` (and the behavioural-\n" +
			"instructions stub for LLM backends) to the repo — same layout\n" +
			"`ww agent scaffold` produces. Pass --no-repo-folder to skip the\n" +
			"repo side and leave the CR change in-cluster only.\n\n" +
			"Backend spec follows the same shape as `ww agent create --backend`:\n" +
			"  <type>         — name = type (single-backend shortcut)\n" +
			"  <name>:<type>  — explicit name + type pair (for multiple of a type)\n\n" +
			"Credentials: pick one of --auth / --auth-from-env / --auth-secret.\n" +
			"Omit all three when the backend type needs no credentials (echo).\n" +
			"LLM backends added without credentials will start the pod but error\n" +
			"on first request — a yellow warning appears in the preflight banner.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			specs, err := agent.ParseBackendSpecs([]string{args[1]})
			if err != nil {
				return err
			}
			// ParseBackendSpecs stamps port 8001 for index 0 (right
			// for `create` where ports come from scratch, wrong for
			// `backend add` where the agent already owns backends).
			// Clear it so BackendAdd's nextFreeBackendPort picks the
			// first slot the existing CR isn't using.
			specs[0].Port = 0
			// Build the auth resolver — flags here drop the <backend>=
			// prefix (and --auth-set drops the <backend>: prefix)
			// because the backend is already named positionally.
			auth, err := resolveSingleBackendAuth(specs[0].Name, authProfile, authFromEnv, authSecret, authSet)
			if err != nil {
				return err
			}
			return runAgentBackendAdd(cmd.Context(), f, args[0], specs[0], auth, !noRepoFolder, commitMessage)
		},
	}
	bindAgentMutatingFlags(cmd, f)
	cmd.Flags().StringVar(&authProfile, "auth", "",
		fmt.Sprintf("Named auth profile (e.g. `oauth`, `api-key`). Known: %s", agent.KnownCredentialProfiles()))
	cmd.Flags().StringVar(&authFromEnv, "auth-from-env", "",
		"Mint a K8s Secret from arbitrary env vars. Form: <VAR1>[,VAR2,...]. Secret keys match names verbatim.")
	cmd.Flags().StringVar(&authSecret, "auth-secret", "",
		"Reference an existing K8s Secret (verified, not modified)")
	cmd.Flags().StringArrayVar(&authSet, "auth-set", nil,
		"Mint a Secret with literal KEY=VALUE pairs. Repeatable. Form: <KEY>=<VALUE>. "+
			"SECURITY: values land in shell history + ps output — for production tokens "+
			"prefer --auth-secret or --auth-from-env.")
	cmd.Flags().BoolVar(&noRepoFolder, "no-repo-folder", false,
		"Skip the repo-side `.agents/<…>/.<name>/` scaffold (CR-only change)")
	cmd.Flags().StringVar(&commitMessage, "commit-message", "",
		"Custom commit message for the repo-side scaffold (default: \"Add backend <name> for agent <agent>\")")
	return cmd
}

// resolveSingleBackendAuth converts the four flat auth flags on
// `backend add` into a single BackendAuthResolver. At most one mode
// may be set — they're mutually exclusive (with the exception of
// --auth-set, which is repeatable but counts as one mode regardless
// of how many KEY=VALUE pairs were passed). All-empty is the
// legitimate "no credentials needed" case for echo backends.
func resolveSingleBackendAuth(backend, profile, fromEnv, secret string, set []string) (agent.BackendAuthResolver, error) {
	modes := 0
	if profile != "" {
		modes++
	}
	if fromEnv != "" {
		modes++
	}
	if secret != "" {
		modes++
	}
	if len(set) > 0 {
		modes++
	}
	if modes > 1 {
		return agent.BackendAuthResolver{}, fmt.Errorf(
			"pick at most one of --auth / --auth-from-env / --auth-secret / --auth-set")
	}
	switch {
	case profile != "":
		return agent.BackendAuthResolver{Backend: backend, Mode: agent.BackendAuthProfile, Profile: profile}, nil
	case fromEnv != "":
		return agent.BackendAuthResolver{Backend: backend, Mode: agent.BackendAuthFromEnv, EnvVars: strings.Split(fromEnv, ",")}, nil
	case secret != "":
		return agent.BackendAuthResolver{Backend: backend, Mode: agent.BackendAuthExistingSecret, ExistingSecret: secret}, nil
	case len(set) > 0:
		// --auth-set on `backend add` drops the <backend>: prefix —
		// the backend's already named positionally — so each entry
		// is just KEY=VALUE. Reuse the inner-half of the create
		// parser so the error messages stay uniform across verbs.
		inline := make(map[string]string, len(set))
		for _, raw := range set {
			key, value, err := agent.SplitInlineKV(raw, "--auth-set")
			if err != nil {
				return agent.BackendAuthResolver{}, err
			}
			if existing, dup := inline[key]; dup {
				return agent.BackendAuthResolver{}, fmt.Errorf(
					"--auth-set: key %q given twice (first=%q, second=%q) — pick one",
					key, existing, value,
				)
			}
			inline[key] = value
		}
		return agent.BackendAuthResolver{Backend: backend, Mode: agent.BackendAuthInline, Inline: inline}, nil
	}
	return agent.BackendAuthResolver{Backend: backend, Mode: agent.BackendAuthNone}, nil
}

func runAgentBackendAdd(ctx context.Context, f *agentFlags, name string, spec agent.BackendSpec, auth agent.BackendAuthResolver, repoFolder bool, commitMessage string) error {
	target, resolver, err := f.resolveTarget(ctx)
	if err != nil {
		return err
	}
	cfg, err := resolver.REST()
	if err != nil {
		return err
	}
	_ = target
	ns := logAndResolveNamespace(f.namespace, target.Namespace)
	assumeYes := f.assumeYes || os.Getenv("WW_ASSUME_YES") == "true"
	return agent.BackendAdd(ctx, cfg, agent.BackendAddOptions{
		Agent:         name,
		Namespace:     ns,
		Backend:       spec,
		Auth:          auth,
		RepoFolder:    repoFolder,
		CommitMessage: commitMessage,
		CLIVersion:    Version,
		AssumeYes:     assumeYes,
		DryRun:        f.dryRun,
		Out:           os.Stdout,
		In:            os.Stdin,
	})
}

func newAgentBackendRenameCmd(f *agentFlags) *cobra.Command {
	var (
		noRepoRename  bool
		commitMessage string
	)
	cmd := &cobra.Command{
		Use:   "rename <agent> <old-name> <new-name>",
		Short: "Rename a backend on a WitwaveAgent (both the CR and the repo folder)",
		Long: "Renames a backend in three places, atomically:\n\n" +
			"1. `spec.backends[<N>].name` on the CR.\n" +
			"2. Every harness + per-backend `gitMappings[]` entry whose `src` or\n" +
			"   `dest` path references the old name.\n" +
			"3. When ww owns the inline backend.yaml (spec.config[0], scaffolded\n" +
			"   at create time), regenerates it so `agents:` lists the new name\n" +
			"   and all routing entries targeting the old name repoint at the new.\n\n" +
			"Repo-side rename: when exactly one gitSync is wired AND --no-repo-\n" +
			"rename isn't set, clones the repo, `git mv`s `.agents/<…>/.<old>/`\n" +
			"to `.agents/<…>/.<new>/`, commits + pushes. Credentials inherit from\n" +
			"the system the same way scaffold does (env token → gh auth → git\n" +
			"credential helper → ssh agent).\n\n" +
			"Refuses when:\n" +
			"- old and new names match (nothing to do),\n" +
			"- new name already exists on the agent (would overwrite),\n" +
			"- new name isn't DNS-1123 compliant.\n\n" +
			"Repo-side failure (clone / push) is non-fatal: the CR rename is\n" +
			"preserved and a recovery recipe is printed.",
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentBackendRename(cmd.Context(), f, args[0], args[1], args[2],
				!noRepoRename, commitMessage)
		},
	}
	bindAgentMutatingFlags(cmd, f)
	cmd.Flags().BoolVar(&noRepoRename, "no-repo-rename", false,
		"Rename the CR only; leave the repo folder alone for manual editing")
	cmd.Flags().StringVar(&commitMessage, "commit-message", "",
		"Custom commit message for the repo rename "+
			"(default: \"Rename backend <old> → <new> for agent <name>\")")
	return cmd
}

func runAgentBackendRename(ctx context.Context, f *agentFlags, name, oldName, newName string, repoRename bool, commitMessage string) error {
	target, resolver, err := f.resolveTarget(ctx)
	if err != nil {
		return err
	}
	cfg, err := resolver.REST()
	if err != nil {
		return err
	}
	ns := logAndResolveNamespace(f.namespace, target.Namespace)
	assumeYes := f.assumeYes || os.Getenv("WW_ASSUME_YES") == "true"
	return agent.BackendRename(ctx, cfg, agent.BackendRenameOptions{
		Agent:         name,
		Namespace:     ns,
		OldName:       oldName,
		NewName:       newName,
		RepoRename:    repoRename,
		CommitMessage: commitMessage,
		AssumeYes:     assumeYes,
		DryRun:        f.dryRun,
		Out:           os.Stdout,
		In:            os.Stdin,
	})
}

func newAgentBackendRemoveCmd(f *agentFlags) *cobra.Command {
	var (
		removeRepoFolder bool
		commitMessage    string
	)
	cmd := &cobra.Command{
		Use:   "remove <agent> <backend-name>",
		Short: "Remove a backend from a WitwaveAgent (and optionally its repo folder)",
		Long: "Drops the named backend from the agent's `spec.backends[]` array.\n\n" +
			"When backend.yaml is inline (ww-managed via spec.config[0], the default\n" +
			"for agents created via `ww agent create`), it is regenerated to exclude\n" +
			"the removed backend and route every concern to the new primary (first\n" +
			"remaining backend). Any user-customised routing in the inline file is\n" +
			"lost; re-edit after the remove lands.\n\n" +
			"When backend.yaml is gitSync-managed (a harness-level gitMapping mounts\n" +
			".witwave/ from the repo), spec.config is left alone. Pass --remove-repo-\n" +
			"folder to automate the repo-side cleanup: clone, git rm -r the\n" +
			"`.agents/<…>/.<backend>/` folder, rewrite backend.yaml to exclude the\n" +
			"removed backend, commit + push. Without the flag, the repo's copy is\n" +
			"preserved and the user is responsible for editing it.\n\n" +
			"Refuses to remove the last backend — the CRD requires at least one.\n" +
			"Operator reconciles the pod to drop the sidecar on next reconcile.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentBackendRemove(cmd.Context(), f, args[0], args[1],
				removeRepoFolder, commitMessage)
		},
	}
	bindAgentMutatingFlags(cmd, f)
	cmd.Flags().BoolVar(&removeRepoFolder, "remove-repo-folder", false,
		"Also remove the `.agents/<…>/.<backend>/` folder from the gitSync repo "+
			"and rewrite backend.yaml to drop references to the backend")
	cmd.Flags().StringVar(&commitMessage, "commit-message", "",
		"Custom commit message for the repo removal "+
			"(default: \"Remove backend <name> for agent <agent>\")")
	return cmd
}

func runAgentBackendRemove(ctx context.Context, f *agentFlags, name, backendName string, removeRepoFolder bool, commitMessage string) error {
	target, resolver, err := f.resolveTarget(ctx)
	if err != nil {
		return err
	}
	cfg, err := resolver.REST()
	if err != nil {
		return err
	}
	ns := logAndResolveNamespace(f.namespace, target.Namespace)
	assumeYes := f.assumeYes || os.Getenv("WW_ASSUME_YES") == "true"
	return agent.BackendRemove(ctx, cfg, agent.BackendRemoveOptions{
		Agent:            name,
		Namespace:        ns,
		BackendName:      backendName,
		RemoveRepoFolder: removeRepoFolder,
		CommitMessage:    commitMessage,
		AssumeYes:        assumeYes,
		DryRun:           f.dryRun,
		Out:              os.Stdout,
		In:               os.Stdin,
	})
}

// ---------------------------------------------------------------------------
// git (attach / detach / list gitSync on a WitwaveAgent)
// ---------------------------------------------------------------------------

func newAgentGitCmd(f *agentFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "git",
		Short: "Attach, detach, or list gitSync repos on a WitwaveAgent",
		Long: "Wires a running WitwaveAgent to a git repository via the operator's\n" +
			"gitSync sidecar. Content under `.agents/<name>/.witwave/` and\n" +
			"`.agents/<name>/.<backend>/` lands in the agent pod on each sync\n" +
			"interval — typically the repo produced by `ww agent scaffold`.\n\n" +
			"Auth posture — three ways to provide a credential Secret:\n\n" +
			"  --auth-secret <name>     reference an existing K8s Secret (production)\n" +
			"  --auth-from-gh           mint one from `gh auth token` (dev laptops)\n" +
			"  --auth-from-env <VAR>    mint one from the named env var (CI/CD / .env)\n\n" +
			"Public repos need no auth flag. Secrets minted by ww carry an\n" +
			"`app.kubernetes.io/managed-by: ww` label so detach + delete can\n" +
			"distinguish ww-created Secrets from hand-authored ones.",
	}
	cmd.AddCommand(newAgentGitAddCmd(f))
	cmd.AddCommand(newAgentGitListCmd(f))
	cmd.AddCommand(newAgentGitRemoveCmd(f))
	return cmd
}

func newAgentGitAddCmd(f *agentFlags) *cobra.Command {
	var (
		repo           string
		repoPath       string
		group          string
		branch         string
		period         string
		syncName       string
		authSecret     string
		authFromGH     bool
		authFromEnv    string
		authSecretName string
	)
	cmd := &cobra.Command{
		Use:   "add <agent>",
		Short: "Attach a gitSync to a WitwaveAgent (repo content → agent pod)",
		Long: "Patches the existing WitwaveAgent CR to add (or replace) a gitSync\n" +
			"entry plus the conventional harness + per-backend gitMappings.\n" +
			"Idempotent: re-running with the same --sync-name updates every\n" +
			"field ww owns and leaves unrelated mappings untouched.\n\n" +
			"--repo-path defaults to `.agents/<agent>/` (or `.agents/<group>/<agent>/`\n" +
			"when --group is set), matching the layout produced by `ww agent scaffold`.\n" +
			"Override explicitly for non-standard repo layouts.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentGitAdd(cmd.Context(), f, args[0], agent.GitAddOptions{
				Repo:      repo,
				RepoPath:  repoPath,
				Group:     group,
				Branch:    branch,
				Period:    period,
				SyncName:  syncName,
				AssumeYes: f.assumeYes,
				DryRun:    f.dryRun,
				Out:       os.Stdout,
				In:        os.Stdin,
				Auth: agent.GitAuthResolver{
					Mode:           chooseAuthMode(authSecret, authFromGH, authFromEnv),
					ExistingSecret: authSecret,
					EnvVar:         authFromEnv,
					SecretName:     authSecretName,
				},
			})
		},
	}
	bindAgentMutatingFlags(cmd, f)
	cmd.Flags().StringVar(&repo, "repo", "",
		"Remote repo (owner/repo, host/owner/repo, full URL, or git@host:owner/repo) — required")
	_ = cmd.MarkFlagRequired("repo")
	cmd.Flags().StringVar(&repoPath, "repo-path", "",
		"Path within the repo (default: `.agents/<agent>/` or `.agents/<group>/<agent>/`)")
	cmd.Flags().StringVar(&group, "group", "",
		"Group segment used to derive --repo-path when it's unset (mirrors scaffold's --group)")
	cmd.Flags().StringVar(&branch, "branch", "",
		"Branch / tag / commit to sync (default: remote HEAD)")
	cmd.Flags().StringVar(&period, "period", agent.DefaultGitPeriod,
		"Sync interval (e.g. 30s, 1m, 5m)")
	cmd.Flags().StringVar(&syncName, "sync-name", "",
		"Name for the gitSyncs[] entry (default: sanitised basename of --repo, e.g. "+
			"`owner/my.repo` → `my-repo`). Pick explicitly when wiring two gitSyncs "+
			"with the same repo basename or two branches of the same repo")
	cmd.Flags().StringVar(&authSecret, "auth-secret", "",
		"Reference an existing K8s Secret with GITSYNC_USERNAME / GITSYNC_PASSWORD")
	cmd.Flags().BoolVar(&authFromGH, "auth-from-gh", false,
		"Mint a K8s Secret from `gh auth token` (reads gh's current session)")
	cmd.Flags().StringVar(&authFromEnv, "auth-from-env", "",
		"Mint a K8s Secret from a named env var (e.g. GITHUB_TOKEN)")
	cmd.Flags().StringVar(&authSecretName, "auth-secret-name", "",
		"Name to use when minting a Secret (default: <agent>-git-credentials)")
	return cmd
}

// chooseAuthMode collapses the three mutually-exclusive auth flags into
// a single GitAuthMode. Exactly one may be set; more than one is a
// usage error surfaced at validation time.
func chooseAuthMode(secret string, fromGH bool, env string) agent.GitAuthMode {
	switch {
	case secret != "":
		return agent.GitAuthExistingSecret
	case fromGH:
		return agent.GitAuthFromGH
	case env != "":
		return agent.GitAuthFromEnv
	}
	return agent.GitAuthNone
}

func runAgentGitAdd(ctx context.Context, f *agentFlags, name string, opts agent.GitAddOptions) error {
	target, resolver, err := f.resolveTarget(ctx)
	if err != nil {
		return err
	}
	cfg, err := resolver.REST()
	if err != nil {
		return err
	}
	ns := logAndResolveNamespace(f.namespace, target.Namespace)
	// Validate mutual exclusivity up-front so users get a crisp error
	// rather than having the auth resolver pick one silently.
	if err := assertOneAuthMode(opts.Auth); err != nil {
		return err
	}
	opts.Agent = name
	opts.Namespace = ns
	return agent.GitAdd(ctx, cfg, opts)
}

func assertOneAuthMode(auth agent.GitAuthResolver) error {
	set := 0
	if auth.ExistingSecret != "" {
		set++
	}
	if auth.Mode == agent.GitAuthFromGH {
		set++
	}
	if auth.EnvVar != "" {
		set++
	}
	if set > 1 {
		return fmt.Errorf("pick at most one of --auth-secret / --auth-from-gh / --auth-from-env")
	}
	return nil
}

func newAgentGitListCmd(f *agentFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list <agent>",
		Short: "Show the gitSyncs + mappings configured on a WitwaveAgent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentGitList(cmd.Context(), f, args[0])
		},
	}
	return cmd
}

func runAgentGitList(ctx context.Context, f *agentFlags, name string) error {
	target, resolver, err := f.resolveTarget(ctx)
	if err != nil {
		return err
	}
	cfg, err := resolver.REST()
	if err != nil {
		return err
	}
	ns := logAndResolveNamespace(f.namespace, target.Namespace)
	if f.namespace == "" {
		fmt.Fprintln(os.Stdout)
	}
	return agent.GitList(ctx, cfg, agent.GitListOptions{
		Agent:     name,
		Namespace: ns,
		Out:       os.Stdout,
	})
}

func newAgentGitRemoveCmd(f *agentFlags) *cobra.Command {
	var (
		syncName     string
		deleteSecret bool
	)
	cmd := &cobra.Command{
		Use:   "remove <agent>",
		Short: "Detach a gitSync from a WitwaveAgent",
		Long: "Removes the named gitSyncs[] entry and every harness + per-backend\n" +
			"gitMapping tied to it. Mappings tied to other gitSyncs are preserved.\n\n" +
			"By default the ww-managed credential Secret is kept so a later\n" +
			"`ww agent git add` can re-attach without re-resolving auth. Pass\n" +
			"--delete-secret to remove it (user-created Secrets under the same\n" +
			"name are always preserved — the managed-by label gates deletion).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentGitRemove(cmd.Context(), f, args[0], syncName, deleteSecret)
		},
	}
	bindAgentMutatingFlags(cmd, f)
	cmd.Flags().StringVar(&syncName, "sync-name", "",
		"Name of the gitSyncs[] entry to detach (default: the agent's only "+
			"gitSync when exactly one is configured; required when 2+)")
	cmd.Flags().BoolVar(&deleteSecret, "delete-secret", false,
		"Also delete the ww-managed credential Secret for this sync")
	return cmd
}

func runAgentGitRemove(ctx context.Context, f *agentFlags, name, syncName string, deleteSecret bool) error {
	target, resolver, err := f.resolveTarget(ctx)
	if err != nil {
		return err
	}
	cfg, err := resolver.REST()
	if err != nil {
		return err
	}
	ns := logAndResolveNamespace(f.namespace, target.Namespace)
	return agent.GitRemove(ctx, cfg, agent.GitRemoveOptions{
		Agent:        name,
		Namespace:    ns,
		SyncName:     syncName,
		DeleteSecret: deleteSecret,
		AssumeYes:    f.assumeYes,
		DryRun:       f.dryRun,
		Out:          os.Stdout,
		In:           os.Stdin,
	})
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
		backends      []string
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
			specs, err := agent.ParseBackendSpecs(backends)
			if err != nil {
				return err
			}
			return runAgentScaffold(cmd.Context(), args[0], agent.ScaffoldOptions{
				Repo:          repo,
				Group:         group,
				Backends:      specs,
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
	cmd.Flags().StringArrayVar(&backends, "backend", nil,
		fmt.Sprintf(
			"Backend to scaffold. Repeatable. Two shapes accepted:\n"+
				"  `<type>`        — name = type (single-backend shortcut)\n"+
				"  `<name>:<type>` — explicit name + type pair (for multi-backend agents)\n"+
				"Valid types: %s. Default when omitted: one %s backend.\n"+
				"Example: --backend claude --backend codex  (multi-model consensus)\n"+
				"Example: --backend echo-1:echo --backend echo-2:echo  (two echo backends)",
			strings.Join(agent.KnownBackends(), ", "), agent.DefaultBackend,
		))
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
		backends        []string
		noWait          bool
		timeout         time.Duration
		createNamespace bool
		team            string
		workspaces      []string
		gitSyncs        []string
		gitMaps         []string
		gitSyncSecrets  []string
		authProfiles    []string
		authFromEnv     []string
		authSecrets     []string
		authSet         []string
	)
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a WitwaveAgent CR (defaults to the echo backend)",
		Long: "Creates a WitwaveAgent with one or more backend sidecars. With no flags,\n" +
			"deploys a single echo backend — a zero-dependency stub that requires no API\n" +
			"keys — so you can exercise an agent end-to-end with \"access to a Kubernetes\n" +
			"cluster and the CLI\" as the only prerequisites.\n\n" +
			"Pass --backend repeatedly to declare multiple backends:\n\n" +
			"  ww agent create consensus-agent --backend claude --backend codex\n" +
			"  ww agent create hello --backend echo-1:echo --backend echo-2:echo\n\n" +
			"Each backend's folder in the gitOps repo (and the /home/agent/.<name>/ mount\n" +
			"in the pod) is named after the backend's NAME, not its type — so two backends\n" +
			"of the same type must use the `<name>:<type>` shape to differentiate them.\n\n" +
			"Pass --workspace repeatedly to bind the agent to one or more existing\n" +
			"WitwaveWorkspaces at creation time, equivalent to a follow-up\n" +
			"`ww workspace bind`. v1alpha1 only supports same-namespace binding;\n" +
			"each workspace must already exist in the agent's namespace.\n\n" +
			"After the CR is applied, waits up to --timeout for the operator to report the\n" +
			"agent as Ready. Pass --no-wait to skip the readiness wait.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			specs, err := agent.ParseBackendSpecs(backends)
			if err != nil {
				return err
			}
			auth, err := agent.ParseBackendAuth(authProfiles, authFromEnv, authSecrets, authSet)
			if err != nil {
				return err
			}
			syncs, err := agent.ParseGitSyncs(gitSyncs)
			if err != nil {
				return err
			}
			secrets, err := agent.ParseGitSyncSecrets(gitSyncSecrets)
			if err != nil {
				return err
			}
			syncs, err = agent.ApplyGitSyncSecrets(syncs, secrets)
			if err != nil {
				return err
			}
			maps, err := agent.ParseGitMappings(gitMaps)
			if err != nil {
				return err
			}
			return runAgentCreate(cmd.Context(), f, args[0], specs, !noWait, timeout, createNamespace, team, workspaces, syncs, maps, auth)
		},
	}
	bindAgentMutatingFlags(cmd, f)
	cmd.Flags().StringArrayVar(&backends, "backend", nil,
		fmt.Sprintf(
			"Backend to deploy. Repeatable. Two shapes accepted:\n"+
				"  `<type>`        — name = type (single-backend shortcut)\n"+
				"  `<name>:<type>` — explicit name + type pair (for multi-backend agents)\n"+
				"Valid types: %s. Default when omitted: one %s backend",
			strings.Join(agent.KnownBackends(), ", "), agent.DefaultBackend,
		))
	cmd.Flags().BoolVar(&noWait, "no-wait", false,
		"Return as soon as the CR is accepted; skip the readiness wait")
	cmd.Flags().DurationVar(&timeout, "timeout", 2*time.Minute,
		"Maximum time to wait for the agent to report Ready (ignored with --no-wait)")
	cmd.Flags().BoolVar(&createNamespace, "create-namespace", false,
		"Create the target namespace if it doesn't already exist (no-op otherwise)")
	cmd.Flags().StringVar(&team, "team", "",
		"Stamp witwave.ai/team=<team> at creation (avoids a follow-up `ww agent team join`). "+
			"Omit to leave the agent in the namespace-wide manifest.")
	cmd.Flags().StringArrayVar(&workspaces, "workspace", nil,
		"Bind the agent to a WitwaveWorkspace at creation time (repeatable). "+
			"Equivalent to a follow-up `ww workspace bind <agent> <workspace>`. "+
			"Each named workspace must already exist in the agent's namespace; "+
			"v1alpha1 only supports same-namespace binding.")
	cmd.Flags().StringArrayVar(&gitSyncs, "gitsync", nil,
		"Declare a gitSync entry on the agent (repeatable). Form: <name>=<url>[@<branch>]. "+
			"Populates spec.gitSyncs[] — the operator runs an init+sidecar pair that clones "+
			"the named repo into /git/<name>. The CLI never accepts inline credentials; pair "+
			"with --gitsync-secret to wire a pre-created Kubernetes Secret for private repos.")
	cmd.Flags().StringArrayVar(&gitMaps, "gitmap", nil,
		"Declare a gitMapping (repeatable). Form: [<container>=]<gitsync>:<src>:<dest>. "+
			"<container> defaults to `harness`; pass a backend name from --backend to land "+
			"the mapping on that backend container instead. <gitsync> must reference a "+
			"--gitsync entry. Duplicate (container, dest) pairs are rejected at parse time.")
	cmd.Flags().StringArrayVar(&gitSyncSecrets, "gitsync-secret", nil,
		"Reference a pre-created Kubernetes Secret as gitSync credentials (repeatable). "+
			"Form: <gitsync-name>=<k8s-secret>. The Secret should carry the gitSync env "+
			"variables (typically GITSYNC_USERNAME/GITSYNC_PASSWORD or GITSYNC_SSH_KEY_FILE). "+
			"<gitsync-name> must reference a --gitsync entry; CLI never accepts inline tokens.")
	cmd.Flags().StringArrayVar(&authProfiles, "auth", nil,
		fmt.Sprintf(
			"Per-backend auth profile. Repeatable. Form: <backend>=<profile>.\n"+
				"Profile reads conventional env var(s) from the shell + mints a K8s Secret.\n"+
				"Known profiles: %s",
			agent.KnownCredentialProfiles(),
		))
	cmd.Flags().StringArrayVar(&authFromEnv, "auth-from-env", nil,
		"Escape hatch: mint a K8s Secret from arbitrary env vars. Repeatable. "+
			"Form: <backend>=<VAR1>[,VAR2,...]. Secret keys match the env var names.")
	cmd.Flags().StringArrayVar(&authSecrets, "auth-secret", nil,
		"Reference an existing K8s Secret (verified, not modified). Repeatable. "+
			"Form: <backend>=<secret-name>.")
	cmd.Flags().StringArrayVar(&authSet, "auth-set", nil,
		"Mint a Secret with literal KEY=VALUE pairs. Repeatable per (backend, KEY). "+
			"Form: <backend>:<KEY>=<VALUE>. SECURITY: values land in shell history + ps "+
			"output — for production tokens prefer --auth-secret or --auth-from-env.")
	return cmd
}

func runAgentCreate(ctx context.Context, f *agentFlags, name string, backends []agent.BackendSpec, wait bool, timeout time.Duration, createNamespace bool, team string, workspaces []string, gitSyncs []agent.GitSyncFlagSpec, gitMappings []agent.GitMappingFlagSpec, backendAuth []agent.BackendAuthResolver) error {
	target, resolver, err := f.resolveTarget(ctx)
	if err != nil {
		return err
	}
	cfg, err := resolver.REST()
	if err != nil {
		return err
	}
	ns := logAndResolveNamespace(f.namespace, target.Namespace)

	assumeYes := f.assumeYes || os.Getenv("WW_ASSUME_YES") == "true"
	return agent.Create(ctx, target, cfg, resolver.ConfigFlags(), agent.CreateOptions{
		Name:            name,
		Namespace:       ns,
		Backends:        backends,
		CLIVersion:      Version,
		CreatedBy:       fmt.Sprintf("ww agent create %s", name),
		AssumeYes:       assumeYes,
		DryRun:          f.dryRun,
		Wait:            wait,
		Timeout:         timeout,
		CreateNamespace: createNamespace,
		Team:            team,
		WorkspaceRefs:   workspaces,
		GitSyncs:        gitSyncs,
		GitMappings:     gitMappings,
		BackendAuth:     backendAuth,
		Out:             os.Stdout,
		In:              os.Stdin,
	})
}

// ---------------------------------------------------------------------------
// list
// ---------------------------------------------------------------------------

func newAgentListCmd(f *agentFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List WitwaveAgent CRs across every namespace (narrow with --namespace)",
		Long: "Lists WitwaveAgent CRs. The default scope is EVERY namespace the caller\n" +
			"can read — matches the `kubectl get pods -A` muscle memory that most\n" +
			"operators reach for anyway. Narrow to a single namespace with --namespace.\n\n" +
			"The NAMESPACE column is always shown regardless of scope so sort / grep\n" +
			"pipelines work the same across modes.\n\n" +
			"DESIGN.md NS-3: list is a read verb — the cluster-wide default never\n" +
			"applies to mutating verbs (create, delete, …), which still honour the\n" +
			"context-ns-first resolution.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentList(cmd.Context(), f)
		},
	}
	cmd.Flags().BoolVarP(&f.allNamespaces, "all-namespaces", "A", false,
		"Explicit all-namespaces mode. Redundant — this is already the default — "+
			"but accepted for kubectl parity so muscle-memory flags don't error.")
	return cmd
}

func runAgentList(ctx context.Context, f *agentFlags) error {
	_, resolver, err := f.resolveTarget(ctx)
	if err != nil {
		return err
	}
	cfg, err := resolver.REST()
	if err != nil {
		return err
	}
	// New default scope (DESIGN.md NS-3): list spans every namespace
	// unless the user explicitly narrows it with -n. -A is preserved
	// for kubectl parity — functionally redundant now since the
	// default IS all-namespaces, but harmless to keep.
	allNamespaces := f.allNamespaces || f.namespace == ""
	var ns string
	if !allNamespaces {
		ns = f.namespace
	}
	return agent.List(ctx, cfg, agent.ListOptions{
		Namespace:     ns,
		AllNamespaces: allNamespaces,
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
	ns := logAndResolveNamespace(f.namespace, target.Namespace)
	if f.namespace == "" {
		fmt.Fprintln(os.Stdout)
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
	var (
		removeRepoFolder bool
		deleteGitSecret  bool
		purge            bool
		commitMessage    string
	)
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a WitwaveAgent CR (operator cascades pod cleanup)",
		Long: "Deletes the WitwaveAgent CR. The operator cascades pod + Service\n" +
			"teardown via owner references — no manual cleanup needed on the\n" +
			"cluster side.\n\n" +
			"Optional repo-side + credential cleanup:\n\n" +
			"  --remove-repo-folder   Also wipe the agent's `.agents/<…>/`\n" +
			"                         directory from the wired gitSync repo\n" +
			"                         (clone → git rm -r → commit → push).\n" +
			"                         Requires exactly one gitSync configured;\n" +
			"                         multiple syncs refuse as ambiguous.\n" +
			"                         Runs BEFORE the CR delete so a repo-\n" +
			"                         side failure leaves cluster state intact\n" +
			"                         and the user can retry.\n\n" +
			"  --delete-git-secret    Also delete every ww-managed credential\n" +
			"                         Secret referenced by the CR's\n" +
			"                         gitSyncs[]. Secrets without the\n" +
			"                         `app.kubernetes.io/managed-by: ww` label\n" +
			"                         are preserved regardless.\n\n" +
			"  --purge                Convenience: --remove-repo-folder +\n" +
			"                         --delete-git-secret. For when you want\n" +
			"                         every ww trace of this agent gone.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if purge {
				removeRepoFolder = true
				deleteGitSecret = true
			}
			return runAgentDelete(cmd.Context(), f, args[0], removeRepoFolder, deleteGitSecret, commitMessage)
		},
	}
	bindAgentMutatingFlags(cmd, f)
	cmd.Flags().BoolVar(&removeRepoFolder, "remove-repo-folder", false,
		"Also wipe the agent's `.agents/<…>/` directory from the wired gitSync repo "+
			"(refuses when multiple gitSyncs are configured)")
	cmd.Flags().BoolVar(&deleteGitSecret, "delete-git-secret", false,
		"Also delete every ww-managed credential Secret referenced by the CR's gitSyncs[]")
	cmd.Flags().BoolVar(&purge, "purge", false,
		"Convenience flag: enables --remove-repo-folder AND --delete-git-secret")
	cmd.Flags().StringVar(&commitMessage, "commit-message", "",
		"Custom commit message for the repo wipe (default: \"Remove agent <name>\")")
	return cmd
}

func runAgentDelete(ctx context.Context, f *agentFlags, name string, removeRepoFolder, deleteGitSecret bool, commitMessage string) error {
	target, resolver, err := f.resolveTarget(ctx)
	if err != nil {
		return err
	}
	cfg, err := resolver.REST()
	if err != nil {
		return err
	}
	ns := logAndResolveNamespace(f.namespace, target.Namespace)
	assumeYes := f.assumeYes || os.Getenv("WW_ASSUME_YES") == "true"
	return agent.Delete(ctx, target, cfg, agent.DeleteOptions{
		Name:             name,
		Namespace:        ns,
		RemoveRepoFolder: removeRepoFolder,
		DeleteGitSecret:  deleteGitSecret,
		CommitMessage:    commitMessage,
		AssumeYes:        assumeYes,
		DryRun:           f.dryRun,
		Out:              os.Stdout,
		In:               os.Stdin,
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
		backend   string
	)
	cmd := &cobra.Command{
		Use:   "send <name> <prompt>",
		Short: "Send an A2A prompt to an agent via the Kubernetes apiserver Service proxy",
		Long: "Makes a single A2A message/send round-trip against the agent's harness\n" +
			"Service. Uses the apiserver's built-in Service proxy so no local port-forward\n" +
			"or external LoadBalancer is required — any ClusterIP Service works.\n\n" +
			"By default the harness routes the prompt to whichever backend backend.yaml\n" +
			"names as the primary for the `a2a` concern. Pass --backend <name> to bypass\n" +
			"that routing and target a specific backend container directly — the harness\n" +
			"honours the metadata.backend_id hint and dispatches to the named sidecar.\n\n" +
			"Not suited for streaming or very large payloads (apiserver proxy has size\n" +
			"caps); ww agent logs -f is the right tool for live observation. Use --raw\n" +
			"to print the full JSON-RPC envelope for debugging.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentSend(cmd.Context(), f, args[0], args[1], messageID, timeout, rawJSON, backend)
		},
	}
	cmd.Flags().StringVar(&messageID, "message-id", "",
		"Explicit A2A messageId (default: ww-send-<timestamp>)")
	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Second,
		"Round-trip timeout through the apiserver Service proxy")
	cmd.Flags().BoolVar(&rawJSON, "raw", false,
		"Print the raw JSON-RPC response envelope instead of extracting the agent text")
	cmd.Flags().StringVar(&backend, "backend", "",
		"Target a specific backend by name (stamps metadata.backend_id; harness "+
			"dispatches directly to the named sidecar instead of routing per backend.yaml)")
	return cmd
}

func runAgentSend(ctx context.Context, f *agentFlags, name, prompt, messageID string, timeout time.Duration, rawJSON bool, backend string) error {
	target, resolver, err := f.resolveTarget(ctx)
	if err != nil {
		return err
	}
	cfg, err := resolver.REST()
	if err != nil {
		return err
	}
	ns := logAndResolveNamespace(f.namespace, target.Namespace)
	return agent.Send(ctx, cfg, agent.SendOptions{
		Agent:     name,
		Namespace: ns,
		Prompt:    prompt,
		MessageID: messageID,
		BackendID: backend,
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
	ns := logAndResolveNamespace(f.namespace, target.Namespace)
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
	ns := logAndResolveNamespace(f.namespace, target.Namespace)
	opts.Agent = name
	opts.Namespace = ns
	return agent.Events(ctx, cfg, opts)
}
