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

package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"

	nyxv1alpha1 "github.com/nyx-ai/nyx-operator/api/v1alpha1"
)

// Label keys used for every resource the operator creates.
const (
	labelName      = "app.kubernetes.io/name"
	labelComponent = "app.kubernetes.io/component"
	labelPartOf    = "app.kubernetes.io/part-of"
	labelManagedBy = "app.kubernetes.io/managed-by"

	// componentAgent matches the chart's nyx.agentLabels component value
	// ("nyx-harness") so Prometheus rules, ServiceMonitors, and Grafana
	// panels that select on `app.kubernetes.io/component=nyx-harness`
	// match operator-rendered agents the same way they match Helm-rendered
	// agents (#575). managedBy stays "nyx-operator" (vs the chart's "helm")
	// on purpose — it's the one label that is semantically different
	// between the two install paths and consumers should be able to tell
	// the rendering path apart.
	componentAgent   = "nyx-harness"
	componentBackend = "backend"
	partOf           = "nyx"
	managedBy        = "nyx-operator"

	// sharedStorageVolume is the pod-level volume name used when the agent
	// references a pre-existing shared PVC. All containers mount it.
	sharedStorageVolume = "shared-storage"

	// agentConfigVolumePrefix is the prefix for per-agent/backend inline
	// config ConfigMap volume names.
	agentConfigVolumePrefix = "agent-config-"

	// teamLabel opts a NyxAgent into a named team. Agents sharing the same
	// value are listed together in the team manifest ConfigMap so each
	// harness can address its peers by name (#474). When unset, the agent
	// is grouped with every other label-less NyxAgent in its namespace.
	teamLabel = "nyx.ai/team"

	// componentManifest labels the per-team manifest ConfigMap so it can
	// be identified independently of the per-agent owned ConfigMaps
	// (inline config, dashboard nginx template, etc.).
	componentManifest = "nyx-manifest"

	// manifestVolumeName / manifestMountPath mirror the chart's harness
	// volume wiring so operator-rendered pods behave identically to
	// Helm-rendered pods (#474). MANIFEST_PATH env override still wins
	// at runtime if an operator needs to remap it.
	manifestVolumeName = "manifest"
	manifestMountPath  = "/home/agent/manifest.json"
	manifestSubPath    = "manifest.json"
)

// agentLabels returns the canonical label set for resources owned by an agent.
func agentLabels(agent *nyxv1alpha1.NyxAgent) map[string]string {
	return map[string]string{
		labelName:      agent.Name,
		labelComponent: componentAgent,
		labelPartOf:    partOf,
		labelManagedBy: managedBy,
	}
}

// selectorLabels returns the minimal label set used as a Deployment/Service
// selector. This intentionally omits managed-by/part-of so selectors stay
// forward-compatible when those values change.
func selectorLabels(agent *nyxv1alpha1.NyxAgent) map[string]string {
	return map[string]string{
		labelName: agent.Name,
	}
}

// imageRef assembles a container image reference from an ImageSpec, falling
// back to the provided default tag when the spec omits one.
func imageRef(img nyxv1alpha1.ImageSpec, fallbackTag string) string {
	tag := img.Tag
	if tag == "" {
		tag = fallbackTag
	}
	return fmt.Sprintf("%s:%s", img.Repository, tag)
}

// imagePullPolicy returns the effective container ImagePullPolicy for an
// ImageSpec, defaulting to IfNotPresent when the spec leaves it unset. This
// mirrors the Helm chart's `| default "IfNotPresent"` behaviour so the
// operator and the chart render identical pod specs for the same CR (#578).
// When the user explicitly sets PullPolicy (including "Always" for :latest
// during local dev), the spec value is honoured verbatim.
func imagePullPolicy(img nyxv1alpha1.ImageSpec) corev1.PullPolicy {
	if img.PullPolicy == "" {
		return corev1.PullIfNotPresent
	}
	return img.PullPolicy
}

// probeDefaults returns an effective ProbeSpec merging optional overrides with
// the Helm-chart defaults.
func probeDefaults(override *nyxv1alpha1.ProbeSpec, liveness bool) nyxv1alpha1.ProbeSpec {
	// Defaults match charts/nyx/values.yaml probes.*.
	var def nyxv1alpha1.ProbeSpec
	if liveness {
		def = nyxv1alpha1.ProbeSpec{
			InitialDelaySeconds: 10,
			PeriodSeconds:       30,
			TimeoutSeconds:      5,
			FailureThreshold:    3,
		}
	} else {
		def = nyxv1alpha1.ProbeSpec{
			InitialDelaySeconds: 5,
			PeriodSeconds:       10,
			TimeoutSeconds:      5,
			FailureThreshold:    3,
		}
	}
	if override == nil {
		return def
	}
	if override.InitialDelaySeconds > 0 {
		def.InitialDelaySeconds = override.InitialDelaySeconds
	}
	if override.PeriodSeconds > 0 {
		def.PeriodSeconds = override.PeriodSeconds
	}
	if override.TimeoutSeconds > 0 {
		def.TimeoutSeconds = override.TimeoutSeconds
	}
	if override.FailureThreshold > 0 {
		def.FailureThreshold = override.FailureThreshold
	}
	return def
}

// httpProbe builds an HTTP GET probe against the given port and path using
// the timing from spec.
func httpProbe(port int32, path string, spec nyxv1alpha1.ProbeSpec) *corev1.Probe {
	return &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			HTTPGet: &corev1.HTTPGetAction{
				Path: path,
				Port: intstr.FromInt(int(port)),
			},
		},
		InitialDelaySeconds: spec.InitialDelaySeconds,
		PeriodSeconds:       spec.PeriodSeconds,
		TimeoutSeconds:      spec.TimeoutSeconds,
		FailureThreshold:    spec.FailureThreshold,
	}
}

