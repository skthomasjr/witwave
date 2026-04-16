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
	"fmt"
	"sort"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	nyxv1alpha1 "github.com/nyx-ai/nyx-operator/api/v1alpha1"
)

// Label keys used for every resource the operator creates.
const (
	labelName      = "app.kubernetes.io/name"
	labelComponent = "app.kubernetes.io/component"
	labelPartOf    = "app.kubernetes.io/part-of"
	labelManagedBy = "app.kubernetes.io/managed-by"

	componentAgent   = "agent"
	componentBackend = "backend"
	partOf           = "nyx"
	managedBy        = "nyx-operator"

	// sharedStorageVolume is the pod-level volume name used when the agent
	// references a pre-existing shared PVC. All containers mount it.
	sharedStorageVolume = "shared-storage"

	// agentConfigVolumePrefix is the prefix for per-agent/backend inline
	// config ConfigMap volume names.
	agentConfigVolumePrefix = "agent-config-"
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
		ImagePullPolicy: agent.Spec.Image.PullPolicy,
		Ports: []corev1.ContainerPort{
			{Name: "http", ContainerPort: harnessPort},
		},
		Env: append([]corev1.EnvVar{
			{Name: "AGENT_NAME", Value: agent.Name},
			{Name: "AGENT_PORT", Value: fmt.Sprintf("%d", harnessPort)},
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
	}

	// Backend containers.
	containers := []corev1.Container{harness}
	// Sort backends by name for a deterministic rendering.
	backends := append([]nyxv1alpha1.BackendSpec(nil), agent.Spec.Backends...)
	sort.Slice(backends, func(i, j int) bool { return backends[i].Name < backends[j].Name })
	for _, b := range backends {
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
			ImagePullPolicy: b.Image.PullPolicy,
			Ports: []corev1.ContainerPort{
				{Name: "http", ContainerPort: bPort},
			},
			Env: append([]corev1.EnvVar{
				{Name: "AGENT_NAME", Value: fmt.Sprintf("%s-a2-%s", agent.Name, b.Name)},
				{Name: "AGENT_OWNER", Value: agent.Name},
				{Name: "AGENT_ID", Value: b.Name},
				{Name: "BACKEND_PORT", Value: fmt.Sprintf("%d", bPort)},
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

	podSpec := corev1.PodSpec{
		TerminationGracePeriodSeconds: int64Ptr(60),
		AutomountServiceAccountToken:  boolPtr(false),
		SecurityContext: &corev1.PodSecurityContext{
			RunAsNonRoot: boolPtr(true),
			RunAsUser:    int64Ptr(1000),
			RunAsGroup:   int64Ptr(1000),
			FSGroup:      int64Ptr(1000),
		},
		ImagePullSecrets: agent.Spec.ImagePullSecrets,
		Containers:       containers,
		Volumes:          volumes,
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
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec:       podSpec,
			},
		},
	}
}

// buildService constructs the ClusterIP Service exposing the agent's
// nyx-harness HTTP port. Prometheus scrape annotations are added when metrics
// are enabled.
func buildService(agent *nyxv1alpha1.NyxAgent) *corev1.Service {
	port := agent.Spec.Port
	if port == 0 {
		port = 8000
	}
	annotations := map[string]string{}
	if agent.Spec.Metrics.Enabled {
		annotations["prometheus.io/scrape"] = "true"
		annotations["prometheus.io/port"] = fmt.Sprintf("%d", port)
		annotations["prometheus.io/path"] = "/metrics"
	}
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:        agent.Name,
			Namespace:   agent.Namespace,
			Labels:      agentLabels(agent),
			Annotations: annotations,
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: selectorLabels(agent),
			Ports: []corev1.ServicePort{{
				Name:       "http",
				Port:       port,
				TargetPort: intstr.FromInt(int(port)),
			}},
		},
	}
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

// buildBackendPVCs returns the PVCs the operator should create for each
// backend whose Storage.Enabled is true and whose ExistingClaim is empty.
func buildBackendPVCs(agent *nyxv1alpha1.NyxAgent) []*corev1.PersistentVolumeClaim {
	var out []*corev1.PersistentVolumeClaim
	for _, b := range agent.Spec.Backends {
		if b.Storage == nil || !b.Storage.Enabled || b.Storage.ExistingClaim != "" {
			continue
		}
		size := b.Storage.Size
		if size == "" {
			size = "1Gi"
		}
		qty, err := resource.ParseQuantity(size)
		if err != nil {
			// Skip malformed entries; validation will surface this elsewhere.
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
	return out
}

// ── small helpers ─────────────────────────────────────────────────────────────

func boolPtr(b bool) *bool    { return &b }
func int32Ptr(i int32) *int32 { return &i }
func int64Ptr(i int64) *int64 { return &i }
