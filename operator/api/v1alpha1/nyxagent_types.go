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

// PreStopSpec configures a `lifecycle.preStop` sleep hook on every container
// in the agent pod. When enabled, each container sleeps for DelaySeconds
// before SIGTERM arrives, giving in-flight A2A requests, scheduled jobs, and
// webhook deliveries a coordinated drain window (#447). Mirrors the
// `preStop` block in `charts/nyx/values.yaml` (#547, #512). Keep
// DelaySeconds strictly less than Spec.TerminationGracePeriodSeconds so
// SIGTERM has enough remaining grace to complete graceful shutdown before
// SIGKILL.
type PreStopSpec struct {
	// Enabled toggles rendering of the preStop sleep hook. Off by default —
	// opt in for production deployments where graceful shutdown matters.
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// DelaySeconds is the preStop sleep duration. Must be strictly less
	// than the pod's terminationGracePeriodSeconds when Enabled=true.
	// +kubebuilder:default=5
	// +kubebuilder:validation:Minimum=0
	// +optional
	DelaySeconds int32 `json:"delaySeconds,omitempty"`
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

	// Enabled toggles whether this backend's container, PVC, and any
	// per-backend ConfigMap get reconciled. Defaults to true. Mirrors
	// the per-backend `enabled` flag in the Helm chart (#chart beta.32).
	// +kubebuilder:default=true
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

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
	// Enabled toggles whether the entire agent (Deployment, Service, PVCs,
	// ConfigMaps, HPA, PDB, Dashboard) gets reconciled. Defaults to true.
	// Mirrors the per-agent `enabled` flag in the Helm chart (#chart beta.32).
	// +kubebuilder:default=true
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// Port is the HTTP port nyx-harness listens on (Service + probe target).
	// +kubebuilder:default=8000
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +optional
	Port int32 `json:"port,omitempty"`

	// ServiceType controls the agent Service.spec.type. Defaults to ClusterIP.
	// Set to NodePort or LoadBalancer to expose the agent outside the
	// cluster without an Ingress (#chart beta.31 / #466).
	// +kubebuilder:validation:Enum=ClusterIP;NodePort;LoadBalancer
	// +kubebuilder:default=ClusterIP
	// +optional
	ServiceType corev1.ServiceType `json:"serviceType,omitempty"`

	// TerminationGracePeriodSeconds overrides the pod's grace window between
	// SIGTERM and SIGKILL. Defaults to 60s, matching the chart-managed
	// agent pods. Increase for workloads with long-running per-request work
	// (multi-minute jobs, slow webhook deliveries) that need more drain
	// time during voluntary disruption (#458).
	// +kubebuilder:validation:Minimum=0
	// +optional
	TerminationGracePeriodSeconds *int64 `json:"terminationGracePeriodSeconds,omitempty"`

	// PreStop adds a `lifecycle.preStop` sleep hook to every container in
	// the agent pod. Mirrors the chart-level `preStop` block (#547, #512).
	// Off by default; when enabled, PreStop.DelaySeconds must be strictly
	// less than TerminationGracePeriodSeconds (defaulting to 60s) so
	// SIGTERM still fires with enough remaining grace for graceful
	// shutdown before SIGKILL.
	// +optional
	PreStop *PreStopSpec `json:"preStop,omitempty"`

	// Image is the nyx-harness orchestrator image.
	Image ImageSpec `json:"image"`

	// ImagePullSecrets used by the agent pod.
	// +optional
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`

	// ServiceAccountName is the name of a pre-existing ServiceAccount to
	// attach to the agent pod. The operator does not create or manage the
	// ServiceAccount or its RBAC bindings — supply an externally-managed SA
	// that already has the permissions your MCP tools require (e.g. a
	// ServiceAccount bound to a Role that lets `mcp-kubernetes` or
	// `mcp-helm` talk to the Kubernetes API in-cluster).
	//
	// When this field is set, AutomountServiceAccountToken flips to true
	// automatically so the SA token is projected into every container in
	// the pod. Note: the token is visible to every sibling container
	// (nyx-harness + all backend sidecars + any MCP sidecars) — there is
	// no per-container scoping. When unset, the pod retains the hardened
	// default (no SA, no token mounted).
	//
	// See risk #538.
	// +optional
	ServiceAccountName string `json:"serviceAccountName,omitempty"`

	// AutomountServiceAccountToken explicitly controls whether the
	// ServiceAccount token is mounted into the agent pod. When nil (the
	// default), the operator infers the value: false when
	// ServiceAccountName is empty (hardened), true when ServiceAccountName
	// is set (so MCP tools can reach the Kubernetes API). Set this field
	// explicitly only when you need to override the inferred value — e.g.
	// to mount the default SA's token without naming a custom SA, or to
	// keep the token out of a pod that does name a custom SA.
	//
	// See risk #538.
	// +optional
	AutomountServiceAccountToken *bool `json:"automountServiceAccountToken,omitempty"`

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

	// ServiceMonitor optionally creates a monitoring.coreos.com/v1
	// ServiceMonitor for the agent when the Prometheus Operator CRDs
	// are installed (#476). Gated by spec.metrics.enabled and a CRD
	// presence check at reconcile time; no-ops on clusters without
	// prometheus-operator.
	// +optional
	ServiceMonitor *ServiceMonitorSpec `json:"serviceMonitor,omitempty"`

	// Dashboard optionally deploys the Vue 3 dashboard (#470) alongside the
	// agent. The operator renders a per-agent dashboard (one per NyxAgent),
	// independent of the cluster-wide dashboard the Helm chart deploys.
	// Disabled by default.
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

	// ClusterDomain is the cluster DNS zone used to build the in-cluster
	// FQDN the dashboard's nginx proxies to (e.g. `cluster.local`,
	// `cluster.example`). nginx's `resolver` directive does not apply the
	// pod's /etc/resolv.conf search list, so the upstream must be an
	// absolute FQDN; this field lets clusters bootstrapped with a
	// non-default `--service-dns-domain` point the dashboard at the right
	// zone (risk #581). Must match `^[a-z0-9][a-z0-9.-]*[a-z0-9]$`.
	// Defaults to `cluster.local` for back-compat.
	// +kubebuilder:default=cluster.local
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`
	// +optional
	ClusterDomain string `json:"clusterDomain,omitempty"`

	// HarnessURL is retained for CR backward-compatibility but no longer
	// read by the operator. Since beta.46 the dashboard owns cross-agent
	// routing and talks to each agent's service directly via the per-agent
	// nginx config the operator renders; the legacy /api/* catch-all that
	// this field fed is gone. Drop this field from CRs at your leisure; it
	// is ignored either way.
	// Deprecated: no effect since beta.46; remove on next breaking CRD bump.
	// +optional
	HarnessURL string `json:"harnessUrl,omitempty"`

	// Resources for the dashboard container.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
}

