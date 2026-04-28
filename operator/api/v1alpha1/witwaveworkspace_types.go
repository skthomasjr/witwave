/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// WitwaveWorkspaceStorageType selects the VolumeSource for a workspace volume.
// In v1alpha1 only `pvc` is honoured by the controller; `hostPath` is a
// reserved name accepted by the structural schema but rejected at admission
// to keep the field's enum stable for a future v1.x without a CRD bump
// (see tmp/workspace-crd.md "Out of scope for v1").
// +kubebuilder:validation:Enum=pvc;hostPath
type WitwaveWorkspaceStorageType string

const (
	// WitwaveWorkspaceStorageTypePVC selects a PersistentVolumeClaim volume source.
	// The controller reconciles a PVC named `<workspace>-vol-<volume.name>`
	// in the workspace's namespace.
	WitwaveWorkspaceStorageTypePVC WitwaveWorkspaceStorageType = "pvc"

	// WitwaveWorkspaceStorageTypeHostPath is reserved for a future v1.x and is
	// rejected by the admission webhook today. Operators that need a
	// single-node hostPath fallback should use the per-agent SharedStorage
	// surface in the meantime.
	WitwaveWorkspaceStorageTypeHostPath WitwaveWorkspaceStorageType = "hostPath"
)

// WitwaveWorkspaceVolumeReclaimPolicy controls what happens to the operator-created
// PVC when its parent WitwaveWorkspace is deleted (and the refuse-delete finalizer
// is finally cleared because no agent references the workspace any more).
// +kubebuilder:validation:Enum=Delete;Retain
type WitwaveWorkspaceVolumeReclaimPolicy string

const (
	// WitwaveWorkspaceVolumeReclaimPolicyDelete removes the PVC alongside the
	// WitwaveWorkspace. The default — matches the structural-schema default and
	// keeps cluster state tidy when a workspace is genuinely retired.
	WitwaveWorkspaceVolumeReclaimPolicyDelete WitwaveWorkspaceVolumeReclaimPolicy = "Delete"

	// WitwaveWorkspaceVolumeReclaimPolicyRetain leaves the PVC behind so the
	// underlying volume's data survives a WitwaveWorkspace recreate. Recommended
	// for stateful volumes (memory, datasets) where the design doc
	// explicitly calls out "don't lose accumulated state on WitwaveWorkspace
	// recreate".
	WitwaveWorkspaceVolumeReclaimPolicyRetain WitwaveWorkspaceVolumeReclaimPolicy = "Retain"
)

// WitwaveWorkspaceVolume describes one shared volume stamped onto every agent that
// references the parent WitwaveWorkspace. Mirrors the shape of `SharedStorageSpec`
// (per-agent) but lives at workspace scope so multiple agents can converge
// on the same PVC.
type WitwaveWorkspaceVolume struct {
	// Name is the workspace-local volume identifier. Used to derive the PVC
	// name (`<workspace>-vol-<name>`) and the per-agent volumeMount name
	// (`workspace-<workspace>-<name>`). DNS-1123 label-safe so the rendered
	// resource names stay valid.
	// +kubebuilder:validation:Pattern=^[a-z0-9][a-z0-9-]*$
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Name string `json:"name"`

	// StorageType selects the VolumeSource. Only `pvc` is honoured in
	// v1alpha1; the webhook rejects `hostPath`.
	// +kubebuilder:default=pvc
	// +optional
	StorageType WitwaveWorkspaceStorageType `json:"storageType,omitempty"`

	// Size is the PVC storage request (e.g. `50Gi`). Required for
	// StorageType=pvc when the operator creates the PVC. Stored as a
	// resource.Quantity so the structural schema rejects malformed values
	// before they reach the controller.
	// +optional
	Size *resource.Quantity `json:"size,omitempty"`

	// AccessMode is the PVC access mode. Defaults to ReadWriteMany — the
	// cross-node-safe choice that lets any number of agent pods, scheduled
	// to any nodes, mount the same volume concurrently. Operators on
	// single-node clusters (Docker Desktop, single-node k3s, etc.) whose
	// default storage class has no RWM option can set this to ReadWriteOnce
	// instead; all binding agent pods must then land on the same node.
	// ReadWriteOncePod is also accepted (single-pod single-node), though
	// it precludes more than one agent binding the workspace.
	// +kubebuilder:default=ReadWriteMany
	// +kubebuilder:validation:Enum=ReadWriteMany;ReadWriteOnce;ReadWriteOncePod
	// +optional
	AccessMode corev1.PersistentVolumeAccessMode `json:"accessMode,omitempty"`

	// StorageClassName is the storage class for the operator-created PVC.
	// When nil the cluster default storage class is used.
	// +optional
	StorageClassName *string `json:"storageClassName,omitempty"`

	// MountPath is the absolute path inside each participating agent's
	// containers. When empty the controller derives
	// `/workspaces/<workspace>/<volume.name>` so cross-agent paths line up
	// without operator-supplied glue.
	// +kubebuilder:validation:Pattern=`^(|/.*)$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// ReclaimPolicy controls PVC cleanup on WitwaveWorkspace deletion. Defaults
	// to Delete; flip to Retain for stateful volumes whose data must
	// survive a WitwaveWorkspace recreate.
	// +kubebuilder:default=Delete
	// +optional
	ReclaimPolicy WitwaveWorkspaceVolumeReclaimPolicy `json:"reclaimPolicy,omitempty"`
}