// agentConfigMapName returns the ConfigMap name for an agent's or backend's
// inline config files. The backend name may be empty for the agent itself.
func agentConfigMapName(agent *nyxv1alpha1.NyxAgent, backendName string) string {
	if backendName == "" {
		return fmt.Sprintf("%s-config", agent.Name)
	}
	return fmt.Sprintf("%s-%s-config", agent.Name, backendName)
}

// buildConfigMap renders one ConfigMap containing the given inline files, or
// nil if files is empty.
func buildConfigMap(agent *nyxv1alpha1.NyxAgent, name string, files []nyxv1alpha1.ConfigFile) *corev1.ConfigMap {
	if len(files) == 0 {
		return nil
	}
	data := make(map[string]string, len(files))
	for _, f := range files {
		data[f.Name] = f.Content
	}
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: agent.Namespace,
			Labels:    agentLabels(agent),
		},
		Data: data,
	}
}

// configVolumesAndMounts converts a list of ConfigFiles (backed by the named
// ConfigMap) into the pod Volume + container VolumeMount pairs needed to
// surface each file at its declared mount path.
//
// Each file becomes its own subPath mount so the existing container filesystem
// is not masked (the common K8s pattern for injecting individual files).
func configVolumesAndMounts(cmName string, volumeKey string, files []nyxv1alpha1.ConfigFile) ([]corev1.Volume, []corev1.VolumeMount) {
	if len(files) == 0 {
		return nil, nil
	}
	vol := corev1.Volume{
		Name: volumeKey,
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: cmName},
			},
		},
	}
	mounts := make([]corev1.VolumeMount, 0, len(files))
	for _, f := range files {
		mounts = append(mounts, corev1.VolumeMount{
			Name:      volumeKey,
			MountPath: f.MountPath,
			SubPath:   f.Name,
			ReadOnly:  true,
		})
	}
	return []corev1.Volume{vol}, mounts
}

// backendStorageVolumeAndMounts returns the pod Volume and container
// VolumeMounts for a backend's persistent storage, or (nil, nil) when storage
// is disabled.
func backendStorageVolumeAndMounts(agent *nyxv1alpha1.NyxAgent, b nyxv1alpha1.BackendSpec) (*corev1.Volume, []corev1.VolumeMount) {
	if b.Storage == nil || !b.Storage.Enabled {
		return nil, nil
	}
	claimName := b.Storage.ExistingClaim
	if claimName == "" {
		claimName = fmt.Sprintf("%s-%s-data", agent.Name, b.Name)
	}
	volName := fmt.Sprintf("%s-data", b.Name)
	vol := &corev1.Volume{
		Name: volName,
		VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: claimName,
			},
		},
	}
	mounts := make([]corev1.VolumeMount, 0, len(b.Storage.Mounts))
	if len(b.Storage.Mounts) == 0 {
		// Default to mounting the root of the PVC at /data when no sub-mounts are listed.
		mounts = append(mounts, corev1.VolumeMount{Name: volName, MountPath: "/data"})
	} else {
		for _, m := range b.Storage.Mounts {
			mounts = append(mounts, corev1.VolumeMount{
				Name:      volName,
				MountPath: m.MountPath,
				SubPath:   m.SubPath,
			})
		}
	}
	return vol, mounts
}

