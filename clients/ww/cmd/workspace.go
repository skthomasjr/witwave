package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/witwave-ai/witwave/clients/ww/internal/k8s"
	"github.com/witwave-ai/witwave/clients/ww/internal/workspace"
)

// workspaceFlags carries the namespace flag shared across every
// `ww workspace *` subcommand. Per DESIGN.md KC-6 (namespace-per-subtree)
// + NS-1 (default to context's namespace) + NS-2 (always print resolved
// ns). Cluster-identity flags (--kubeconfig, --context) live on the root
// command per KC-5 and reach us via K8sFromCtx.
type workspaceFlags struct {
	namespace     string
	allNamespaces bool

	// Mutating-command flags. Not every subcommand wires both.
	assumeYes bool
	dryRun    bool
}

func bindWorkspaceFlags(cmd *cobra.Command, f *workspaceFlags) {
	cmd.PersistentFlags().StringVarP(&f.namespace, "namespace", "n", "",
		fmt.Sprintf("Namespace for the workspace (defaults to the kubeconfig context's namespace, then %q)", workspace.DefaultWorkspaceNamespace))
}

func bindWorkspaceMutatingFlags(cmd *cobra.Command, f *workspaceFlags) {
	cmd.Flags().BoolVarP(&f.assumeYes, "yes", "y", false,
		"Skip the preflight confirmation prompt (or set WW_ASSUME_YES=true)")
	cmd.Flags().BoolVar(&f.dryRun, "dry-run", false,
		"Print the plan and exit without applying any changes")
}

func (f *workspaceFlags) resolveTarget(ctx context.Context) (*k8s.Target, *k8s.Resolver, error) {
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

// logAndResolveWorkspaceNamespace mirrors logAndResolveNamespace from
// cmd/agent.go but pinned to the workspace subtree's defaults. Keeps
// DESIGN.md NS-2 (always echo the resolved namespace when the flag was
// omitted) consistent across every `ww workspace *` verb.
func logAndResolveWorkspaceNamespace(flagValue, contextNS string) string {
	ns, source := workspace.ResolveNamespaceWithSource(flagValue, contextNS)
	if source == workspace.NamespaceFromFlag {
		return ns
	}
	var why string
	switch source {
	case workspace.NamespaceFromContext:
		why = "from kubeconfig context"
	case workspace.NamespaceFromDefault:
		why = "ww default"
	}
	fmt.Fprintf(os.Stdout, "Using namespace: %s (%s)\n", ns, why)
	return ns
}

// newWorkspaceCmd is the parent command for `ww workspace *`.
func newWorkspaceCmd() *cobra.Command {
	f := &workspaceFlags{}
	cmd := &cobra.Command{
		Use:   "workspace",
		Short: "Manage Workspace custom resources on a Kubernetes cluster",
		Long: "Create, list, inspect, delete, and bind Workspace CRs. The witwave-operator\n" +
			"reconciles each Workspace into a set of shared volumes, projected Secrets,\n" +
			"and rendered ConfigMaps that participating WitwaveAgents see at runtime.\n\n" +
			"Membership is agent-owned: a WitwaveAgent declares which workspaces it\n" +
			"participates in via spec.workspaceRefs[]. Use `ww workspace bind <agent>\n" +
			"<workspace>` and `ww workspace unbind <agent> <workspace>` to manage that\n" +
			"list without hand-editing the CR.\n\n" +
			"Prerequisite: the operator must already be installed on the target cluster\n" +
			"(see `ww operator install`). Every `ww workspace *` command honours the\n" +
			"ambient kubeconfig and current-context (override via the root --kubeconfig\n" +
			"/ --context flags). Use --namespace / -n to target a specific namespace;\n" +
			"omit to use the kubeconfig context's namespace (falling back to \"" +
			workspace.DefaultWorkspaceNamespace + "\").",
	}
	bindWorkspaceFlags(cmd, f)

	cmd.AddCommand(newWorkspaceCreateCmd(f))
	cmd.AddCommand(newWorkspaceListCmd(f))
	cmd.AddCommand(newWorkspaceGetCmd(f))
	cmd.AddCommand(newWorkspaceStatusCmd(f))
	cmd.AddCommand(newWorkspaceDeleteCmd(f))
	cmd.AddCommand(newWorkspaceBindCmd(f))
	cmd.AddCommand(newWorkspaceUnbindCmd(f))
	return cmd
}

// ---------------------------------------------------------------------------
// create
// ---------------------------------------------------------------------------

func newWorkspaceCreateCmd(f *workspaceFlags) *cobra.Command {
	var (
		fromFile        string
		volumes         []string
		secrets         []string
		createNamespace bool
	)
	cmd := &cobra.Command{
		Use:   "create [name]",
		Short: "Create a Workspace CR (from a YAML file or convenience flags)",
		Long: "Creates a Workspace CR. Two construction modes:\n\n" +
			"  ww workspace create -f workspace.yaml\n" +
			"  ww workspace create my-ws --volume source=50Gi@efs-sc --secret github-token=env\n\n" +
			"--volume <name>=<size>[@<storageClass>]   declare a shared PVC. Repeatable.\n" +
			"                                          Defaults to RWM access mode (the\n" +
			"                                          v1alpha1 contract — RWO is v1.x).\n" +
			"--secret <name>[@/abs/path | =env]        reference an existing Secret.\n" +
			"                                          - bare name              → reference only\n" +
			"                                          - name@/abs/path         → mount at path\n" +
			"                                          - name=env               → project as envFrom\n\n" +
			"--from-file is mutually exclusive with --volume / --secret. Use the file\n" +
			"path for full control over reclaim policies, configFiles, and other\n" +
			"fields the convenience flags don't surface.\n\n" +
			"After the CR is applied the operator reconciles the PVC(s) and the\n" +
			"inline ConfigMaps (when present); use `ww workspace status <name>`\n" +
			"to watch provisioning progress.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var name string
			if len(args) == 1 {
				name = args[0]
			}
			vols, err := workspace.ParseVolumeSpecs(volumes)
			if err != nil {
				return err
			}
			secs, err := workspace.ParseSecretSpecs(secrets)
			if err != nil {
				return err
			}
			return runWorkspaceCreate(cmd.Context(), f, name, fromFile, vols, secs, createNamespace)
		},
	}
	bindWorkspaceMutatingFlags(cmd, f)
	cmd.Flags().StringVarP(&fromFile, "from-file", "f", "",
		"Path to a YAML/JSON Workspace manifest. Mutually exclusive with --volume / --secret.")
	cmd.Flags().StringArrayVar(&volumes, "volume", nil,
		"Shared volume to declare. Repeatable. Form: <name>=<size>[@<storageClass>]. "+
			"Defaults to ReadWriteMany access mode.")
	cmd.Flags().StringArrayVar(&secrets, "secret", nil,
		"Existing-Secret reference. Repeatable. Form: <name>, <name>@/abs/path, or <name>=env.")
	cmd.Flags().BoolVar(&createNamespace, "create-namespace", false,
		"Create the target namespace if it doesn't already exist (no-op otherwise)")
	return cmd
}

