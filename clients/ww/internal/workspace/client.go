package workspace

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/witwave-ai/witwave/clients/ww/internal/agent"
)

// clientFactory holds the constructor functions verb implementations use
// to build Kubernetes clients from a *rest.Config. Production callers use
// the real client-go constructors; tests swap these out for functions
// returning fakes, so they can drive the verbs without an apiserver.
//
// Mirrors internal/agent/gitops.go's clientFactory pattern so test setup
// is uniform across both packages.
var clientFactory = struct {
	dyn  func(*rest.Config) (dynamic.Interface, error)
	kube func(*rest.Config) (kubernetes.Interface, error)
}{
	dyn: func(cfg *rest.Config) (dynamic.Interface, error) {
		return dynamic.NewForConfig(cfg)
	},
	kube: func(cfg *rest.Config) (kubernetes.Interface, error) {
		return kubernetes.NewForConfig(cfg)
	},
}

// newDynamicClient is the shared constructor so verb implementations
// don't each carry the build-client boilerplate. Delegates through
// clientFactory.dyn so tests can substitute a fake implementation.
func newDynamicClient(cfg *rest.Config) (dynamic.Interface, error) {
	dyn, err := clientFactory.dyn(cfg)
	if err != nil {
		return nil, fmt.Errorf("build dynamic client: %w", err)
	}
	return dyn, nil
}

// fetchWitwaveWorkspaceCR returns the current state of the named WitwaveWorkspace CR,
// or a diagnosable error.
func fetchWitwaveWorkspaceCR(ctx context.Context, dyn dynamic.Interface, namespace, name string) (*unstructured.Unstructured, error) {
	cr, err := dyn.Resource(GVR()).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf(
				"WitwaveWorkspace %q not found in namespace %q",
				name, namespace,
			)
		}
		return nil, fmt.Errorf("get workspace: %w", err)
	}
	return cr, nil
}

// fetchAgentCR returns the current state of the named WitwaveAgent CR.
// Used by bind/unbind to mutate the agent's Spec.WorkspaceRefs in place.
func fetchAgentCR(ctx context.Context, dyn dynamic.Interface, namespace, name string) (*unstructured.Unstructured, error) {
	cr, err := dyn.Resource(agent.GVR()).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf(
				"WitwaveAgent %q not found in namespace %q — create it first with `ww agent create %s`",
				name, namespace, name,
			)
		}
		return nil, fmt.Errorf("get agent: %w", err)
	}
	return cr, nil
}

// updateAgentCR writes the agent CR back via the dynamic client.
func updateAgentCR(ctx context.Context, dyn dynamic.Interface, cr *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	updated, err := dyn.Resource(agent.GVR()).Namespace(cr.GetNamespace()).Update(ctx, cr, metav1.UpdateOptions{})
	if err != nil {
		return nil, fmt.Errorf("update agent: %w", err)
	}
	return updated, nil
}

// readWorkspaceRefs returns the current spec.workspaceRefs[] on a
// WitwaveAgent CR as a []map[string]interface{}. Missing → empty slice.
// Errors on malformed shape (caller can't proceed safely if the existing
// CR's workspaceRefs field is bogus).
func readWorkspaceRefs(cr *unstructured.Unstructured) ([]map[string]interface{}, error) {
	raw, found, err := unstructured.NestedSlice(cr.Object, "spec", "workspaceRefs")
	if err != nil {
		return nil, fmt.Errorf("read spec.workspaceRefs: %w", err)
	}
	if !found {
		return nil, nil
	}
	out := make([]map[string]interface{}, 0, len(raw))
	for i, entry := range raw {
		m, ok := entry.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("spec.workspaceRefs[%d] is not a map; got %T", i, entry)
		}
		out = append(out, m)
	}
	return out, nil
}

// writeWorkspaceRefs replaces the workspaceRefs array wholesale on the
// CR. Pass an empty slice to clear the field entirely.
func writeWorkspaceRefs(cr *unstructured.Unstructured, refs []map[string]interface{}) error {
	if len(refs) == 0 {
		unstructured.RemoveNestedField(cr.Object, "spec", "workspaceRefs")
		return nil
	}
	asSlice := make([]interface{}, 0, len(refs))
	for _, r := range refs {
		asSlice = append(asSlice, r)
	}
	return unstructured.SetNestedSlice(cr.Object, asSlice, "spec", "workspaceRefs")
}