// buildDeployment assembles the agent Deployment: one nyx-harness container
// plus one container per backend. AppVersion is the chart/operator app version
// used as a default image tag when an ImageSpec omits Tag.
func buildDeployment(agent *nyxv1alpha1.NyxAgent, appVersion string) *appsv1.Deployment {
	labels := agentLabels(agent)
	selector := selectorLabels(agent)

	// Build volumes and container mount references for agent + each backend.
	var volumes []corev1.Volume
	var harnessMounts []corev1.VolumeMount

	// Agent inline config.
	if len(agent.Spec.Config) > 0 {
		vols, mounts := configVolumesAndMounts(
			agentConfigMapName(agent, ""),
			agentConfigVolumePrefix+"harness",
			agent.Spec.Config,
		)
		volumes = append(volumes, vols...)
		harnessMounts = append(harnessMounts, mounts...)
	}

	// Shared storage (pre-existing PVC).
	if agent.Spec.SharedStorage != nil && agent.Spec.SharedStorage.ClaimName != "" {
		mountPath := agent.Spec.SharedStorage.MountPath
		if mountPath == "" {
			mountPath = "/data/shared"
		}
		volumes = append(volumes, corev1.Volume{
			Name: sharedStorageVolume,
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: agent.Spec.SharedStorage.ClaimName,
				},
			},
		})
		harnessMounts = append(harnessMounts, corev1.VolumeMount{
			Name:      sharedStorageVolume,
			MountPath: mountPath,
		})
	}

	// Team manifest — mounted at the same path the chart uses
	// (/home/agent/manifest.json) so the harness's /team and /proxy/{name}
	// endpoints work identically across rendering paths (#474). The CM
	// itself is reconciled separately; if it doesn't exist yet, the pod
	// will stay in ContainerCreating until the first reconcile writes it.
	{
		cmName := manifestConfigMapName(agent)
		vol, mount := manifestVolumeAndMount(cmName)
		volumes = append(volumes, vol)
		harnessMounts = append(harnessMounts, mount)
	}

	// nyx-harness container.
	harnessPort := agent.Spec.Port
	if harnessPort == 0 {
		harnessPort = 8000
	}
	livenessProbe := probeDefaults(nil, true)
	readinessProbe := probeDefaults(nil, false)
	if agent.Spec.Probes != nil {
		livenessProbe = probeDefaults(agent.Spec.Probes.Liveness, true)
		readinessProbe = probeDefaults(agent.Spec.Probes.Readiness, false)
	}

	harness := corev1.Container{
		Name:            "nyx-harness",
		Image:           imageRef(agent.Spec.Image, appVersion),
		ImagePullPolicy: imagePullPolicy(agent.Spec.Image),
		Ports: []corev1.ContainerPort{
			{Name: "http", ContainerPort: harnessPort},
		},
		Env: append([]corev1.EnvVar{
			{Name: "AGENT_NAME", Value: agent.Name},
			{Name: "AGENT_PORT", Value: fmt.Sprintf("%d", harnessPort)},
			{Name: "METRICS_ENABLED", Value: metricsEnabledValue(agent)},
		}, agent.Spec.Env...),
		EnvFrom:   agent.Spec.EnvFrom,
		Resources: agent.Spec.Resources,
		SecurityContext: &corev1.SecurityContext{
			AllowPrivilegeEscalation: boolPtr(false),
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		},
		LivenessProbe:  httpProbe(harnessPort, "/health/live", livenessProbe),
		ReadinessProbe: httpProbe(harnessPort, "/health/ready", readinessProbe),
		VolumeMounts:   harnessMounts,
		Lifecycle:      preStopLifecycle(agent),
	}

	// Backend containers.
	containers := []corev1.Container{harness}
	// Sort backends by name for a deterministic rendering. Skip per-backend
	// disabled entries (#chart beta.32 / #466 mirror) so the container,
	// per-backend volume mounts, and any inline configs aren't materialised.
	backends := append([]nyxv1alpha1.BackendSpec(nil), agent.Spec.Backends...)
	sort.Slice(backends, func(i, j int) bool { return backends[i].Name < backends[j].Name })
	for _, b := range backends {
		if !backendEnabled(b) {
			continue
		}
		bPort := b.Port
		if bPort == 0 {
			bPort = 8080
		}
		var bMounts []corev1.VolumeMount

		// Backend inline config.
		if len(b.Config) > 0 {
			vols, mounts := configVolumesAndMounts(
				agentConfigMapName(agent, b.Name),
				agentConfigVolumePrefix+b.Name,
				b.Config,
			)
			volumes = append(volumes, vols...)
			bMounts = append(bMounts, mounts...)
		}

		// Backend storage PVC.
		if vol, mounts := backendStorageVolumeAndMounts(agent, b); vol != nil {
			volumes = append(volumes, *vol)
			bMounts = append(bMounts, mounts...)
		}

		// Shared storage (same volume as harness).
		if agent.Spec.SharedStorage != nil && agent.Spec.SharedStorage.ClaimName != "" {
			mountPath := agent.Spec.SharedStorage.MountPath
			if mountPath == "" {
				mountPath = "/data/shared"
			}
			bMounts = append(bMounts, corev1.VolumeMount{Name: sharedStorageVolume, MountPath: mountPath})
		}

		bc := corev1.Container{
			Name:            b.Name,
			Image:           imageRef(b.Image, appVersion),
			ImagePullPolicy: imagePullPolicy(b.Image),
			Ports: []corev1.ContainerPort{
				{Name: "http", ContainerPort: bPort},
			},
			Env: append([]corev1.EnvVar{
				{Name: "AGENT_NAME", Value: fmt.Sprintf("%s-a2-%s", agent.Name, b.Name)},
				{Name: "AGENT_OWNER", Value: agent.Name},
				{Name: "AGENT_ID", Value: b.Name},
				{Name: "AGENT_URL", Value: fmt.Sprintf("http://localhost:%d", bPort)},
				{Name: "BACKEND_PORT", Value: fmt.Sprintf("%d", bPort)},
				{Name: "METRICS_ENABLED", Value: metricsEnabledValue(agent)},
			}, b.Env...),
			EnvFrom:   b.EnvFrom,
			Resources: b.Resources,
			SecurityContext: &corev1.SecurityContext{
				AllowPrivilegeEscalation: boolPtr(false),
				Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
			},
			LivenessProbe:  httpProbe(bPort, "/health", livenessProbe),
			ReadinessProbe: httpProbe(bPort, "/health", readinessProbe),
			VolumeMounts:   bMounts,
			Lifecycle:      preStopLifecycle(agent),
		}
		if b.Model != "" {
			bc.Env = append(bc.Env, corev1.EnvVar{Name: "BACKEND_MODEL", Value: b.Model})
		}
		containers = append(containers, bc)
	}

	// Replicas: omitted when autoscaling owns the value.
	var replicas *int32
	if agent.Spec.Autoscaling == nil || !agent.Spec.Autoscaling.Enabled {
		replicas = int32Ptr(1)
	}

	// Resolve ServiceAccount + automount per risk #538. Default stays
	// hardened (no SA, token not mounted). When the user opts in by
	// setting ServiceAccountName, automount flips to true so in-cluster
	// MCP tools (mcp-kubernetes, mcp-helm) can reach the Kubernetes API.
	// An explicit AutomountServiceAccountToken on the spec always wins.
	var automount *bool
	switch {
	case agent.Spec.AutomountServiceAccountToken != nil:
		automount = agent.Spec.AutomountServiceAccountToken
	case agent.Spec.ServiceAccountName != "":
		automount = boolPtr(true)
	default:
		automount = boolPtr(false)
	}

	podSpec := corev1.PodSpec{
		TerminationGracePeriodSeconds: func() *int64 {
			if agent.Spec.TerminationGracePeriodSeconds != nil {
				return agent.Spec.TerminationGracePeriodSeconds
			}
			return int64Ptr(60)
		}(),
		ServiceAccountName:           agent.Spec.ServiceAccountName,
		AutomountServiceAccountToken: automount,
		SecurityContext: &corev1.PodSecurityContext{
			RunAsNonRoot: boolPtr(true),
			RunAsUser:    int64Ptr(1000),
			RunAsGroup:   int64Ptr(1000),
			FSGroup:      int64Ptr(1000),
			SeccompProfile: &corev1.SeccompProfile{
				Type: corev1.SeccompProfileTypeRuntimeDefault,
			},
		},
		ImagePullSecrets: agent.Spec.ImagePullSecrets,
		Containers:       containers,
		Volumes:          volumes,
	}

	// Pod-level annotations: start from any user-supplied spec.podAnnotations
	// (#477), then overlay operator-managed keys so the operator always wins
	// on conflict. The Prometheus scrape set (#chart beta.35 / #472) is
	// stamped when spec.metrics.enabled and metrics.podAnnotations are on.
	podAnnotations := mergeStringMap(nil, agent.Spec.PodAnnotations)
	if agent.Spec.Metrics.Enabled && metricsPodAnnotationsEnabled(agent) {
		podAnnotations = mergeStringMap(podAnnotations, map[string]string{
			"prometheus.io/scrape": "true",
			"prometheus.io/port":   fmt.Sprintf("%d", harnessPort),
			"prometheus.io/path":   "/metrics",
		})
	}
	if len(podAnnotations) == 0 {
		podAnnotations = nil
	}

	// Pod-level labels: start from any user-supplied spec.podLabels (#477),
	// drop entries that would clobber operator-managed selector keys, then
	// overlay the canonical agent labels so the selector stays stable.
	podLabels := map[string]string{}
	reservedLabelKeys := map[string]struct{}{
		labelName:      {},
		labelComponent: {},
		labelPartOf:    {},
		labelManagedBy: {},
	}
	for k, v := range agent.Spec.PodLabels {
		if _, reserved := reservedLabelKeys[k]; reserved {
			continue
		}
		podLabels[k] = v
	}
	for k, v := range labels {
		podLabels[k] = v
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      agent.Name,
			Namespace: agent.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: replicas,
			Selector: &metav1.LabelSelector{MatchLabels: selector},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: podLabels, Annotations: podAnnotations},
				Spec:       podSpec,
			},
		},
	}
}