func runWorkspaceCreate(ctx context.Context, f *workspaceFlags, name, fromFile string, volumes []workspace.VolumeSpec, secrets []workspace.SecretSpec, createNamespace bool) error {
	if name == "" && fromFile == "" {
		return fmt.Errorf("either a positional <name> or --from-file is required")
	}
	target, resolver, err := f.resolveTarget(ctx)
	if err != nil {
		return err
	}
	cfg, err := resolver.REST()
	if err != nil {
		return err
	}
	ns := logAndResolveWorkspaceNamespace(f.namespace, target.Namespace)
	assumeYes := f.assumeYes || os.Getenv("WW_ASSUME_YES") == "true"

	createdBy := "ww workspace create"
	if name != "" {
		createdBy += " " + name
	}
	return workspace.Create(ctx, target, cfg, workspace.CreateOptions{
		Name:            name,
		Namespace:       ns,
		FromFile:        fromFile,
		Volumes:         volumes,
		Secrets:         secrets,
		CLIVersion:      Version,
		CreatedBy:       createdBy,
		AssumeYes:       assumeYes,
		DryRun:          f.dryRun,
		CreateNamespace: createNamespace,
		Out:             os.Stdout,
		In:              os.Stdin,
	})
}

// ---------------------------------------------------------------------------
// list
// ---------------------------------------------------------------------------

func newWorkspaceListCmd(f *workspaceFlags) *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List Workspace CRs across every namespace (narrow with --namespace)",
		Long: "Lists Workspace CRs. The default scope is EVERY namespace the caller\n" +
			"can read — matches the `kubectl get ws -A` muscle memory that most\n" +
			"operators reach for. Narrow to a single namespace with --namespace.\n\n" +
			"Columns: NAMESPACE, NAME, VOLUMES, AGENTS, AGE, STATUS. The NAMESPACE\n" +
			"column is always shown regardless of scope so sort / grep pipelines\n" +
			"work the same across modes.\n\n" +
			"DESIGN.md NS-3: list is a read verb — the cluster-wide default never\n" +
			"applies to mutating verbs (create, delete, …), which still honour\n" +
			"the context-ns-first resolution.",
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := workspace.ParseOutputFormat(output)
			if err != nil {
				return err
			}
			return runWorkspaceList(cmd.Context(), f, format)
		},
	}
	cmd.Flags().BoolVarP(&f.allNamespaces, "all-namespaces", "A", false,
		"Explicit all-namespaces mode. Redundant — this is already the default — "+
			"but accepted for kubectl parity so muscle-memory flags don't error.")
	cmd.Flags().StringVarP(&output, "output", "o", "",
		"Output format: table (default), yaml, json")
	return cmd
}

