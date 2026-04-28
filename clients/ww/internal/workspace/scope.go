package workspace

import (
	"github.com/witwave-ai/witwave/clients/ww/internal/agent"
)

// NamespaceSource identifies where a resolved namespace came from. Used
// by callers that log the resolution so the message can distinguish
// "we picked this from your kubeconfig context" from "we fell back to
// the ww default because nothing else was configured."
//
// Reuses the type set defined in internal/agent so command-side code
// can switch on a single set of constants regardless of which subtree
// produced the resolution. See DESIGN.md NS-1, NS-2.
type NamespaceSource = agent.NamespaceSource

const (
	// NamespaceFromFlag means the user passed --namespace / -n.
	NamespaceFromFlag = agent.NamespaceFromFlag
	// NamespaceFromContext means the kubeconfig context pinned a namespace.
	NamespaceFromContext = agent.NamespaceFromContext
	// NamespaceFromDefault means nothing was pinned; ww fell back to
	// DefaultWitwaveWorkspaceNamespace.
	NamespaceFromDefault = agent.NamespaceFromDefault
)

// ResolveNamespace picks the namespace for a `ww workspace` operation.
// Precedence: explicit flag → context's configured namespace →
// DefaultWitwaveWorkspaceNamespace. Callers MUST log the resolved value so the
// user sees where an unnamed invocation landed (DESIGN.md NS-2).
func ResolveNamespace(flagValue, contextNS string) string {
	ns, _ := ResolveNamespaceWithSource(flagValue, contextNS)
	return ns
}

// ResolveNamespaceWithSource mirrors ResolveNamespace but additionally
// returns the source of the resolved value — so log lines can read
// "(from kubeconfig context)" vs "(ww default)" accurately.
func ResolveNamespaceWithSource(flagValue, contextNS string) (string, NamespaceSource) {
	if flagValue != "" {
		return flagValue, NamespaceFromFlag
	}
	if contextNS != "" {
		return contextNS, NamespaceFromContext
	}
	return DefaultWitwaveWorkspaceNamespace, NamespaceFromDefault
}