// mergeStringMap returns a new map containing all entries from base with
// entries from overlay applied on top (overlay wins on key collision).
// A nil base is treated as empty. The result is nil only when both inputs
// are empty.
func mergeStringMap(base, overlay map[string]string) map[string]string {
	if len(base) == 0 && len(overlay) == 0 {
		return nil
	}
	out := make(map[string]string, len(base)+len(overlay))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range overlay {
		out[k] = v
	}
	return out
}

// buildService constructs the Service exposing the agent's nyx-harness
// HTTP port. Service.spec.type defaults to ClusterIP and is overridable
// via spec.serviceType (#chart beta.31 / #466). Prometheus scrape
// annotations honour spec.metrics.serviceAnnotations (default true)
// when spec.metrics.enabled is true (#chart beta.35 / #472).
func buildService(agent *nyxv1alpha1.NyxAgent) *corev1.Service {
	port := agent.Spec.Port
	if port == 0 {
		port = 8000
	}

	annotations := map[string]string{}
	if agent.Spec.Metrics.Enabled && metricsServiceAnnotationsEnabled(agent) {
		annotations["prometheus.io/scrape"] = "true"
		annotations["prometheus.io/port"] = fmt.Sprintf("%d", port)
		annotations["prometheus.io/path"] = "/metrics"
	}

	svcType := agent.Spec.ServiceType
	if svcType == "" {
		svcType = corev1.ServiceTypeClusterIP
	}

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:        agent.Name,
			Namespace:   agent.Namespace,
			Labels:      agentLabels(agent),
			Annotations: annotations,
		},
		Spec: corev1.ServiceSpec{
			Type:     svcType,
			Selector: selectorLabels(agent),
			Ports: []corev1.ServicePort{{
				Name:       "http",
				Port:       port,
				TargetPort: intstr.FromInt(int(port)),
			}},
		},
	}
}

// serviceMonitorGVK is the monitoring.coreos.com/v1 ServiceMonitor
// GroupVersionKind used by the Prometheus Operator. The operator writes
// ServiceMonitors via an unstructured client so the controller build has
// no hard dependency on prometheus-operator Go types — the CRD may or
// may not be installed on the target cluster (#476).
var serviceMonitorGVK = schema.GroupVersionKind{
	Group:   "monitoring.coreos.com",
	Version: "v1",
	Kind:    "ServiceMonitor",
}

// serviceMonitorEnabled reports whether the agent opted in to
// ServiceMonitor reconciliation. Creation still additionally requires
// spec.metrics.enabled=true (no point scraping a Service that has no
// annotations or metrics endpoint advertised) and CRD presence.
func serviceMonitorEnabled(agent *nyxv1alpha1.NyxAgent) bool {
	return agent.Spec.ServiceMonitor != nil && agent.Spec.ServiceMonitor.Enabled
}

