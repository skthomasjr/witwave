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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ImageSpec describes a container image used by an agent or backend.
type ImageSpec struct {
	// Repository is the image repository without tag or digest.
	// +kubebuilder:validation:MinLength=1
	Repository string `json:"repository"`

	// Tag is the image tag. If empty, the operator may fill in a default.
	// +optional
	Tag string `json:"tag,omitempty"`

	// PullPolicy controls when the image is pulled.
	// +kubebuilder:validation:Enum=Always;IfNotPresent;Never
	// +optional
	PullPolicy corev1.PullPolicy `json:"pullPolicy,omitempty"`
}

// ConfigFile represents a single inline config file mounted into a container.
// The file is materialised as one key in a ConfigMap owned by the NyxAgent.
type ConfigFile struct {
	// Name is the ConfigMap key and the filename inside the container.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// MountPath is the absolute path inside the container.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^/.*`
	MountPath string `json:"mountPath"`

	// Content is the literal file contents.
	// +kubebuilder:validation:MinLength=1
	Content string `json:"content"`
}

// ProbeSpec describes a single probe's timing parameters.
// Defaults match the Helm chart's probes.* defaults.
type ProbeSpec struct {
	// +kubebuilder:default=10
	// +optional
	InitialDelaySeconds int32 `json:"initialDelaySeconds,omitempty"`

	// +kubebuilder:default=30
	// +optional
	PeriodSeconds int32 `json:"periodSeconds,omitempty"`

	// +kubebuilder:default=5
	// +optional
	TimeoutSeconds int32 `json:"timeoutSeconds,omitempty"`

	// +kubebuilder:default=3
	// +optional
	FailureThreshold int32 `json:"failureThreshold,omitempty"`
}

// ProbesSpec bundles liveness and readiness probe timings.
type ProbesSpec struct {
	// +optional
	Liveness *ProbeSpec `json:"liveness,omitempty"`

	// +optional
	Readiness *ProbeSpec `json:"readiness,omitempty"`
}

// AutoscalingSpec controls optional HorizontalPodAutoscaler creation.
type AutoscalingSpec struct {
	// Enabled creates an HPA for the agent's Deployment.
	// When true, the Deployment's replicas field is omitted so the HPA owns it.
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// +kubebuilder:default=1
	// +optional
	MinReplicas int32 `json:"minReplicas,omitempty"`

	// +kubebuilder:default=3
	// +optional
	MaxReplicas int32 `json:"maxReplicas,omitempty"`

	// +optional
	TargetCPUUtilizationPercentage *int32 `json:"targetCPUUtilizationPercentage,omitempty"`

	// +optional
	TargetMemoryUtilizationPercentage *int32 `json:"targetMemoryUtilizationPercentage,omitempty"`
}

// PodDisruptionBudgetSpec controls optional PodDisruptionBudget creation.
// Exactly one of MinAvailable or MaxUnavailable should be set.
type PodDisruptionBudgetSpec struct {
	// Enabled creates a PodDisruptionBudget for the agent's Deployment.
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// +optional
	MinAvailable *int32 `json:"minAvailable,omitempty"`

	// +optional
	MaxUnavailable *int32 `json:"maxUnavailable,omitempty"`
}

