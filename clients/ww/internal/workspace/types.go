// Package workspace implements the `ww workspace *` subtree: create, list,
// get, status, delete, bind, and unbind WitwaveWorkspace custom resources on a
// target cluster.
//
// Like internal/agent, this package uses a dynamic client with
// unstructured.Unstructured rather than a generated typed client. That
// matches the pattern already established in internal/agent and
// internal/operator and avoids pulling in the operator's v1alpha1 package
// as a cross-module dependency.
package workspace

import (
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/witwave-ai/witwave/clients/ww/internal/agent"
)

// Kubernetes identity of the WitwaveWorkspace resource. Mirrors the GVR declared
// in operator/api/v1alpha1/witwaveworkspace_types.go.
const (
	APIGroup   = agent.APIGroup
	APIVersion = agent.APIVersion
	Kind       = "WitwaveWorkspace"
	Resource   = "witwaveworkspaces"
)

// GVR returns the GroupVersionResource used by every dynamic client call
// in this package.
func GVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: APIGroup, Version: APIVersion, Resource: Resource}
}

// APIVersionString returns the `apiVersion:` field value for CR manifests.
func APIVersionString() string {
	return APIGroup + "/" + APIVersion
}

// Managed-by marker stamped on every CR this package creates so operators
// can tell `ww`-created WitwaveWorkspaces from hand-authored manifests.
const (
	LabelManagedBy   = agent.LabelManagedBy
	LabelManagedByWW = agent.LabelManagedByWW

	// AnnotationCreatedBy records the exact command that minted the CR.
	AnnotationCreatedBy = agent.AnnotationCreatedBy
)

// DefaultWitwaveWorkspaceNamespace is the ww-specific fallback namespace for
// every `ww workspace *` operation when neither --namespace nor the
// kubeconfig context pin one. Same value as the agent subtree
// (DefaultAgentNamespace) so workspaces and the agents that bind them
// land in the same default namespace.
const DefaultWitwaveWorkspaceNamespace = agent.DefaultAgentNamespace