// buildServiceMonitor assembles the unstructured ServiceMonitor manifest
// for an agent. Mirrors charts/nyx/templates/servicemonitor.yaml so the
// two rendering paths produce equivalent ServiceMonitors for the same
// input: one endpoint named `http` (the harness Service port), scraped
// at /metrics with the configured interval + timeout, selected by the
// agent's app.kubernetes.io/name label, and namespace-scoped to the
// agent's namespace.
func buildServiceMonitor(agent *nyxv1alpha1.NyxAgent) *unstructured.Unstructured {
	sm := agent.Spec.ServiceMonitor
	if sm == nil {
		return nil
	}
	interval := sm.Interval
	if interval == "" {
		interval = "30s"
	}
	scrapeTimeout := sm.ScrapeTimeout
	if scrapeTimeout == "" {
		scrapeTimeout = "10s"
	}

	// Labels: start from the canonical agent labels so Prometheus's
	// ServiceMonitor selectors that look at the nyx label set still
	// match, then merge any tenancy labels the user supplied (e.g.
	// `release: kube-prometheus-stack`).
	labels := agentLabels(agent)
	for k, v := range sm.Labels {
		labels[k] = v
	}

	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(serviceMonitorGVK)
	obj.SetName(agent.Name)
	obj.SetNamespace(agent.Namespace)
	obj.SetLabels(labels)
	obj.Object["spec"] = map[string]interface{}{
		"selector": map[string]interface{}{
			"matchLabels": map[string]interface{}{
				labelName: agent.Name,
			},
		},
		"namespaceSelector": map[string]interface{}{
			"matchNames": []interface{}{agent.Namespace},
		},
		"endpoints": []interface{}{
			map[string]interface{}{
				"port":          "http",
				"path":          "/metrics",
				"interval":      interval,
				"scrapeTimeout": scrapeTimeout,
			},
		},
	}
	return obj
}

// metricsServiceAnnotationsEnabled / metricsPodAnnotationsEnabled resolve
// the per-agent metrics annotation toggles, defaulting to backward-
// compatible chart behaviour when the field is unset (#chart beta.35).
func metricsServiceAnnotationsEnabled(agent *nyxv1alpha1.NyxAgent) bool {
	if agent.Spec.Metrics.ServiceAnnotations != nil {
		return *agent.Spec.Metrics.ServiceAnnotations
	}
	return true // chart default
}

func metricsPodAnnotationsEnabled(agent *nyxv1alpha1.NyxAgent) bool {
	if agent.Spec.Metrics.PodAnnotations != nil {
		return *agent.Spec.Metrics.PodAnnotations
	}
	return false // chart default
}

// metricsEnabledValue renders the METRICS_ENABLED env var value for harness
// and backend containers, mirroring the chart's quoted bool semantics
// (#502). Backends gate their /metrics endpoint on this variable.
func metricsEnabledValue(agent *nyxv1alpha1.NyxAgent) string {
	if agent.Spec.Metrics.Enabled {
		return "true"
	}
	return "false"
}

// backendEnabled reports the per-backend enabled flag with default-true
// semantics (#chart beta.32). Returns true when the field is unset.
// preStopLifecycle returns a `lifecycle.preStop` exec sleep hook when the
// agent's PreStop spec is enabled. The sleep duration mirrors the chart
// default (5s) when DelaySeconds is unset, matching charts/nyx
// templates/deployment.yaml (#547, #512). Returns nil when PreStop is
// disabled or the spec is absent.
func preStopLifecycle(agent *nyxv1alpha1.NyxAgent) *corev1.Lifecycle {
	if agent.Spec.PreStop == nil || !agent.Spec.PreStop.Enabled {
		return nil
	}
	delay := agent.Spec.PreStop.DelaySeconds
	if delay <= 0 {
		delay = 5
	}
	return &corev1.Lifecycle{
		PreStop: &corev1.LifecycleHandler{
			Exec: &corev1.ExecAction{
				Command: []string{"/bin/sh", "-c", fmt.Sprintf("sleep %d", delay)},
			},
		},
	}
}

func backendEnabled(b nyxv1alpha1.BackendSpec) bool {
	if b.Enabled != nil {
		return *b.Enabled
	}
	return true
}

// buildHPA constructs the optional HorizontalPodAutoscaler, or nil when
// autoscaling is disabled.
func buildHPA(agent *nyxv1alpha1.NyxAgent) *autoscalingv2.HorizontalPodAutoscaler {
	a := agent.Spec.Autoscaling
	if a == nil || !a.Enabled {
		return nil
	}
	minR := a.MinReplicas
	if minR == 0 {
		minR = 1
	}
	maxR := a.MaxReplicas
	if maxR == 0 {
		maxR = 3
	}
	var metrics []autoscalingv2.MetricSpec
	if a.TargetCPUUtilizationPercentage != nil {
		metrics = append(metrics, autoscalingv2.MetricSpec{
			Type: autoscalingv2.ResourceMetricSourceType,
			Resource: &autoscalingv2.ResourceMetricSource{
				Name: corev1.ResourceCPU,
				Target: autoscalingv2.MetricTarget{
					Type:               autoscalingv2.UtilizationMetricType,
					AverageUtilization: a.TargetCPUUtilizationPercentage,
				},
			},
		})
	}
	if a.TargetMemoryUtilizationPercentage != nil {
		metrics = append(metrics, autoscalingv2.MetricSpec{
			Type: autoscalingv2.ResourceMetricSourceType,
			Resource: &autoscalingv2.ResourceMetricSource{
				Name: corev1.ResourceMemory,
				Target: autoscalingv2.MetricTarget{
					Type:               autoscalingv2.UtilizationMetricType,
					AverageUtilization: a.TargetMemoryUtilizationPercentage,
				},
			},
		})
	}
	// If neither target is set, default to 80% CPU so the HPA has at least one metric.
	if len(metrics) == 0 {
		def := int32(80)
		metrics = append(metrics, autoscalingv2.MetricSpec{
			Type: autoscalingv2.ResourceMetricSourceType,
			Resource: &autoscalingv2.ResourceMetricSource{
				Name: corev1.ResourceCPU,
				Target: autoscalingv2.MetricTarget{
					Type:               autoscalingv2.UtilizationMetricType,
					AverageUtilization: &def,
				},
			},
		})
	}
	return &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      agent.Name,
			Namespace: agent.Namespace,
			Labels:    agentLabels(agent),
		},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       agent.Name,
			},
			MinReplicas: &minR,
			MaxReplicas: maxR,
			Metrics:     metrics,
		},
	}
}