func runWorkspaceList(ctx context.Context, f *workspaceFlags, format workspace.OutputFormat) error {
	_, resolver, err := f.resolveTarget(ctx)
	if err != nil {
		return err
	}
	cfg, err := resolver.REST()
	if err != nil {
		return err
	}
	allNamespaces := f.allNamespaces || f.namespace == ""
	var ns string
	if !allNamespaces {
		ns = f.namespace
	}
	return workspace.List(ctx, cfg, workspace.ListOptions{
		Namespace:     ns,
		AllNamespaces: allNamespaces,
		Output:        format,
		Out:           os.Stdout,
	})
}

// ---------------------------------------------------------------------------
// get
// ---------------------------------------------------------------------------

func newWorkspaceGetCmd(f *workspaceFlags) *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "get <name>",
		Short: "Fetch a single Workspace and emit YAML/JSON or a one-row table",
		Long: "Fetches a Workspace by name and emits it. Default output is a single-row\n" +
			"table identical to `ww workspace list -n <ns>` scoped to one entry; pass\n" +
			"`-o yaml` or `-o json` to print the underlying object verbatim, suitable\n" +
			"for piping into `kubectl apply -f -` or `jq`.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := workspace.ParseOutputFormat(output)
			if err != nil {
				return err
			}
			return runWorkspaceGet(cmd.Context(), f, args[0], format)
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "",
		"Output format: table (default), yaml, json")
	return cmd
}

func runWorkspaceGet(ctx context.Context, f *workspaceFlags, name string, format workspace.OutputFormat) error {
	target, resolver, err := f.resolveTarget(ctx)
	if err != nil {
		return err
	}
	cfg, err := resolver.REST()
	if err != nil {
		return err
	}
	ns := logAndResolveWorkspaceNamespace(f.namespace, target.Namespace)
	if f.namespace == "" {
		fmt.Fprintln(os.Stdout)
	}
	return workspace.Get(ctx, cfg, workspace.GetOptions{
		Name:      name,
		Namespace: ns,
		Output:    format,
		Out:       os.Stdout,
	})
}

// ---------------------------------------------------------------------------
// status
// ---------------------------------------------------------------------------

func newWorkspaceStatusCmd(f *workspaceFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status <name>",
		Short: "Show volumes, conditions, and bound agents for a Workspace",
		Long: "Renders a curated, human-readable view of a Workspace: identity,\n" +
			"declared volumes (size + storage class + reclaim policy + mount path),\n" +
			"declared secrets and configFiles, the controller's reconcile conditions,\n" +
			"and the inverted-index list of currently bound agents.\n\n" +
			"For full YAML output use `ww workspace get <name> -o yaml`.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWorkspaceStatus(cmd.Context(), f, args[0])
		},
	}
	return cmd
}

func runWorkspaceStatus(ctx context.Context, f *workspaceFlags, name string) error {
	target, resolver, err := f.resolveTarget(ctx)
	if err != nil {
		return err
	}
	cfg, err := resolver.REST()
	if err != nil {
		return err
	}
	ns := logAndResolveWorkspaceNamespace(f.namespace, target.Namespace)
	if f.namespace == "" {
		fmt.Fprintln(os.Stdout)
	}
	return workspace.Status(ctx, cfg, workspace.StatusOptions{
		Name:      name,
		Namespace: ns,
		Out:       os.Stdout,
	})
}

// ---------------------------------------------------------------------------
// delete
// ---------------------------------------------------------------------------

func newWorkspaceDeleteCmd(f *workspaceFlags) *cobra.Command {
	var (
		wait    bool
		timeout time.Duration
	)
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a Workspace CR (refuse-delete finalizer blocks while bound)",
		Long: "Deletes the Workspace CR. The operator stamps a refuse-delete\n" +
			"finalizer on every Workspace, so the apiserver will mark the CR\n" +
			"Terminating but block actual removal until every agent that\n" +
			"references it via spec.workspaceRefs[] unbinds.\n\n" +
			"The plan banner enumerates the currently-bound agents up-front so\n" +
			"there are no surprises. Pass --wait to block until the apiserver\n" +
			"removes the CR (bounded by --timeout, default 2m).\n\n" +
			"To unblock a stuck delete, unbind the offending agents:\n\n" +
			"  ww workspace unbind <agent> <workspace>",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWorkspaceDelete(cmd.Context(), f, args[0], wait, timeout)
		},
	}
	bindWorkspaceMutatingFlags(cmd, f)
	cmd.Flags().BoolVar(&wait, "wait", false,
		"Block until the apiserver removes the CR (bounded by --timeout)")
	cmd.Flags().DurationVar(&timeout, "timeout", 2*time.Minute,
		"Maximum time to wait when --wait is set")
	return cmd
}