// BackendStorageMount maps a sub-path of a backend's PVC to a mount path.
type BackendStorageMount struct {
	// +kubebuilder:validation:MinLength=1
	SubPath string `json:"subPath"`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^/.*`
	MountPath string `json:"mountPath"`
}

// BackendStorageSpec configures optional persistent storage for a backend.
// Either ExistingClaim (reference a pre-existing PVC) or a new PVC created by
// the operator is used; when Enabled=true and ExistingClaim is empty the
// operator creates a PVC named <agent>-<backend>-data.
type BackendStorageSpec struct {
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// +optional
	Size string `json:"size,omitempty"`

	// +optional
	StorageClassName string `json:"storageClassName,omitempty"`

	// +optional
	ExistingClaim string `json:"existingClaim,omitempty"`

	// +optional
	Mounts []BackendStorageMount `json:"mounts,omitempty"`
}

// BackendSpec defines one backend sidecar container.
type BackendSpec struct {
	// Name identifies the backend (e.g. claude, codex, gemini). Used as the
	// container name and the backend ID in routing.
	// +kubebuilder:validation:Pattern=^[a-z0-9][a-z0-9-]*$
	Name string `json:"name"`

	// Image is the backend container image.
	Image ImageSpec `json:"image"`

	// Port is the HTTP port the backend listens on inside the pod.
	// +kubebuilder:default=8080
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +optional
	Port int32 `json:"port,omitempty"`

	// Model is the default model string passed to the backend.
	// +optional
	Model string `json:"model,omitempty"`

	// Resources are the CPU/memory requests and limits for this backend.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// Env adds environment variables directly.
	// +optional
	Env []corev1.EnvVar `json:"env,omitempty"`

	// EnvFrom sources env vars from Secrets or ConfigMaps.
	// +optional
	EnvFrom []corev1.EnvFromSource `json:"envFrom,omitempty"`

	// Config mounts inline config files into the backend container.
	// +optional
	Config []ConfigFile `json:"config,omitempty"`

	// Storage provisions or references a PVC for backend persistence.
	// +optional
	Storage *BackendStorageSpec `json:"storage,omitempty"`
}

// SharedStorageRef references a pre-existing PVC to mount into all containers
// of the agent pod. The operator does not create this PVC — it must exist
// before the NyxAgent is reconciled.
type SharedStorageRef struct {
	// ClaimName is the name of the existing PersistentVolumeClaim.
	// +kubebuilder:validation:MinLength=1
	ClaimName string `json:"claimName"`

	// MountPath is the absolute path inside each container.
	// +kubebuilder:default=/data/shared
	// +optional
	MountPath string `json:"mountPath,omitempty"`
}

// NyxAgentSpec defines the desired state of NyxAgent.
type NyxAgentSpec struct {
	// Port is the HTTP port nyx-harness listens on (Service + probe target).
	// +kubebuilder:default=8000
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +optional
	Port int32 `json:"port,omitempty"`

	// TerminationGracePeriodSeconds overrides the pod's grace window between
	// SIGTERM and SIGKILL. Defaults to 60s, matching the chart-managed
	// agent pods. Increase for workloads with long-running per-request work
	// (multi-minute jobs, slow webhook deliveries) that need more drain
	// time during voluntary disruption (#458).
	// +kubebuilder:validation:Minimum=0
	// +optional
	TerminationGracePeriodSeconds *int64 `json:"terminationGracePeriodSeconds,omitempty"`

	// Image is the nyx-harness orchestrator image.
	Image ImageSpec `json:"image"`

	// ImagePullSecrets used by the agent pod.
	// +optional
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`

	// Resources are CPU/memory requests and limits for the nyx-harness container.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// Env adds environment variables to the nyx-harness container.
	// +optional
	Env []corev1.EnvVar `json:"env,omitempty"`

	// EnvFrom sources env vars from Secrets or ConfigMaps for the
	// nyx-harness container.
	// +optional
	EnvFrom []corev1.EnvFromSource `json:"envFrom,omitempty"`

	// Metrics toggles Prometheus scrape annotations on the Service.
	// +optional
	Metrics MetricsSpec `json:"metrics,omitempty"`

	// Config mounts inline config files into the nyx-harness container.
	// +optional
	Config []ConfigFile `json:"config,omitempty"`

	// Probes overrides liveness/readiness probe timing.
	// +optional
	Probes *ProbesSpec `json:"probes,omitempty"`

	// Autoscaling optionally creates an HPA for the agent Deployment.
	// +optional
	Autoscaling *AutoscalingSpec `json:"autoscaling,omitempty"`

	// PodDisruptionBudget optionally creates a PDB for the agent Deployment.
	// +optional
	PodDisruptionBudget *PodDisruptionBudgetSpec `json:"podDisruptionBudget,omitempty"`

	// Backends lists the backend sidecars (a2-claude, a2-codex, a2-gemini, …).
	// +kubebuilder:validation:MinItems=1
	Backends []BackendSpec `json:"backends"`

	// SharedStorage optionally mounts a pre-existing PVC into every container.
	// +optional
	SharedStorage *SharedStorageRef `json:"sharedStorage,omitempty"`

	// Dashboard optionally deploys the Vue 3 dashboard (#470) alongside the
	// agent. The dashboard is the future replacement for the existing `ui`
	// surface; both run side-by-side until dashboard reaches feature parity
	// with `ui/`. Disabled by default — the current UI remains primary until
	// explicitly flipped.
	// +optional
	Dashboard *DashboardSpec `json:"dashboard,omitempty"`
}

