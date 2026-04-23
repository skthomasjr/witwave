package agent

import (
	"context"
	"fmt"
	"io"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

// Default gitSync entry name. One sidecar per agent is the common case;
// this name keeps the CR readable and predictable. Override via the
// --sync-name flag if multiple gitSyncs are wired to the same agent.
const DefaultGitSyncName = "witwave"

// DefaultGitPeriod matches the chart's default sync interval
// (charts/witwave/values.yaml gitSyncs[].period comment). Chosen to
// balance "recent edits pick up quickly" with "we're not hammering
// github.com every 10 seconds."
const DefaultGitPeriod = "60s"

// fetchAgentCR returns the current state of the named WitwaveAgent CR,
// or a diagnosable error. Shared by every `ww agent git *` verb.
func fetchAgentCR(ctx context.Context, dyn dynamic.Interface, namespace, name string) (*unstructured.Unstructured, error) {
	cr, err := dyn.Resource(GVR()).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
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

// updateAgentCR writes the CR back via the dynamic client. Callers
// construct the desired state (with whatever gitSyncs / gitMappings
// mutations needed) then hand the full object to this helper.
func updateAgentCR(ctx context.Context, dyn dynamic.Interface, cr *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	updated, err := dyn.Resource(GVR()).Namespace(cr.GetNamespace()).Update(ctx, cr, metav1.UpdateOptions{})
	if err != nil {
		return nil, fmt.Errorf("update agent: %w", err)
	}
	return updated, nil
}

// readGitSyncs returns the current gitSyncs array on a CR as a
// []map[string]interface{}. Missing → empty slice. Errors on malformed
// shape (caller can't proceed safely if the existing CR's gitSyncs
// field is bogus).
func readGitSyncs(cr *unstructured.Unstructured) ([]map[string]interface{}, error) {
	raw, found, err := unstructured.NestedSlice(cr.Object, "spec", "gitSyncs")
	if err != nil {
		return nil, fmt.Errorf("read spec.gitSyncs: %w", err)
	}
	if !found {
		return nil, nil
	}
	out := make([]map[string]interface{}, 0, len(raw))
	for i, entry := range raw {
		m, ok := entry.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("spec.gitSyncs[%d] is not a map; got %T", i, entry)
		}
		out = append(out, m)
	}
	return out, nil
}

// writeGitSyncs replaces the gitSyncs array wholesale on the CR. Pass
// an empty slice to clear the field entirely (ww agent git remove).
func writeGitSyncs(cr *unstructured.Unstructured, syncs []map[string]interface{}) error {
	if len(syncs) == 0 {
		unstructured.RemoveNestedField(cr.Object, "spec", "gitSyncs")
		return nil
	}
	asSlice := make([]interface{}, 0, len(syncs))
	for _, s := range syncs {
		asSlice = append(asSlice, s)
	}
	return unstructured.SetNestedSlice(cr.Object, asSlice, "spec", "gitSyncs")
}

// readHarnessGitMappings returns the agent-level gitMappings array
// (harness-scoped), in the same shape readGitSyncs uses. Per-backend
// mappings are read separately via readBackendGitMappings below.
func readHarnessGitMappings(cr *unstructured.Unstructured) ([]map[string]interface{}, error) {
	raw, found, err := unstructured.NestedSlice(cr.Object, "spec", "gitMappings")
	if err != nil {
		return nil, fmt.Errorf("read spec.gitMappings: %w", err)
	}
	if !found {
		return nil, nil
	}
	out := make([]map[string]interface{}, 0, len(raw))
	for i, entry := range raw {
		m, ok := entry.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("spec.gitMappings[%d] is not a map; got %T", i, entry)
		}
		out = append(out, m)
	}
	return out, nil
}

// writeHarnessGitMappings mirrors writeGitSyncs for the harness-scoped
// mappings array.
func writeHarnessGitMappings(cr *unstructured.Unstructured, mappings []map[string]interface{}) error {
	if len(mappings) == 0 {
		unstructured.RemoveNestedField(cr.Object, "spec", "gitMappings")
		return nil
	}
	asSlice := make([]interface{}, 0, len(mappings))
	for _, m := range mappings {
		asSlice = append(asSlice, m)
	}
	return unstructured.SetNestedSlice(cr.Object, asSlice, "spec", "gitMappings")
}

