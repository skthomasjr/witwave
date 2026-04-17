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

	// AccessModes is the list of access modes for the operator-created
	// backend PVC. Defaults to [ReadWriteOnce] when empty for backward
	// compatibility with single-replica deployments. Set to [ReadWriteMany]
	// when running HPA-scaled backends on RWX-capable storage, or
	// [ReadWriteOncePod] (K8s 1.27+) for stricter single-pod isolation.
	// Ignored when ExistingClaim is set — the pre-existing PVC's access
	// modes are honoured as-is.
	// +optional
	AccessModes []corev1.PersistentVolumeAccessMode `json:"accessModes,omitempty"`

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

	// EnvFrom sources env vars from Secrets or ConfigMaps. When
	// Credentials is set to a resolvable value, the resolved Secret is
	// prepended to this list at render time so legacy `envFrom` shapes
	// keep working alongside the resolver.
	// +optional
	EnvFrom []corev1.EnvFromSource `json:"envFrom,omitempty"`

	// Credentials is the unified dev/prod credentials resolver for
	// this backend (#nyx.resolveCredentials parity). Three modes:
	// ExistingSecret (production), inline Secrets map with
	// AcknowledgeInsecureInline=true (dev, operator reconciles a
	// Secret keyed by the map), or empty (legacy EnvFrom passthrough).
	// +optional
	Credentials *BackendCredentialsSpec `json:"credentials,omitempty"`

	// Config mounts inline config files into the backend container.
	// +optional
	Config []ConfigFile `json:"config,omitempty"`

	// Storage provisions or references a PVC for backend persistence.
	// +optional
	Storage *BackendStorageSpec `json:"storage,omitempty"`

	// GitMappings materialise files or directories from a named GitSync
	// repo into this backend container. The referenced GitSync name must
	// exist in NyxAgentSpec.GitSyncs. Mirrors the chart's per-backend
	// `gitMappings` block (#475).
	// +optional
	GitMappings []GitMappingSpec `json:"gitMappings,omitempty"`
}

// GitSyncCredentialsSpec is the dev-friendly / production-friendly
// credentials resolver for a GitSync entry, mirroring the chart's
// `nyx.resolveCredentials` helper for gitSync scope.
//
// Three mutually-exclusive modes:
//
//   1. ExistingSecret — reference a pre-created Secret. The operator
//      never writes to it. Production default.
//
//   2. Inline (Username + Token + AcknowledgeInsecureInline=true) —
//      the operator reconciles a per-entry Secret keyed
//      `GITSYNC_USERNAME` / `GITSYNC_PASSWORD` and envFroms it into
//      the git-sync init + sidecar. The ack flag is load-bearing: the
//      admission webhook refuses inline values without it because they
//      land in etcd + CR history and are recoverable via
//      `kubectl get nyxagent -o yaml`.
//
//   3. Empty — the entry's legacy `EnvFrom` list is used as-is (no
//      operator-managed Secret, no Secret reconciliation).
//
// When both ExistingSecret and inline values are set, ExistingSecret
// wins and the inline values are ignored.
type GitSyncCredentialsSpec struct {
	// ExistingSecret references a pre-created Secret containing the
	// gitSync credentials (typically GITSYNC_USERNAME / GITSYNC_PASSWORD
	// for HTTPS, or an SSH key env for git+ssh).
	// +optional
	ExistingSecret string `json:"existingSecret,omitempty"`

	// Username is the inline git username (dev path only). Must be
	// combined with AcknowledgeInsecureInline=true.
	// +optional
	Username string `json:"username,omitempty"`

	// Token is the inline git token or password (dev path only).
	// Must be combined with AcknowledgeInsecureInline=true.
	// +optional
	Token string `json:"token,omitempty"`

	// AcknowledgeInsecureInline MUST be true for inline Username/Token
	// to be accepted. The admission webhook rejects any NyxAgent that
	// sets Username or Token without this flag. The name is intentionally
	// long and awkward — it shows up in CR YAML and is the one place
	// users accept the security tradeoff explicitly.
	// +optional
	AcknowledgeInsecureInline bool `json:"acknowledgeInsecureInline,omitempty"`
}

