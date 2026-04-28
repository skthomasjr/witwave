package cmd

import (
	"context"
	"fmt"
	"os"

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