// ServiceMonitorSpec configures an optional monitoring.coreos.com/v1
// ServiceMonitor reconciled by the operator when the Prometheus Operator
// CRDs are present on the cluster (#476). Mirrors the chart's
// `serviceMonitor.*` block in charts/nyx/values.yaml so operator-rendered
// agents are scraped the same way chart-rendered agents are.
//
// The reconciler probes for the `monitoring.coreos.com/v1 ServiceMonitor`
// REST mapping at reconcile time and no-ops when the CRD is absent — no
// hard dependency on prometheus-operator Go types is introduced; the
// ServiceMonitor is created via an unstructured client.
type ServiceMonitorSpec struct {
	// Enabled toggles creation of the ServiceMonitor. Requires
	// spec.metrics.enabled=true (no endpoint to scrape otherwise) and the
	// monitoring.coreos.com CRD to be installed. When the CRD is absent
	// the reconciler logs once and no-ops; reconciliation resumes
	// automatically once the CRD appears.
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// Interval between scrapes (e.g. "30s"). Defaults to "30s".
	// +kubebuilder:default="30s"
	// +optional
	Interval string `json:"interval,omitempty"`

	// ScrapeTimeout per scrape (e.g. "10s"). Defaults to "10s".
	// +kubebuilder:default="10s"
	// +optional
	ScrapeTimeout string `json:"scrapeTimeout,omitempty"`

	// Labels merged into the ServiceMonitor's metadata.labels. Useful
	// when the cluster's Prometheus selects ServiceMonitors by a
	// tenancy label (e.g. `release: kube-prometheus-stack`).
	// +optional
	Labels map[string]string `json:"labels,omitempty"`
}

// MetricsSpec toggles Prometheus scrape behaviour for the agent.
type MetricsSpec struct {
	// Master toggle. When false, no scrape annotations are emitted on
	// either the Service or the Pod template, regardless of the
	// granular flags below.
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// ServiceAnnotations stamps prometheus.io/{scrape,port,path}
	// annotations onto the agent Service for Prometheus configs using
	// `kubernetes_sd_configs.role: service`. Defaults to true (preserves
	// historical chart behaviour).
	// +kubebuilder:default=true
	// +optional
	ServiceAnnotations *bool `json:"serviceAnnotations,omitempty"`

	// PodAnnotations stamps the same annotations onto the Pod template
	// for Prometheus configs using `kubernetes_sd_configs.role: pod`
	// (per-replica metrics). Defaults to false — opt in when needed.
	// Mirrors charts/nyx beta.35 / #472.
	// +kubebuilder:default=false
	// +optional
	PodAnnotations *bool `json:"podAnnotations,omitempty"`
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