// DashboardSpec configures an optional dashboard Deployment + Service per
// agent. The operator renders one Deployment and one Service scoped to the
// NyxAgent, so an agent can have its own dashboard instance independent of
// any release-level dashboard deployed via Helm (#470).
type DashboardSpec struct {
	// Enabled toggles creation of the dashboard Deployment + Service.
	Enabled bool `json:"enabled"`

	// Image is the dashboard container image.
	// +optional
	Image *ImageSpec `json:"image,omitempty"`

	// Replicas for the dashboard Deployment. Defaults to 1.
	// +kubebuilder:validation:Minimum=0
	// +optional
	Replicas *int32 `json:"replicas,omitempty"`

	// Port is the Service port (the container always listens on 8080).
	// +kubebuilder:default=80
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +optional
	Port int32 `json:"port,omitempty"`

	// HarnessURL overrides the origin the dashboard's nginx /api/* proxy
	// forwards to. Defaults to "http://<agent>-harness:<port>" derived from
	// the parent NyxAgent.
	// +optional
	HarnessURL string `json:"harnessUrl,omitempty"`

	// Resources for the dashboard container.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
}

// MetricsSpec toggles Prometheus scrape behaviour for the agent Service.
type MetricsSpec struct {
	// +optional
	Enabled bool `json:"enabled,omitempty"`
}

// NyxAgentPhase is a coarse-grained lifecycle phase for display purposes.
// +kubebuilder:validation:Enum=Pending;Ready;Degraded;Error
type NyxAgentPhase string

const (
	NyxAgentPhasePending  NyxAgentPhase = "Pending"
	NyxAgentPhaseReady    NyxAgentPhase = "Ready"
	NyxAgentPhaseDegraded NyxAgentPhase = "Degraded"
	NyxAgentPhaseError    NyxAgentPhase = "Error"
)

// NyxAgentStatus defines the observed state of NyxAgent.
type NyxAgentStatus struct {
	// Phase is a coarse-grained lifecycle indicator.
	// +optional
	Phase NyxAgentPhase `json:"phase,omitempty"`

	// ObservedGeneration is the generation of the spec most recently reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// ReadyReplicas mirrors the agent Deployment's status.readyReplicas.
	// +optional
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`

	// Conditions follow the standard Kubernetes condition convention.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// Standard condition types for NyxAgent.
const (
	ConditionAvailable        = "Available"
	ConditionProgressing      = "Progressing"
	ConditionReconcileSuccess = "ReconcileSuccess"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=nyxa
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Ready",type=integer,JSONPath=`.status.readyReplicas`
// +kubebuilder:printcolumn:name="Backends",type=string,JSONPath=`.spec.backends[*].name`,priority=1
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// NyxAgent is the Schema for the nyxagents API.
type NyxAgent struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NyxAgentSpec   `json:"spec,omitempty"`
	Status NyxAgentStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NyxAgentList contains a list of NyxAgent.
type NyxAgentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NyxAgent `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NyxAgent{}, &NyxAgentList{})
}