// WitwaveWorkspaceSecret declares a workspace-scoped Secret reference. The operator
// stays out of the secrets-write trust boundary by accepting only references
// to pre-created Secrets — no inline data field exists by design (see
// tmp/workspace-crd.md "Soft-leaning decisions baked in").
type WitwaveWorkspaceSecret struct {
	// Name references an existing Secret in the WitwaveWorkspace's namespace.
	// +kubebuilder:validation:Pattern=^[a-z0-9][a-z0-9.-]*$
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// MountPath, when non-empty, mounts the Secret as a directory of files
	// at the given absolute path. Each Secret key becomes one file. Mutually
	// exclusive with EnvFrom — admission rejects entries that set both.
	// +kubebuilder:validation:Pattern=`^(|/.*)$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// EnvFrom, when true, projects the Secret's keys as environment
	// variables on every backend container of every participating agent.
	// Mutually exclusive with MountPath.
	// +optional
	EnvFrom bool `json:"envFrom,omitempty"`
}

// WitwaveWorkspaceInlineFile carries inline content the operator renders into a
// project-owned ConfigMap. The dual-check IsControlledBy + label pattern
// keeps the operator from ever touching a ConfigMap a user authored by
// hand under the same name.
type WitwaveWorkspaceInlineFile struct {
	// Name is the ConfigMap data key (and conventional filename).
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Path is the filename inside the rendered ConfigMap (typically the
	// same as Name). Materialised as one ConfigMap key.
	// +kubebuilder:validation:MinLength=1
	Path string `json:"path"`

	// Content is the literal file contents. Stamped verbatim into the
	// ConfigMap data — no templating, no validation, no expansion.
	Content string `json:"content"`
}

// WitwaveWorkspaceConfigFile describes one configuration file mount stamped onto
// every participating agent. Exactly one of ConfigMap or Inline must be
// set; the webhook rejects entries that violate the invariant.
type WitwaveWorkspaceConfigFile struct {
	// ConfigMap references a pre-created ConfigMap by name. When set, the
	// operator does not own the ConfigMap and never reconciles its data —
	// it is only mounted into the participating agents.
	// +kubebuilder:validation:Pattern=^[a-z0-9][a-z0-9.-]*$
	// +optional
	ConfigMap string `json:"configMap,omitempty"`

	// Inline carries operator-rendered content. When set the operator
	// reconciles a ConfigMap owned by the WitwaveWorkspace via IsControlledBy +
	// label dual-check.
	// +optional
	Inline *WitwaveWorkspaceInlineFile `json:"inline,omitempty"`

	// MountPath is the absolute path inside each participating agent's
	// containers where this file is mounted.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^/.*`
	MountPath string `json:"mountPath"`

	// SubPath optionally restricts the mount to a single key inside the
	// referenced ConfigMap (matches Kubernetes' standard volumeMount
	// SubPath semantics). Use it when MountPath points at a single file
	// rather than a directory.
	// +optional
	SubPath string `json:"subPath,omitempty"`
}

// WitwaveWorkspaceSpec is the desired state of a WitwaveWorkspace.
type WitwaveWorkspaceSpec struct {
	// Volumes lists the shared volumes provisioned for this workspace. Each
	// entry produces one PVC the operator stamps onto every participating
	// agent's pods.
	// +optional
	// +kubebuilder:validation:MaxItems=20
	Volumes []WitwaveWorkspaceVolume `json:"volumes,omitempty"`

	// Secrets lists pre-created Secret references projected onto every
	// participating agent. The operator never writes to these Secrets —
	// secrets-write RBAC is intentionally out of the workspace controller's
	// trust boundary.
	// +optional
	// +kubebuilder:validation:MaxItems=50
	Secrets []WitwaveWorkspaceSecret `json:"secrets,omitempty"`

	// ConfigFiles lists ConfigMap-backed files mounted onto every
	// participating agent. Each entry references either a pre-created
	// ConfigMap or carries inline content the operator renders into an
	// owned ConfigMap.
	// +optional
	// +kubebuilder:validation:MaxItems=50
	ConfigFiles []WitwaveWorkspaceConfigFile `json:"configFiles,omitempty"`
}

// WitwaveWorkspaceBoundAgent records one WitwaveAgent that currently references this
// WitwaveWorkspace via Spec.WorkspaceRefs. The list is maintained by the workspace
// controller as an inverted index — agents are the source of truth.
type WitwaveWorkspaceBoundAgent struct {
	// Name of the bound WitwaveAgent.
	Name string `json:"name"`

	// Namespace of the bound WitwaveAgent. Recorded explicitly even though
	// in v1alpha1 the controller only matches same-namespace agents — the
	// field documents the assumption and keeps the door open for a future
	// cross-namespace shape without status-format churn.
	Namespace string `json:"namespace"`
}

// WitwaveWorkspaceStatus is the observed state of a WitwaveWorkspace.
type WitwaveWorkspaceStatus struct {
	// ObservedGeneration is the spec generation most recently reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// BoundAgents is the operator-maintained inverted index of WitwaveAgents
	// that currently reference this workspace via Spec.WorkspaceRefs.
	// +optional
	// +listType=map
	// +listMapKey=name
	BoundAgents []WitwaveWorkspaceBoundAgent `json:"boundAgents,omitempty"`

	// Conditions follow the standard Kubernetes condition convention.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// Standard condition types for WitwaveWorkspace.
const (
	// WitwaveWorkspaceConditionReady flips True when every desired side-effect of
	// the spec has been reconciled (volumes, configFiles) and the bound-
	// agents index is up to date.
	WitwaveWorkspaceConditionReady = "Ready"

	// WitwaveWorkspaceConditionVolumesProvisioned reports the state of PVC
	// provisioning. Decoupled from Ready so dashboards can attribute a
	// not-ready WitwaveWorkspace to a specific reconcile concern.
	WitwaveWorkspaceConditionVolumesProvisioned = "VolumesProvisioned"

	// WitwaveWorkspaceConditionBoundAgentsTracked reports the state of the
	// inverted-index update.
	WitwaveWorkspaceConditionBoundAgentsTracked = "BoundAgentsTracked"

	// WitwaveWorkspaceConditionConfigMapsRendered reports the state of inline
	// ConfigMap reconciliation.
	WitwaveWorkspaceConditionConfigMapsRendered = "ConfigMapsRendered"

	// WitwaveWorkspaceConditionDeletionBlocked is set on a workspace whose
	// deletion is being blocked by the refuse-delete finalizer because
	// at least one agent still references it.
	WitwaveWorkspaceConditionDeletionBlocked = "DeletionBlocked"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=wws
// The helm.sh/resource-policy=keep annotation prevents accidental deletion
// of every WitwaveWorkspace CR in the cluster on `helm uninstall`. Mirrors the
// WitwaveAgent and WitwavePrompt CRDs (#1647).
// +kubebuilder:metadata:annotations="helm.sh/resource-policy=keep"
// +kubebuilder:printcolumn:name="Volumes",type=integer,JSONPath=`.spec.volumes[*]`,priority=1
// +kubebuilder:printcolumn:name="Bound",type=integer,JSONPath=`.status.boundAgents[*]`,priority=1
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// WitwaveWorkspace is the Schema for the witwaveworkspaces API. A WitwaveWorkspace provisions
// shared volumes, projects pre-created Secrets, and renders ConfigMap-backed
// files that the operator stamps onto every WitwaveAgent whose
// Spec.WorkspaceRefs references it.
type WitwaveWorkspace struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   WitwaveWorkspaceSpec   `json:"spec,omitempty"`
	Status WitwaveWorkspaceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// WitwaveWorkspaceList contains a list of WitwaveWorkspace.
type WitwaveWorkspaceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []WitwaveWorkspace `json:"items"`
}

func init() {
	SchemeBuilder.Register(&WitwaveWorkspace{}, &WitwaveWorkspaceList{})
}
