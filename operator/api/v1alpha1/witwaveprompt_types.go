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
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// WitwavePromptKind selects which harness scheduler the generated prompt file
// targets. Each kind maps 1:1 to a directory the harness already watches
// (jobs, tasks, triggers, continuations, webhooks) or to the singleton
// heartbeat file.
// +kubebuilder:validation:Enum=job;task;trigger;continuation;webhook;heartbeat
type WitwavePromptKind string

const (
	// WitwavePromptKindJob renders into /home/agent/.witwave/jobs/<file>.md.
	WitwavePromptKindJob WitwavePromptKind = "job"

	// WitwavePromptKindTask renders into /home/agent/.witwave/tasks/<file>.md.
	WitwavePromptKindTask WitwavePromptKind = "task"

	// WitwavePromptKindTrigger renders into /home/agent/.witwave/triggers/<file>.md.
	WitwavePromptKindTrigger WitwavePromptKind = "trigger"

	// WitwavePromptKindContinuation renders into
	// /home/agent/.witwave/continuations/<file>.md.
	WitwavePromptKindContinuation WitwavePromptKind = "continuation"

	// WitwavePromptKindWebhook renders into /home/agent/.witwave/webhooks/<file>.md.
	WitwavePromptKindWebhook WitwavePromptKind = "webhook"

	// WitwavePromptKindHeartbeat renders into /home/agent/.witwave/HEARTBEAT.md.
	// Because the harness treats HEARTBEAT.md as a singleton, each bound
	// agent can carry at most one WitwavePrompt with kind=heartbeat — the
	// admission webhook enforces that invariant so two CRs do not race
	// to overwrite the same file.
	WitwavePromptKindHeartbeat WitwavePromptKind = "heartbeat"
)

// WitwavePromptAgentRef selects a target WitwaveAgent. In v1alpha1 the only
// selector mode is a direct name reference; namespace defaults to the
// WitwavePrompt's own namespace. An optional FilenameSuffix disambiguates
// when the same CR binds to multiple agents and needs a slightly
// different filename per agent (e.g. to avoid colliding with a
// gitSync-managed prompt that happens to share the WitwavePrompt's
// default filename on one of the target agents).
type WitwavePromptAgentRef struct {
	// Name is the WitwaveAgent name this prompt binds to.
	// +kubebuilder:validation:Pattern=^[a-z0-9][a-z0-9-]*$
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// FilenameSuffix is appended to the default filename
	// ("witwaveprompt-<crname>") for this agent only. Useful when a
	// previously-existing gitSync-managed prompt on one of the bound
	// agents happens to share the default filename; leaving this
	// empty uses the default. Must be DNS-1123 label-safe.
	// +kubebuilder:validation:Pattern=^[a-z0-9][a-z0-9-]*$
	// +optional
	FilenameSuffix string `json:"filenameSuffix,omitempty"`
}

// WitwavePromptSpec is the desired state of a WitwavePrompt: one prompt definition
// materialised across one or more WitwaveAgents as a ConfigMap-backed file the
// harness picks up alongside anything gitSync already dropped into the
// same directory.
type WitwavePromptSpec struct {
	// Kind selects the target harness scheduler / directory.
	Kind WitwavePromptKind `json:"kind"`

	// AgentRefs lists the WitwaveAgents this prompt binds to. In v1alpha1
	// this is the only selector mode; `agentSelector` and `allAgents`
	// forms are deliberately deferred (#WitwavePrompt-v2).
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=100
	AgentRefs []WitwavePromptAgentRef `json:"agentRefs"`

	// Frontmatter is the YAML frontmatter block the harness will see at
	// the top of the rendered .md file. Stored as raw JSON so the webhook
	// can enforce kind-specific invariants (job/task need `schedule`,
	// trigger needs `endpoint`, continuation needs `continues-after`,
	// webhook needs `url`) without this CRD needing a new field per
	// kind. Keys are emitted verbatim as YAML when the ConfigMap is
	// rendered — users write the frontmatter the same way they would
	// in a .md file checked into git.
	// +kubebuilder:pruning:PreserveUnknownFields
	// +optional
	Frontmatter *apiextensionsv1.JSON `json:"frontmatter,omitempty"`

	// Body is the prompt text that appears after the frontmatter. Empty
	// is allowed for kinds (like webhook) where frontmatter alone is
	// meaningful. The harness trims trailing whitespace on read, so
	// leading/trailing blank lines here are cosmetic.
	// +optional
	Body string `json:"body,omitempty"`
}

// WitwavePromptBinding records the result of reconciling a single target
// agent. One entry per AgentRef. Lets `kubectl describe` show which
// agents picked up the prompt and which failed.
type WitwavePromptBinding struct {
	// AgentName mirrors spec.agentRefs[i].name.
	AgentName string `json:"agentName"`

	// ConfigMapName is the ConfigMap the operator reconciles for this
	// binding, empty when the target agent does not exist yet.
	// +optional
	ConfigMapName string `json:"configMapName,omitempty"`

	// Filename is the filename materialised inside the pod under
	// .witwave/<kind>/ (or "HEARTBEAT.md" for kind=heartbeat).
	// +optional
	Filename string `json:"filename,omitempty"`

	// Ready is true when the ConfigMap for this binding has been
	// applied successfully in the latest reconcile.
	// +optional
	Ready bool `json:"ready,omitempty"`

	// Message carries a short human-readable error when Ready=false.
	// +optional
	Message string `json:"message,omitempty"`
}

// WitwavePromptStatus is the observed state of a WitwavePrompt.
type WitwavePromptStatus struct {
	// ObservedGeneration is the spec generation most recently reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Bindings lists one entry per resolved target agent.
	// +optional
	// +listType=map
	// +listMapKey=agentName
	Bindings []WitwavePromptBinding `json:"bindings,omitempty"`

	// ReadyCount is the number of bindings whose ConfigMap was applied
	// successfully in the latest reconcile.
	// +optional
	ReadyCount int32 `json:"readyCount,omitempty"`

	// Conditions follow the standard Kubernetes condition convention.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// Standard condition types for WitwavePrompt.
const (
	// WitwavePromptConditionReady flips to True when every binding in
	// Status.Bindings has Ready=true and the observed generation
	// matches spec.
	WitwavePromptConditionReady = "Ready"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=wwp
// The helm.sh/resource-policy=keep annotation below MUST NOT be removed by
// automated regeneration (e.g. `make manifests`). It instructs Helm to retain
// this CRD on `helm uninstall`, preventing accidental deletion of every
// WitwavePrompt CR in the cluster. See #1647 (the bug that prompted adding it)
// and #1614 (the operator install/uninstall lifecycle work it complements).
// controller-gen preserves existing metadata.annotations on regeneration, and
// this marker re-injects the annotation if the CRD file is rebuilt from scratch.
// +kubebuilder:metadata:annotations="helm.sh/resource-policy=keep"
// +kubebuilder:printcolumn:name="Kind",type=string,JSONPath=`.spec.kind`
// +kubebuilder:printcolumn:name="Ready",type=integer,JSONPath=`.status.readyCount`
// +kubebuilder:printcolumn:name="Agents",type=string,JSONPath=`.spec.agentRefs[*].name`,priority=1
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// WitwavePrompt is the Schema for the witwaveprompts API.
type WitwavePrompt struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   WitwavePromptSpec   `json:"spec,omitempty"`
	Status WitwavePromptStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// WitwavePromptList contains a list of WitwavePrompt.
type WitwavePromptList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []WitwavePrompt `json:"items"`
}

func init() {
	SchemeBuilder.Register(&WitwavePrompt{}, &WitwavePromptList{})
}