// readBackends returns the agent's backends array as a slice of maps.
// Needed by attach/remove to update per-backend gitMappings atomically
// with the harness-level changes.
func readBackends(cr *unstructured.Unstructured) ([]map[string]interface{}, error) {
	raw, found, err := unstructured.NestedSlice(cr.Object, "spec", "backends")
	if err != nil {
		return nil, fmt.Errorf("read spec.backends: %w", err)
	}
	if !found {
		return nil, fmt.Errorf("spec.backends is unset; agent CR is malformed")
	}
	out := make([]map[string]interface{}, 0, len(raw))
	for i, entry := range raw {
		m, ok := entry.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("spec.backends[%d] is not a map; got %T", i, entry)
		}
		out = append(out, m)
	}
	return out, nil
}

// writeBackends replaces the backends array on the CR.
func writeBackends(cr *unstructured.Unstructured, backends []map[string]interface{}) error {
	asSlice := make([]interface{}, 0, len(backends))
	for _, b := range backends {
		asSlice = append(asSlice, b)
	}
	return unstructured.SetNestedSlice(cr.Object, asSlice, "spec", "backends")
}

// buildHarnessMapping constructs the gitMapping entry for the harness's
// .witwave/ directory. Src is the path in the repo; dest is the fixed
// mount path the harness expects.
func buildHarnessMapping(syncName, repoRoot string) map[string]interface{} {
	return map[string]interface{}{
		"gitSync": syncName,
		"src":     repoRoot + "/.witwave/",
		"dest":    "/home/agent/.witwave/",
	}
}

// buildBackendMapping constructs the gitMapping entry for a specific
// backend's config directory. Mounts the repo's `.<backend>/` into the
// container's `/home/agent/.<backend>/`.
func buildBackendMapping(syncName, repoRoot, backend string) map[string]interface{} {
	return map[string]interface{}{
		"gitSync": syncName,
		"src":     repoRoot + "/." + backend + "/",
		"dest":    "/home/agent/." + backend + "/",
	}
}

// buildGitSyncEntry assembles a gitSyncs[] entry. credentials is nil
// for public repos; otherwise points at a K8s Secret via existingSecret.
func buildGitSyncEntry(syncName, repo, ref, period string, credSecret string) map[string]interface{} {
	entry := map[string]interface{}{
		"name":   syncName,
		"repo":   repo,
		"period": period,
	}
	if ref != "" {
		entry["ref"] = ref
	}
	if credSecret != "" {
		entry["credentials"] = map[string]interface{}{
			"existingSecret": credSecret,
		}
	}
	return entry
}

// renderGitSyncSummary writes a one-paragraph human summary of a
// gitSync entry. Used by `ww agent git list` and the post-add report.
func renderGitSyncSummary(out io.Writer, sync map[string]interface{}) {
	name, _ := sync["name"].(string)
	repo, _ := sync["repo"].(string)
	ref, _ := sync["ref"].(string)
	period, _ := sync["period"].(string)
	fmt.Fprintf(out, "  %s\n", name)
	fmt.Fprintf(out, "    repo:    %s\n", repo)
	if ref != "" {
		fmt.Fprintf(out, "    ref:     %s\n", ref)
	}
	fmt.Fprintf(out, "    period:  %s\n", period)
	if creds, ok := sync["credentials"].(map[string]interface{}); ok {
		if sec, ok := creds["existingSecret"].(string); ok && sec != "" {
			fmt.Fprintf(out, "    secret:  %s\n", sec)
		}
	}
}

// syncEntryByName returns the first gitSync entry whose name matches,
// plus its index in the slice. Returns (-1, nil) when not found.
func syncEntryByName(syncs []map[string]interface{}, name string) (int, map[string]interface{}) {
	for i, s := range syncs {
		if n, _ := s["name"].(string); n == name {
			return i, s
		}
	}
	return -1, nil
}

// filterMappingsByGitSync removes every entry whose gitSync field
// equals `syncName`. Used when detaching — preserves unrelated mappings
// while dropping only the ones tied to the sync we're removing.
func filterMappingsByGitSync(mappings []map[string]interface{}, syncName string) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(mappings))
	for _, m := range mappings {
		if n, _ := m["gitSync"].(string); n == syncName {
			continue
		}
		out = append(out, m)
	}
	return out
}

// newDynamicClient is the shared constructor so verb implementations
// don't each carry the build-client boilerplate.
func newDynamicClient(cfg *rest.Config) (dynamic.Interface, error) {
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("build dynamic client: %w", err)
	}
	return dyn, nil
}