func runWorkspaceDelete(ctx context.Context, f *workspaceFlags, name string, wait bool, timeout time.Duration) error {
	target, resolver, err := f.resolveTarget(ctx)
	if err != nil {
		return err
	}
	cfg, err := resolver.REST()
	if err != nil {
		return err
	}
	ns := logAndResolveWorkspaceNamespace(f.namespace, target.Namespace)
	assumeYes := f.assumeYes || os.Getenv("WW_ASSUME_YES") == "true"
	target.Namespace = ns
	return workspace.Delete(ctx, target, cfg, workspace.DeleteOptions{
		Name:        name,
		Namespace:   ns,
		AssumeYes:   assumeYes,
		DryRun:      f.dryRun,
		Wait:        wait,
		WaitTimeout: timeout,
		Out:         os.Stdout,
		In:          os.Stdin,
	})
}

// ---------------------------------------------------------------------------
// bind
// ---------------------------------------------------------------------------

func newWorkspaceBindCmd(f *workspaceFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bind <agent> <workspace>",
		Short: "Add a workspace to a WitwaveAgent's spec.workspaceRefs[]",
		Long: "Adds an entry to the named WitwaveAgent's spec.workspaceRefs[]. The\n" +
			"operator picks up the change on next reconcile and stamps the\n" +
			"workspace's volumes, secrets, and configFiles onto the agent's\n" +
			"backend containers.\n\n" +
			"Idempotent: re-binding the same (agent, workspace) is a no-op with\n" +
			"a clear log line.\n\n" +
			"v1alpha1 only supports same-namespace binding (the operator ignores\n" +
			"cross-namespace refs). The CLI rejects cross-namespace asks loudly\n" +
			"so users see the limitation up-front.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWorkspaceBind(cmd.Context(), f, args[0], args[1])
		},
	}
	bindWorkspaceMutatingFlags(cmd, f)
	return cmd
}

func runWorkspaceBind(ctx context.Context, f *workspaceFlags, agentName, workspaceName string) error {
	target, resolver, err := f.resolveTarget(ctx)
	if err != nil {
		return err
	}
	cfg, err := resolver.REST()
	if err != nil {
		return err
	}
	ns := logAndResolveWorkspaceNamespace(f.namespace, target.Namespace)
	return workspace.Bind(ctx, cfg, workspace.BindOptions{
		Agent:              agentName,
		AgentNamespace:     ns,
		Workspace:          workspaceName,
		WorkspaceNamespace: ns,
		AssumeYes:          f.assumeYes,
		DryRun:             f.dryRun,
		Out:                os.Stdout,
		In:                 os.Stdin,
	})
}

// ---------------------------------------------------------------------------
// unbind
// ---------------------------------------------------------------------------

func newWorkspaceUnbindCmd(f *workspaceFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unbind <agent> <workspace>",
		Short: "Remove a workspace from a WitwaveAgent's spec.workspaceRefs[]",
		Long: "Removes the named workspace from a WitwaveAgent's spec.workspaceRefs[].\n" +
			"The operator drops the workspace mounts from the agent's backend\n" +
			"containers on next reconcile.\n\n" +
			"Idempotent: unbinding an agent that wasn't bound is a no-op.\n\n" +
			"Does NOT delete the Workspace itself — use `ww workspace delete\n" +
			"<workspace>` once every agent that references it has unbound.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWorkspaceUnbind(cmd.Context(), f, args[0], args[1])
		},
	}
	bindWorkspaceMutatingFlags(cmd, f)
	return cmd
}

func runWorkspaceUnbind(ctx context.Context, f *workspaceFlags, agentName, workspaceName string) error {
	target, resolver, err := f.resolveTarget(ctx)
	if err != nil {
		return err
	}
	cfg, err := resolver.REST()
	if err != nil {
		return err
	}
	ns := logAndResolveWorkspaceNamespace(f.namespace, target.Namespace)
	return workspace.Unbind(ctx, cfg, workspace.UnbindOptions{
		Agent:          agentName,
		AgentNamespace: ns,
		Workspace:      workspaceName,
		AssumeYes:      f.assumeYes,
		DryRun:         f.dryRun,
		Out:            os.Stdout,
		In:             os.Stdin,
	})
}
