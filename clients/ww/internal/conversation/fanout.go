package conversation

import (
	"context"
	"fmt"
	"sort"
	"sync"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	"github.com/witwave-ai/witwave/clients/ww/internal/portforward"
)

// witwaveAgentGVR — the GroupVersionResource for WitwaveAgent CRs.
// Repeating the GVR here rather than importing the operator's types
// avoids dragging the operator's full client-runtime dependency tree
// into the CLI for what's a single LIST call.
var witwaveAgentGVR = schema.GroupVersionResource{
	Group:    "witwave.ai",
	Version:  "v1alpha1",
	Resource: "witwaveagents",
}

// AgentTarget is one (namespace, agent) pair to fan out against.
type AgentTarget struct {
	Namespace string
	Agent     string
}

// DiscoverAgents lists WitwaveAgent CRs across the cluster (when ns is
// empty / "" / "all") or in one namespace (when ns is non-empty). The
// caller's RBAC determines which namespaces actually return results;
// a permission error on one namespace surfaces as an empty result for
// that scope rather than a hard failure for the whole call.
func DiscoverAgents(ctx context.Context, cfg *rest.Config, ns string, allNamespaces bool) ([]AgentTarget, error) {
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("build dynamic client: %w", err)
	}
	listNs := ns
	if allNamespaces {
		listNs = ""
	}
	list, err := dyn.Resource(witwaveAgentGVR).Namespace(listNs).List(ctx, metav1.ListOptions{})
	if err != nil {
		// 404 on the GVR itself = the operator hasn't installed CRDs.
		// Surface a friendly hint rather than the raw API error.
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("WitwaveAgent CRD not installed in this cluster — run `ww operator install` first")
		}
		return nil, fmt.Errorf("list WitwaveAgent: %w", err)
	}
	out := make([]AgentTarget, 0, len(list.Items))
	for i := range list.Items {
		item := &list.Items[i]
		out = append(out, AgentTarget{
			Namespace: item.GetNamespace(),
			Agent:     item.GetName(),
		})
	}
	// Stable order so the output is reproducible across runs.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Agent < out[j].Agent
	})
	return out, nil
}

// FanOutResult is one agent's contribution to a multi-agent list.
// On success Entries is populated; on failure Err captures why so the
// caller can render an "unreachable" footer without aborting the
// whole list.
type FanOutResult struct {
	Target  AgentTarget
	Entries []Entry
	Err     error
}

// FanOutList opens a port-forward per target, hits its /conversations,
// and aggregates the results. Each agent runs in its own goroutine; one
// slow or unreachable agent does NOT block the rest. Returns one
// FanOutResult per target (in the same order targets came in) so the
// caller can both render the entries AND surface partial failures.
//
// Token resolution is the caller's job — pass tokenFor(target) to look
// up the right bearer per agent. Pass nil to use no bearer (works only
// when the cluster has CONVERSATIONS_AUTH_DISABLED=true on every agent).
func FanOutList(
	ctx context.Context,
	cfg *rest.Config,
	targets []AgentTarget,
	opts ListOptions,
	tokenFor func(AgentTarget) string,
) []FanOutResult {
	results := make([]FanOutResult, len(targets))
	var wg sync.WaitGroup

	for i, t := range targets {
		i := i
		t := t
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i].Target = t

			fwd, err := portforward.Open(ctx, cfg, t.Namespace, t.Agent)
			if err != nil {
				results[i].Err = err
				return
			}
			defer fwd.Close()

			token := ""
			if tokenFor != nil {
				token = tokenFor(t)
			}
			client := NewClient(fwd.BaseURL, token)
			entries, err := client.List(ctx, opts)
			if err != nil {
				results[i].Err = err
				return
			}
			// Stamp each entry's Agent field if missing — the harness
			// /conversations response is supposed to include it but a
			// defensive backfill keeps cross-agent rendering correct
			// even if a backend version drift omits it.
			for j := range entries {
				if entries[j].Agent == "" {
					entries[j].Agent = t.Agent
				}
			}
			results[i].Entries = entries
		}()
	}
	wg.Wait()
	return results
}

// MergeAndSummarize takes the per-agent results from FanOutList and
// returns a single sorted SessionSummary list ready for table render,
// plus a list of unreachable (target, err) pairs for the partial-
// failure footer. Empty entries (clean agents with no conversation
// history) are skipped silently — they're not errors.
func MergeAndSummarize(results []FanOutResult) (summaries []SessionSummary, unreachable []FanOutResult) {
	var allEntries []Entry
	nsByAgent := make(map[string]string)
	for _, r := range results {
		if r.Err != nil {
			unreachable = append(unreachable, r)
			continue
		}
		for j := range r.Entries {
			// Re-stamp namespace so the summary table knows which
			// scope each row came from. The harness response doesn't
			// include namespace; only the dispatcher does.
			nsByAgent[r.Entries[j].Agent] = r.Target.Namespace
		}
		allEntries = append(allEntries, r.Entries...)
	}

	// Group by (agent, session) into summaries via the existing helper,
	// then re-stamp namespace from the lookup map (Summarize takes a
	// single namespace string, but we may be cross-namespace here).
	summaries = Summarize(allEntries, "")
	for i := range summaries {
		summaries[i].Namespace = nsByAgent[summaries[i].Agent]
	}
	return summaries, unreachable
}
