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

// NyxPromptKind selects which harness scheduler the generated prompt file
// targets. Each kind maps 1:1 to a directory the harness already watches
// (jobs, tasks, triggers, continuations, webhooks) or to the singleton
// heartbeat file.
// +kubebuilder:validation:Enum=job;task;trigger;continuation;webhook;heartbeat
type NyxPromptKind string

const (
	// NyxPromptKindJob renders into /home/agent/.nyx/jobs/<file>.md.
	NyxPromptKindJob NyxPromptKind = "job"

	// NyxPromptKindTask renders into /home/agent/.nyx/tasks/<file>.md.
	NyxPromptKindTask NyxPromptKind = "task"

	// NyxPromptKindTrigger renders into /home/agent/.nyx/triggers/<file>.md.
	NyxPromptKindTrigger NyxPromptKind = "trigger"

	// NyxPromptKindContinuation renders into
	// /home/agent/.nyx/continuations/<file>.md.
	NyxPromptKindContinuation NyxPromptKind = "continuation"

	// NyxPromptKindWebhook renders into /home/agent/.nyx/webhooks/<file>.md.
	NyxPromptKindWebhook NyxPromptKind = "webhook"

	// NyxPromptKindHeartbeat renders into /home/agent/.nyx/HEARTBEAT.md.
	// Because the harness treats HEARTBEAT.md as a singleton, each bound
	// agent can carry at most one NyxPrompt with kind=heartbeat — the
	// admission webhook enforces that invariant so two CRs do not race
	// to overwrite the same file.
	NyxPromptKindHeartbeat NyxPromptKind = "heartbeat"
)

// NyxPromptAgentRef selects a target NyxAgent. In v1alpha1 the only
// selector mode is a direct name reference; namespace defaults to the
// NyxPrompt's own namespace. An optional FilenameSuffix disambiguates
// when the same CR binds to multiple agents and needs a slightly
// different filename per agent (e.g. to avoid colliding with a
// gitSync-managed prompt that happens to share the NyxPrompt's
// default filename on one of the target agents).
type NyxPromptAgentRef struct {
	// Name is the NyxAgent name this prompt binds to.
	// +kubebuilder:validation:Pattern=^[a-z0-9][a-z0-9-]*$
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// FilenameSuffix is appended to the default filename
	// ("nyxprompt-<crname>") for this agent only. Useful when a
	// previously-existing gitSync-managed prompt on one of the bound
	// agents happens to share the default filename; leaving this
	// empty uses the default. Must be DNS-1123 label-safe.
	// +kubebuilder:validation:Pattern=^[a-z0-9][a-z0-9-]*$
	// +optional
	FilenameSuffix string `json:"filenameSuffix,omitempty"`
}

// NyxPromptSpec is the desired state of a NyxPrompt: one prompt definition
// materialised across one or more NyxAgents as a ConfigMap-backed file the
// harness picks up alongside anything gitSync already dropped into the
// same directory.
type NyxPromptSpec struct {
	// Kind selects the target harness scheduler / directory.
	Kind NyxPromptKind `json:"kind"`

	// AgentRefs lists the NyxAgents this prompt binds to. In v1alpha1
	// this is the only selector mode; `agentSelector` and `allAgents`
	// forms are deliberately deferred (#NyxPrompt-v2).
	// +kubebuilder:validation:MinItems=1
	AgentRefs []NyxPromptAgentRef `json:"agentRefs"`

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

// NyxPromptBinding records the result of reconciling a single target
// agent. One entry per AgentRef. Lets `kubectl describe` show which
// agents picked up the prompt and which failed.
type NyxPromptBinding struct {
	// AgentName mirrors spec.agentRefs[i].name.
	AgentName string `json:"agentName"`

	// ConfigMapName is the ConfigMap the operator reconciles for this
	// binding, empty when the target agent does not exist yet.
	// +optional
	ConfigMapName string `json:"configMapName,omitempty"`

	// Filename is the filename materialised inside the pod under
	// .nyx/<kind>/ (or "HEARTBEAT.md" for kind=heartbeat).
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

// NyxPromptStatus is the observed state of a NyxPrompt.
type NyxPromptStatus struct {
	// ObservedGeneration is the spec generation most recently reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Bindings lists one entry per resolved target agent.
	// +optional
	// +listType=map
	// +listMapKey=agentName
	Bindings []NyxPromptBinding `json:"bindings,omitempty"`

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

// Standard condition types for NyxPrompt.
const (
	// NyxPromptConditionReady flips to True when every binding in
	// Status.Bindings has Ready=true and the observed generation
	// matches spec.
	NyxPromptConditionReady = "Ready"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=nyxp
// +kubebuilder:printcolumn:name="Kind",type=string,JSONPath=`.spec.kind`
// +kubebuilder:printcolumn:name="Ready",type=integer,JSONPath=`.status.readyCount`
// +kubebuilder:printcolumn:name="Agents",type=string,JSONPath=`.spec.agentRefs[*].name`,priority=1
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// NyxPrompt is the Schema for the nyxprompts API.
type NyxPrompt struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NyxPromptSpec   `json:"spec,omitempty"`
	Status NyxPromptStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NyxPromptList contains a list of NyxPrompt.
type NyxPromptList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NyxPrompt `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NyxPrompt{}, &NyxPromptList{})
}