// buildPDB constructs the optional PodDisruptionBudget, or nil when disabled.
// Exactly one of MinAvailable or MaxUnavailable is honoured — MaxUnavailable
// wins when both are set.
func buildPDB(agent *nyxv1alpha1.NyxAgent) *policyv1.PodDisruptionBudget {
	p := agent.Spec.PodDisruptionBudget
	if p == nil || !p.Enabled {
		return nil
	}
	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      agent.Name,
			Namespace: agent.Namespace,
			Labels:    agentLabels(agent),
		},
		Spec: policyv1.PodDisruptionBudgetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: selectorLabels(agent)},
		},
	}
	if p.MaxUnavailable != nil {
		v := intstr.FromInt(int(*p.MaxUnavailable))
		pdb.Spec.MaxUnavailable = &v
	} else {
		min := int32(1)
		if p.MinAvailable != nil {
			min = *p.MinAvailable
		}
		v := intstr.FromInt(int(min))
		pdb.Spec.MinAvailable = &v
	}
	return pdb
}

// PVCBuildError describes a backend PVC entry that could not be built.
// Surfaced to the controller so it can log + emit events; previously these
// were silently dropped (#454).
type PVCBuildError struct {
	BackendName string
	Size        string
	Err         error
}

func (e *PVCBuildError) Error() string {
	return fmt.Sprintf("backend %q: invalid storage.size %q: %v", e.BackendName, e.Size, e.Err)
}

// buildBackendPVCs returns the PVCs the operator should create for each
// backend whose Storage.Enabled is true and whose ExistingClaim is empty.
// The second return value lists per-backend size-parse failures so the
// caller can surface them as logs + events instead of silent drops (#454).
func buildBackendPVCs(agent *nyxv1alpha1.NyxAgent) ([]*corev1.PersistentVolumeClaim, []*PVCBuildError) {
	var out []*corev1.PersistentVolumeClaim
	var errs []*PVCBuildError
	for _, b := range agent.Spec.Backends {
		if !backendEnabled(b) {
			continue
		}
		if b.Storage == nil || !b.Storage.Enabled || b.Storage.ExistingClaim != "" {
			continue
		}
		size := b.Storage.Size
		if size == "" {
			size = "1Gi"
		}
		qty, err := resource.ParseQuantity(size)
		if err != nil {
			errs = append(errs, &PVCBuildError{BackendName: b.Name, Size: size, Err: err})
			continue
		}
		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-%s-data", agent.Name, b.Name),
				Namespace: agent.Namespace,
				Labels:    agentLabels(agent),
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceStorage: qty},
				},
			},
		}
		if b.Storage.StorageClassName != "" {
			pvc.Spec.StorageClassName = &b.Storage.StorageClassName
		}
		out = append(out, pvc)
	}
	return out, errs
}

// ── small helpers ─────────────────────────────────────────────────────────────

func boolPtr(b bool) *bool    { return &b }
func int32Ptr(i int32) *int32 { return &i }
func int64Ptr(i int64) *int64 { return &i }

// dashboardLabels returns the label set for the per-agent dashboard
// Deployment + Service. The "component=dashboard" label distinguishes
// them from the agent's own pods so selectors stay unambiguous (#470).
func dashboardLabels(agent *nyxv1alpha1.NyxAgent) map[string]string {
	return map[string]string{
		labelName:      agent.Name + "-dashboard",
		labelComponent: "dashboard",
		labelPartOf:    partOf,
		labelManagedBy: managedBy,
	}
}

func dashboardSelectorLabels(agent *nyxv1alpha1.NyxAgent) map[string]string {
	return map[string]string{
		labelName: agent.Name + "-dashboard",
	}
}

