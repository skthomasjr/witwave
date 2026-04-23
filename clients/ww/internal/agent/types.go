// Package agent implements the `ww agent *` subtree: create, list, status,
// and delete WitwaveAgent custom resources on a target cluster.
//
// The package uses a dynamic client with unstructured.Unstructured rather
// than a generated typed client. That choice matches the pattern already
// established in clients/ww/internal/operator (install.go uses
// dynamic.NewForConfig for CRD manipulation) and avoids pulling in the
// operator's v1alpha1 package as a cross-module dependency. We can migrate
// to typed + deepcopy later if the CR structure grows complex enough to
// need compile-time field checks.
package agent

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Kubernetes identity of the WitwaveAgent resource. Mirrors the GVR
// already declared in internal/operator/release.go; kept here as a
// package-local copy so the agent package doesn't reach sideways into
// internal/operator.
const (
	APIGroup   = "witwave.ai"
	APIVersion = "v1alpha1"
	Kind       = "WitwaveAgent"
	Resource   = "witwaveagents"
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
// can tell `ww`-created agents from hand-authored manifests.
const (
	LabelManagedBy   = "app.kubernetes.io/managed-by"
	LabelManagedByWW = "ww"

	// AnnotationCreatedBy records the exact command that minted the CR.
	// Useful for forensics when an unexpected agent appears in `kubectl
	// get wwa`.
	AnnotationCreatedBy = "witwave.ai/created-by"
)
