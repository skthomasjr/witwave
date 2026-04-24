package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"

	"github.com/skthomasjr/witwave/clients/ww/internal/k8s"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/rest"
)

// CreateOptions collects the runtime inputs for `ww agent create`.
type CreateOptions struct {
	Name      string
	Namespace string

	// Backends is the list of declared backends. Empty → a single
	// default-echo backend (the hello-world shortcut). Populate via
	// ParseBackendSpecs to turn the repeatable --backend cobra flag
	// into structured entries.
	Backends []BackendSpec

	// CLIVersion from cmd.Version; used to resolve image tags.
	CLIVersion string

	// CreatedBy captures the invoking command (e.g. "ww agent create hello")
	// for the CR's created-by annotation.
	CreatedBy string

	// AssumeYes skips the preflight confirmation. --yes / WW_ASSUME_YES.
	AssumeYes bool
	// DryRun renders the banner and exits without calling the API server.
	DryRun bool

	// Wait controls whether we block after Create until the CR's
	// status.phase flips to Ready. Timeout bounds the wait.
	Wait    bool
	Timeout time.Duration

	// Out + In route UI to/from the caller. Usually os.Stdout / os.Stdin.
	Out io.Writer
	In  io.Reader
}

// Create applies a WitwaveAgent CR to the target cluster. Flow:
//
//  1. Build the unstructured CR from opts + defaults.
//  2. Render preflight banner via k8s.Confirm (honours --yes / --dry-run /
//     local-cluster heuristic per DESIGN.md KC-4).
//  3. Create via dynamic client. AlreadyExists is surfaced cleanly so a
//     user re-running with the same name gets a clear error, not a panic.
//  4. Optionally wait for status.phase == Ready.
func Create(
	ctx context.Context,
	target *k8s.Target,
	cfg *rest.Config,
	flags *genericclioptions.ConfigFlags,
	opts CreateOptions,
) error {
	if opts.Out == nil {
		return fmt.Errorf("CreateOptions.Out is required")
	}
	// Fallback to a single-echo backend when caller didn't supply any —
	// matches the legacy `Backend: ""` behaviour so the hello-world
	// `ww agent create hello` (no flags) stays unchanged.
	backends := opts.Backends
	if len(backends) == 0 {
		backends = []BackendSpec{{Name: DefaultBackend, Type: DefaultBackend, Port: BackendPort(0)}}
	}

	obj, err := Build(BuildOptions{
		Name:       opts.Name,
		Namespace:  opts.Namespace,
		Backends:   backends,
		CLIVersion: opts.CLIVersion,
		CreatedBy:  opts.CreatedBy,
	})
	if err != nil {
		return fmt.Errorf("build agent CR: %w", err)
	}

	plan := []k8s.PlanLine{
		{Key: "Action", Value: fmt.Sprintf("create WitwaveAgent %q", opts.Name)},
		{Key: "Backends", Value: summariseBackends(backends)},
		{Key: "Harness image", Value: HarnessImage(opts.CLIVersion)},
	}
	if IsDevVersion(opts.CLIVersion) {
		plan = append(plan, k8s.PlanLine{
			Key:   "Note",
			Value: "dev build — images will resolve to :latest (floating tag)",
		})
	}

	proceed, err := k8s.Confirm(opts.Out, opts.In, target, plan, k8s.PromptOptions{
		AssumeYes: opts.AssumeYes,
		DryRun:    opts.DryRun,
	})
	if err != nil {
		return err
	}
	if !proceed {
		return nil
	}

	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("build dynamic client: %w", err)
	}

	created, err := dyn.Resource(GVR()).Namespace(opts.Namespace).Create(ctx, obj, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			return fmt.Errorf(
				"WitwaveAgent %q already exists in namespace %q; delete it with `ww agent delete %s -n %s` or choose a different name",
				opts.Name, opts.Namespace, opts.Name, opts.Namespace,
			)
		}
		return fmt.Errorf("create agent: %w", err)
	}
	fmt.Fprintf(opts.Out, "Created WitwaveAgent %s in namespace %s (uid=%s).\n",
		created.GetName(), created.GetNamespace(), created.GetUID(),
	)

	if !opts.Wait {
		fmt.Fprintln(opts.Out, "Skipping readiness wait (--no-wait). Check with `ww agent status "+opts.Name+"`.")
		return nil
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	fmt.Fprintf(opts.Out, "Waiting up to %s for agent to report Ready...\n", timeout)
	if err := waitForReady(ctx, dyn, opts.Namespace, opts.Name, timeout, opts.Out); err != nil {
		return err
	}
	fmt.Fprintf(opts.Out, "\nAgent %s is ready.\n", opts.Name)
	fmt.Fprintln(opts.Out, "Next steps:")
	fmt.Fprintln(opts.Out, "  ww agent status "+opts.Name+"  # see pod + reconcile state")
	fmt.Fprintln(opts.Out, "  ww agent delete "+opts.Name+"  # clean up")
	return nil
}

// summariseBackends returns a compact "name:type/port" summary for the
// preflight banner. `echo-1:echo/8001, echo-2:echo/8002` — enough
// information for the user to confirm the shape at a glance.
func summariseBackends(backends []BackendSpec) string {
	if len(backends) == 0 {
		return "<none — default echo>"
	}
	parts := make([]string, 0, len(backends))
	for _, b := range backends {
		if b.Name == b.Type {
			parts = append(parts, fmt.Sprintf("%s/%d", b.Name, b.Port))
		} else {
			parts = append(parts, fmt.Sprintf("%s:%s/%d", b.Name, b.Type, b.Port))
		}
	}
	return strings.Join(parts, ", ")
}

// waitForReady polls the CR's .status.phase until it reads "Ready" or
// the timeout elapses. Poll interval is deliberately modest (2s) —
// operators reconcile on a handful of events, not on a busy-loop.
func waitForReady(ctx context.Context, dyn dynamic.Interface, ns, name string, timeout time.Duration, out io.Writer) error {
	deadline := time.Now().Add(timeout)
	var lastPhase string
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s waiting for agent %q to report Ready (last phase: %q)", timeout, name, lastPhase)
		}

		cr, err := dyn.Resource(GVR()).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			// Transient Get errors shouldn't abort the wait; log and retry.
			fmt.Fprintf(out, "  (get failed: %v; retrying)\n", err)
		} else {
			phase := readPhase(cr)
			if phase != lastPhase {
				fmt.Fprintf(out, "  phase: %s\n", phase)
				lastPhase = phase
			}
			if phase == "Ready" {
				return nil
			}
		}

		select {
		case <-ctx.Done():
			return errors.New("wait cancelled")
		case <-time.After(2 * time.Second):
		}
	}
}

// readPhase pulls .status.phase from an unstructured CR. Returns an
// empty string when the field is absent — treated the same as "not yet
// reconciled" by the caller.
func readPhase(cr *unstructured.Unstructured) string {
	phase, found, err := unstructured.NestedString(cr.Object, "status", "phase")
	if err != nil || !found {
		return ""
	}
	return phase
}