// buildDashboardConfigMap renders the nginx template the dashboard image
// reads from /etc/nginx/templates/. The template teaches nginx about the
// owned agent's service at /api/agents/<name>/... and serves /api/team
// inline so the dashboard client's two-phase discovery (directory + per-
// agent fan-out) works against a single-CR deployment (#470). Returns nil
// when the dashboard is disabled.
//
// The ConfigMap mirrors the logic in
// charts/nyx/templates/configmap-dashboard-nginx.yaml but scoped to the
// one NyxAgent the operator owns — per-CR dashboards can't see other
// agents the operator happens to manage; that's a deliberate boundary.
func buildDashboardConfigMap(agent *nyxv1alpha1.NyxAgent) *corev1.ConfigMap {
	d := agent.Spec.Dashboard
	if d == nil || !d.Enabled {
		return nil
	}

	agentPort := agent.Spec.Port
	if agentPort == 0 {
		agentPort = 8000
	}

	// FQDN — nginx's resolver doesn't apply Kubernetes search domains, so
	// short names land as NXDOMAIN. The cluster DNS zone defaults to
	// `cluster.local` but is overridable via spec.dashboard.clusterDomain
	// for clusters bootstrapped with a custom --service-dns-domain
	// (risk #581). The CRD pattern validates the value charset, so it is
	// safe to interpolate into the nginx template verbatim.
	clusterDomain := d.ClusterDomain
	if clusterDomain == "" {
		clusterDomain = "cluster.local"
	}
	upstream := fmt.Sprintf("http://%s.%s.svc.%s:%d", agent.Name, agent.Namespace, clusterDomain, agentPort)
	directory := fmt.Sprintf(`[{"name":%q,"url":%q}]`, agent.Name, fmt.Sprintf("http://%s:%d", agent.Name, agentPort))

	tpl := `server {
  listen 8080;
  server_name _;

  resolver ${NGINX_LOCAL_RESOLVERS} valid=30s ipv6=off;

  root /usr/share/nginx/html;
  index index.html;

  location / {
    try_files $uri $uri/ /index.html;
  }

  location = /health {
    access_log off;
    add_header Content-Type application/json;
    return 200 '{"status":"ok","component":"dashboard"}';
  }

  location = /api/team {
    default_type application/json;
    return 200 '` + directory + `';
  }

  location ~ ^/api/agents/` + regexp.QuoteMeta(agent.Name) + `/(.*)$ {
    proxy_pass ` + upstream + `/$1$is_args$args;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_http_version 1.1;
    proxy_buffering off;
    proxy_read_timeout 310s;
    client_max_body_size 1m;
  }

  location /api/ {
    return 404;
  }
}
`

	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      agent.Name + "-dashboard-nginx",
			Namespace: agent.Namespace,
			Labels:    dashboardLabels(agent),
		},
		Data: map[string]string{
			"default.conf.template": tpl,
		},
	}
}

// buildDashboardDeployment returns the Deployment for the per-agent Vue 3
// dashboard, or nil when the dashboard is disabled. The dashboard container
// always listens on 8080; the Service port is controlled separately via
// DashboardSpec.Port. Per-agent nginx routing comes from the sibling
// ConfigMap built by buildDashboardConfigMap — the browser talks to the
// dashboard pod, which proxies to the owned agent's harness directly. No
// HARNESS_URL env var is required (that was the legacy /api/* catch-all
// path retired in beta.46).
func buildDashboardDeployment(agent *nyxv1alpha1.NyxAgent, appVersion string) *appsv1.Deployment {
	d := agent.Spec.Dashboard
	if d == nil || !d.Enabled {
		return nil
	}

	replicas := int32(1)
	if d.Replicas != nil {
		replicas = *d.Replicas
	}

	// Image — fall back to the conventional ghcr image when the caller
	// didn't specify one, so the simplest NyxAgent with
	// `dashboard.enabled: true` still produces a deployable pod.
	img := nyxv1alpha1.ImageSpec{
		Repository: "ghcr.io/skthomasjr/images/dashboard",
	}
	if d.Image != nil {
		img = *d.Image
	}

	probeSpec := nyxv1alpha1.ProbeSpec{}
	containerPort := int32(8080)

	nginxTemplateVol := agent.Name + "-dashboard-nginx"

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      agent.Name + "-dashboard",
			Namespace: agent.Namespace,
			Labels:    dashboardLabels(agent),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(replicas),
			Selector: &metav1.LabelSelector{
				MatchLabels: dashboardSelectorLabels(agent),
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: dashboardLabels(agent),
				},
				Spec: corev1.PodSpec{
					AutomountServiceAccountToken: boolPtr(false),
					ImagePullSecrets:             agent.Spec.ImagePullSecrets,
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: boolPtr(true),
						SeccompProfile: &corev1.SeccompProfile{
							Type: corev1.SeccompProfileTypeRuntimeDefault,
						},
					},
					Volumes: []corev1.Volume{{
						Name: nginxTemplateVol,
						VolumeSource: corev1.VolumeSource{
							ConfigMap: &corev1.ConfigMapVolumeSource{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: agent.Name + "-dashboard-nginx",
								},
							},
						},
					}},
					Containers: []corev1.Container{{
						Name:            "dashboard",
						Image:           imageRef(img, appVersion),
						ImagePullPolicy: imagePullPolicy(img),
						Ports: []corev1.ContainerPort{{
							Name:          "http",
							ContainerPort: containerPort,
							Protocol:      corev1.ProtocolTCP,
						}},
						Env: []corev1.EnvVar{{
							// Enables the nginx-unprivileged image's local-
							// resolvers envsh hook so NGINX_LOCAL_RESOLVERS
							// gets populated from /etc/resolv.conf at boot
							// — required for the resolver directive above.
							Name:  "NGINX_ENTRYPOINT_LOCAL_RESOLVERS",
							Value: "1",
						}},
						VolumeMounts: []corev1.VolumeMount{{
							Name:      nginxTemplateVol,
							MountPath: "/etc/nginx/templates",
							ReadOnly:  true,
						}},
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: boolPtr(false),
							Capabilities: &corev1.Capabilities{
								Drop: []corev1.Capability{"ALL"},
							},
							RunAsNonRoot: boolPtr(true),
						},
						LivenessProbe:  httpProbe(containerPort, "/health", probeDefaults(&probeSpec, true)),
						ReadinessProbe: httpProbe(containerPort, "/health", probeDefaults(&probeSpec, false)),
						Resources:      d.Resources,
					}},
				},
			},
		},
	}
}

// buildDashboardService returns the ClusterIP Service for the dashboard,
// or nil when disabled. The Service port defaults to 80 (what users expect
// to hit in-cluster); targetPort is always 8080 to match the nginx listen.
func buildDashboardService(agent *nyxv1alpha1.NyxAgent) *corev1.Service {
	d := agent.Spec.Dashboard
	if d == nil || !d.Enabled {
		return nil
	}
	port := d.Port
	if port == 0 {
		port = 80
	}
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      agent.Name + "-dashboard",
			Namespace: agent.Namespace,
			Labels:    dashboardLabels(agent),
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: dashboardSelectorLabels(agent),
			Ports: []corev1.ServicePort{{
				Name:       "http",
				Port:       port,
				TargetPort: intstr.FromInt(8080),
			}},
		},
	}
}