// BackendCredentialsSpec is the dev-friendly / production-friendly
// credentials resolver for a backend entry, mirroring the chart's
// `nyx.resolveCredentials` helper for backend scope. Same three-mode
// semantics as GitSyncCredentialsSpec; the inline form carries an
// open-ended map of env-var-name → value so each backend can set its
// own token shape (CLAUDE_CODE_OAUTH_TOKEN, OPENAI_API_KEY,
// GOOGLE_API_KEY, …) without the CRD having to enumerate them.
type BackendCredentialsSpec struct {
	// ExistingSecret references a pre-created Secret containing the
	// backend credentials. The Secret's keys become the backend
	// container's env-var names.
	// +optional
	ExistingSecret string `json:"existingSecret,omitempty"`

	// Secrets is the inline env-var → value map (dev path only). Keys
	// become Secret data keys and environment variable names inside
	// the backend container. Must be combined with
	// AcknowledgeInsecureInline=true.
	// +optional
	Secrets map[string]string `json:"secrets,omitempty"`

	// AcknowledgeInsecureInline MUST be true for inline Secrets to be
	// accepted. See GitSyncCredentialsSpec for the rationale.
	// +optional
	AcknowledgeInsecureInline bool `json:"acknowledgeInsecureInline,omitempty"`
}

// GitSyncSpec configures a git-sync sidecar that clones a repo into /git
// on the pod and keeps it in sync. An init container runs the initial
// clone before the agent starts. Auth credentials are typically injected
// via EnvFrom referencing a Secret (e.g. GITSYNC_USERNAME, GITSYNC_PASSWORD,
// GITSYNC_SSH_KEY_FILE). Mirrors the chart's `gitSyncs[]` entry shape
// (charts/nyx/values.yaml).
type GitSyncSpec struct {
	// Name is the unique identifier for this git-sync entry within the
	// agent. Used to name the sidecar container (git-sync-<name>), the
	// init container (git-sync-init-<name>), and as the `gitSync`
	// reference in GitMappingSpec entries.
	// +kubebuilder:validation:Pattern=^[a-z0-9][a-z0-9-]*$
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Repo is the git repository URL (HTTPS or SSH).
	// +kubebuilder:validation:MinLength=1
	Repo string `json:"repo"`

	// Ref is the branch, tag, or commit SHA to sync. Defaults to HEAD.
	// +optional
	Ref string `json:"ref,omitempty"`

	// Period is the sync interval (e.g. "30s", "1m"). Defaults to "60s".
	// +optional
	Period string `json:"period,omitempty"`

	// Depth is the clone depth. 1 is sufficient for config-only repos.
	// +kubebuilder:validation:Minimum=0
	// +optional
	Depth int32 `json:"depth,omitempty"`

	// Image optionally overrides the default git-sync image for this
	// entry. When unset, the operator's default git-sync image is used
	// (ghcr.io/skthomasjr/images/git-sync:<appVersion>).
	// +optional
	Image *ImageSpec `json:"image,omitempty"`

	// EnvFrom sources env vars from Secrets or ConfigMaps, injected
	// into the git-sync init + sidecar containers for this entry.
	// When Credentials is set to a resolvable value, the resolved
	// Secret is prepended to this list at render time — the legacy
	// `envFrom` shape keeps working alongside the resolver.
	// +optional
	EnvFrom []corev1.EnvFromSource `json:"envFrom,omitempty"`

	// Credentials is the unified dev/prod credentials resolver for this
	// entry (#nyx.resolveCredentials parity). Three modes: ExistingSecret
	// (pre-created Secret, production default), inline Username+Token
	// with AcknowledgeInsecureInline=true (operator reconciles a Secret,
	// dev path), or empty (falls back to the legacy EnvFrom list).
	// +optional
	Credentials *GitSyncCredentialsSpec `json:"credentials,omitempty"`
}

// GitMappingSpec copies a file or directory from a named GitSync repo
// into the target container at a given destination path. Mirrors the
// chart's `gitMappings[]` entry shape so on-cluster behaviour matches
// byte-for-byte (#475).
type GitMappingSpec struct {
	// GitSync is the name of a GitSyncSpec in NyxAgentSpec.GitSyncs.
	// +kubebuilder:validation:Pattern=^[a-z0-9][a-z0-9-]*$
	// +kubebuilder:validation:MinLength=1
	GitSync string `json:"gitSync"`

	// Src is the source path within the repo, relative to the repo
	// root. A trailing "/" indicates a directory copy; otherwise a
	// single-file copy is performed.
	// +kubebuilder:validation:MinLength=1
	Src string `json:"src"`

	// Dest is the destination path inside the target container. The
	// mapped path is materialised from an emptyDir volume mounted into
	// the container, populated by the git-map-init container and
	// kept in sync by the git-sync sidecar's exechook.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^/.*`
	Dest string `json:"dest"`
}

// SharedStorageType selects the VolumeSource the operator emits for the
// agent-wide shared volume. Mirrors the chart's `sharedStorage.storageType`
// enum (`pvc`, `hostPath`) so operator-rendered pods match Helm-rendered
// pods byte-for-byte (#481, #611).
// +kubebuilder:validation:Enum=pvc;hostPath
type SharedStorageType string

const (
	// SharedStorageTypePVC selects a PersistentVolumeClaim volume source.
	// When ExistingClaim is set the operator references it verbatim; when
	// ExistingClaim is empty the operator reconciles a PVC named
	// "<agent>-shared" in the agent's namespace.
	SharedStorageTypePVC SharedStorageType = "pvc"

	// SharedStorageTypeHostPath selects a HostPath volume source. Intended
	// for single-node clusters (kind, Docker Desktop, minikube) that lack
	// a ReadWriteMany storage class; not recommended for production
	// multi-node clusters.
	SharedStorageTypeHostPath SharedStorageType = "hostPath"
)

// SharedStorageSpec configures a volume mounted into every container of the
// agent pod (nyx-harness + every backend sidecar). Mirrors the chart's
// `sharedStorage.*` block (charts/nyx/values.yaml) so the operator path has
// feature parity with the chart path (#481, #611).
//
// Backward compatibility: older CRs set only `claimName` and rely on the
// operator to treat that as a reference to a pre-existing PVC. When
// `claimName` is set the operator treats the spec as
// `{enabled: true, storageType: pvc, existingClaim: <claimName>}` — so
// existing CRs keep working without edits. New CRs should set `enabled`
// plus `storageType` plus either `existingClaim`, PVC sizing fields, or
// `hostPath`.
type SharedStorageSpec struct {
	// Enabled toggles rendering of the shared volume. When false (or when
	// the field is omitted), no shared volume is mounted and the operator
	// reconciles away any shared PVC it previously created.
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// StorageType selects the VolumeSource: `pvc` (default) or `hostPath`.
	// +kubebuilder:default=pvc
	// +optional
	StorageType SharedStorageType `json:"storageType,omitempty"`

	// MountPath is the absolute path inside each container the shared
	// volume is mounted at. Defaults to `/data/shared` to match the
	// chart.
	// +kubebuilder:default=/data/shared
	// +kubebuilder:validation:Pattern=`^/.*`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// ClaimName is a deprecated alias for ExistingClaim retained so CRs
	// authored against the original `SharedStorageRef` shape keep working
	// without edits. When set and ExistingClaim is empty, the operator
	// treats the spec as enabled=true, storageType=pvc,
	// existingClaim=<claimName>.
	// Deprecated: set `existingClaim` instead.
	// +optional
	ClaimName string `json:"claimName,omitempty"`

	// ExistingClaim references a pre-existing PersistentVolumeClaim. Only
	// meaningful when StorageType=pvc. When empty and StorageType=pvc the
	// operator creates a PVC named `<agent>-shared`.
	// +optional
	ExistingClaim string `json:"existingClaim,omitempty"`

	// Size is the PVC storage request (e.g. `1Gi`). Only meaningful when
	// StorageType=pvc and ExistingClaim is empty. Defaults to `1Gi`.
	// +optional
	Size string `json:"size,omitempty"`

	// StorageClassName is the storage class for the created PVC. Only
	// meaningful when StorageType=pvc and ExistingClaim is empty. When
	// empty the cluster default storage class is used.
	// +optional
	StorageClassName string `json:"storageClassName,omitempty"`

	// AccessModes are the PVC access modes. Only meaningful when
	// StorageType=pvc and ExistingClaim is empty. Defaults to
	// `[ReadWriteMany]` matching the chart's default.
	// +optional
	AccessModes []corev1.PersistentVolumeAccessMode `json:"accessModes,omitempty"`

	// HostPath is the node-local directory to bind-mount. Required (and
	// must be absolute) when StorageType=hostPath. Ignored otherwise.
	// Prefer hostPath for single-node local clusters only.
	// +kubebuilder:validation:Pattern=`^(|/.*)$`
	// +optional
	HostPath string `json:"hostPath,omitempty"`

	// HostPathType mirrors corev1.HostPathType. When nil the operator
	// defaults to `DirectoryOrCreate` so pods don't fail to start on
	// fresh nodes that have not yet materialised the directory (matches
	// the chart's fixed `type: DirectoryOrCreate`).
	// +optional
	HostPathType *corev1.HostPathType `json:"hostPathType,omitempty"`
}