// ── Team manifest ─────────────────────────────────────────────────────────────
//
// The manifest ConfigMap lists every NyxAgent that shares a team (or
// namespace when no team label is set) so each harness can route /team
// and /proxy/{name} requests to peers by name. This is the operator's
// equivalent of charts/nyx/templates/configmap-manifest.yaml (#474).
//
// Design note (option a in the gap-approve comment): each reconcile
// recomputes the per-team manifest and writes a CM owned by the agent
// currently being reconciled. A content-hash short-circuit (below)
// avoids churning the CM — and therefore the mounted ConfigMap in every
// pod — when membership is unchanged.

// teamKey returns the value of the team label on an agent, or an empty
// string when the label is absent. Agents with the same team key share
// a manifest; agents without the label are grouped per-namespace.
func teamKey(agent *nyxv1alpha1.NyxAgent) string {
	if agent.Labels == nil {
		return ""
	}
	return agent.Labels[teamLabel]
}

// manifestConfigMapName is the name of the per-team manifest CM for a
// given agent. When the team label is set we include its value so
// multiple teams within a namespace each get their own manifest;
// otherwise we fall back to the namespace-level manifest.
func manifestConfigMapName(agent *nyxv1alpha1.NyxAgent) string {
	if t := teamKey(agent); t != "" {
		return fmt.Sprintf("nyx-manifest-%s", t)
	}
	return "nyx-manifest"
}

// manifestLabels labels the manifest CM so it can be listed without
// colliding with per-agent owned ConfigMaps. The team key (if any) is
// included so the owning reconcile can short-circuit on a label-
// selector list call.
func manifestLabels(team string) map[string]string {
	l := map[string]string{
		labelComponent: componentManifest,
		labelPartOf:    partOf,
		labelManagedBy: managedBy,
	}
	if team != "" {
		l[teamLabel] = team
	}
	return l
}

// manifestMember is one entry in the rendered manifest.json file. The
// shape matches the chart's manifest exactly (name + URL) so the
// harness parser doesn't care which rendering path produced it.
type manifestMember struct {
	Name string
	Port int32
}

// buildManifestJSON renders the manifest.json payload for a fixed set
// of members. Members are sorted by name for deterministic output,
// which is what the hash short-circuit below relies on. The URL shape
// mirrors the chart: in-cluster service DNS `http://<name>:<port>`.
func buildManifestJSON(members []manifestMember) string {
	sort.Slice(members, func(i, j int) bool { return members[i].Name < members[j].Name })
	var b strings.Builder
	b.WriteString("{\n  \"team\": [")
	for i, m := range members {
		if i == 0 {
			b.WriteString("\n")
		}
		port := m.Port
		if port == 0 {
			port = 8000
		}
		fmt.Fprintf(&b, "    {\n      \"name\": %q,\n      \"url\": \"http://%s:%d\"\n    }",
			m.Name, m.Name, port)
		if i < len(members)-1 {
			b.WriteString(",\n")
		} else {
			b.WriteString("\n  ")
		}
	}
	if len(members) == 0 {
		b.WriteString("]\n}\n")
	} else {
		b.WriteString("]\n}\n")
	}
	return b.String()
}

// manifestContentHash returns a short stable hash of the manifest JSON
// used as an annotation on the rendered CM. The reconciler uses it to
// skip writes when the desired payload matches what's already in the
// cluster, so a membership-unchanged reconcile doesn't churn the
// mounted volume on every harness pod.
func manifestContentHash(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:8])
}

// manifestHashAnnotation is the annotation key used to stamp the
// content hash onto the CM. The ConfigMap list in the reconciler
// reads this to short-circuit equal-content reconciles.
const manifestHashAnnotation = "nyx.ai/manifest-hash"

// buildManifestConfigMap assembles the per-team manifest ConfigMap
// covering the given members. `ownerAgent` is the NyxAgent whose
// reconcile is producing the CM — it becomes the controller owner so
// OwnerReferences GC still works when the last team member is
// deleted. Returns the CM plus the hex hash so the caller can
// annotate + compare.
func buildManifestConfigMap(ownerAgent *nyxv1alpha1.NyxAgent, members []manifestMember) (*corev1.ConfigMap, string) {
	body := buildManifestJSON(members)
	hash := manifestContentHash(body)
	team := teamKey(ownerAgent)
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      manifestConfigMapName(ownerAgent),
			Namespace: ownerAgent.Namespace,
			Labels:    manifestLabels(team),
			Annotations: map[string]string{
				manifestHashAnnotation: hash,
			},
		},
		Data: map[string]string{
			manifestSubPath: body,
		},
	}, hash
}

// manifestVolumeAndMount returns the pod Volume + harness VolumeMount
// that surface the per-team manifest CM at the path the chart uses.
// Always attached — an empty CM still renders a valid `{"team": []}`
// payload, so the harness's startup parse never breaks.
func manifestVolumeAndMount(cmName string) (corev1.Volume, corev1.VolumeMount) {
	return corev1.Volume{
			Name: manifestVolumeName,
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: cmName},
				},
			},
		}, corev1.VolumeMount{
			Name:      manifestVolumeName,
			MountPath: manifestMountPath,
			SubPath:   manifestSubPath,
			ReadOnly:  true,
		}
}