// SharedStorageRef is the deprecated alias for SharedStorageSpec retained so
// existing Go consumers don't break. New code should reference
// SharedStorageSpec directly.
// Deprecated: use SharedStorageSpec.
type SharedStorageRef = SharedStorageSpec

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

	// SharedStorage optionally mounts a volume into every container of the
	// agent pod. Supports PVC mode (reference an existing claim or have
	// the operator create one sized per spec) and hostPath mode (for
	// single-node clusters). Mirrors charts/nyx/values.yaml's
	// `sharedStorage.*` block (#481, #611).
	// +optional
	SharedStorage *SharedStorageSpec `json:"sharedStorage,omitempty"`

	// GitSyncs declares git-sync sidecar(s) for this agent. Each entry
	// produces one init container (one-time clone) and one long-running
	// sidecar that keeps /git in sync with the remote repo. All
	// containers in the pod share the /git volume. Mirrors the chart's
	// per-agent `gitSyncs` block (#475).
	// +optional
	GitSyncs []GitSyncSpec `json:"gitSyncs,omitempty"`

	// GitMappings materialise files or directories from named GitSyncs
	// into the nyx-harness container. A referenced GitSync name must
	// exist in GitSyncs. Per-backend overrides live in
	// BackendSpec.GitMappings. Mirrors the chart's per-agent
	// `gitMappings` block (#475).
	// +optional
	GitMappings []GitMappingSpec `json:"gitMappings,omitempty"`

	// ServiceMonitor optionally creates a monitoring.coreos.com/v1
	// ServiceMonitor for the agent when the Prometheus Operator CRDs
	// are installed (#476). Gated by spec.metrics.enabled and a CRD
	// presence check at reconcile time; no-ops on clusters without
	// prometheus-operator.
	// +optional
	ServiceMonitor *ServiceMonitorSpec `json:"serviceMonitor,omitempty"`

	// PodMonitor optionally creates a monitoring.coreos.com/v1 PodMonitor
	// for the agent's backend containers (#582). Gated by
	// spec.metrics.enabled and a CRD presence check at reconcile time;
	// no-ops on clusters without prometheus-operator. Complementary to
	// ServiceMonitor: use PodMonitor to scrape per-backend /metrics
	// directly (tokens, tool-use, context usage).
	// +optional
	PodMonitor *PodMonitorSpec `json:"podMonitor,omitempty"`

	// Dashboard optionally deploys the Vue 3 dashboard (#470) alongside the
	// agent. The operator renders a per-agent dashboard (one per NyxAgent),
	// independent of the cluster-wide dashboard the Helm chart deploys.
	// Disabled by default.
	// +optional
	Dashboard *DashboardSpec `json:"dashboard,omitempty"`

	// PodAnnotations are merged onto the agent pod template's
	// `metadata.annotations`. Common uses: ServiceMesh sidecar injection
	// (e.g. `linkerd.io/inject: enabled`), trace sampling overrides,
	// observability-tool discovery hints (#477).
	//
	// Conflict precedence: operator-managed keys always win. User values
	// are applied first, then overwritten by any operator-managed
	// annotation (e.g. the `prometheus.io/*` set stamped when
	// `spec.metrics.podAnnotations` is enabled — #472). This prevents
	// silent loss of operator behaviour when users and the operator pick
	// overlapping keys.
	// +optional
	PodAnnotations map[string]string `json:"podAnnotations,omitempty"`

	// PodLabels are merged onto the agent pod template's `metadata.labels`.
	// Common uses: custom NetworkPolicy / PodMonitor selectors, team or
	// cost-allocation labels (#477).
	//
	// Conflict precedence: operator-managed keys always win. User labels
	// are applied first, then overwritten by the operator's canonical
	// agent labels. User-supplied entries that collide with the
	// Deployment's selector keys (labelName / labelComponent /
	// labelPartOf / labelManagedBy) are ignored so the selector cannot
	// drift and orphan pods.
	// +optional
	PodLabels map[string]string `json:"podLabels,omitempty"`

	// NodeSelector constrains the agent pod onto nodes whose labels match
	// every entry. Mirrors the chart-side `nodeSelector` value (#603) so
	// operator- and Helm-rendered pods schedule identically.
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// Tolerations allow the agent pod onto nodes with matching taints.
	// Mirrors the chart-side `tolerations` value (#603).
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`

	// Affinity expresses node / pod affinity and anti-affinity rules for
	// the agent pod. Mirrors the chart-side `affinity` value (#603).
	// +optional
	Affinity *corev1.Affinity `json:"affinity,omitempty"`

	// TopologySpreadConstraints spreads agent replicas across zones /
	// nodes for HA. Mirrors the chart-side `topologySpreadConstraints`
	// value (#603).
	// +optional
	TopologySpreadConstraints []corev1.TopologySpreadConstraint `json:"topologySpreadConstraints,omitempty"`

	// PriorityClassName sets the pod's scheduling priority. Mirrors the
	// chart-side `priorityClassName` value (#603).
	// +optional
	PriorityClassName string `json:"priorityClassName,omitempty"`

	// ServicePort overrides the agent Service's `port` so it can differ
	// from the container port (#479). When unset, the Service `port` and
	// `targetPort` both resolve to `Spec.Port` (preserving current
	// behaviour). When set, `Service.spec.ports[0].port` becomes
	// `ServicePort` while `targetPort` continues to point at the
	// container port (`Spec.Port`). Mirrors the chart's `service.yaml`
	// which already separates Service port from target port.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +optional
	ServicePort *int32 `json:"servicePort,omitempty"`
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

	// Port is the Service port (the container always listens on 3000).
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

// PodMonitorSpec configures an optional monitoring.coreos.com/v1 PodMonitor
// reconciled by the operator when the Prometheus Operator CRDs are present
// (#582). Mirrors the chart's `podMonitor.*` block. Unlike ServiceMonitor
// (which scrapes the harness Service), PodMonitor scrapes each backend
// container's /metrics endpoint directly — useful for per-backend
// telemetry (tokens, tool-use, context-window usage).
//
// Reconciliation is gated by the same CRD-presence probe as ServiceMonitor;
// no hard dependency on prometheus-operator Go types is introduced.
type PodMonitorSpec struct {
	// Enabled toggles creation of the PodMonitor. Requires
	// spec.metrics.enabled=true (no endpoint to scrape otherwise) and the
	// monitoring.coreos.com CRD to be installed.
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

	// Labels merged into the PodMonitor's metadata.labels for tenancy-
	// selecting Prometheus deployments (e.g. `release: kube-prometheus-stack`).
	// +optional
	Labels map[string]string `json:"labels,omitempty"`
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
