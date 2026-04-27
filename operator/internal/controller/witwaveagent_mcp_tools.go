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
	"context"
	"errors"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	witwavev1alpha1 "github.com/witwave-ai/witwave-operator/api/v1alpha1"
)

// mcpToolPort is the single port every MCP tool container binds to
// (AGENTS.md, tools/*/Dockerfile). Exposing a constant keeps the renderer
// free of magic numbers and makes parity with the chart
// (`charts/witwave/templates/mcp-tools.yaml`) obvious at a glance.
const mcpToolPort int32 = 8000

// defaultMCPToolImages maps each tool key to the canonical ghcr.io image.
// Scaffold scope (#830): if a consumer omits spec.mcpTools.<name>.image or
// omits just image.repository, we supply the same repo the chart defaults
// to so operator-only installs and chart installs render identical pods.
var defaultMCPToolImages = map[string]string{
	"kubernetes": "ghcr.io/witwave-ai/images/mcp-kubernetes",
	"helm":       "ghcr.io/witwave-ai/images/mcp-helm",
	"prometheus": "ghcr.io/witwave-ai/images/mcp-prometheus", // #1354, #1556
}

// reconcileMCPTools walks every tool the spec asks for and reconciles a
// Deployment + Service pair. Tools with Enabled=false (or removed
// entirely from the spec) are GC'd via a label-scoped list + IsControlledBy
// filter so the operator no longer stranded leftover tool Deployments /
// Services after a tool was disabled (#1172).
func (r *WitwaveAgentReconciler) reconcileMCPTools(ctx context.Context, agent *witwavev1alpha1.WitwaveAgent) error {
	// Compute the desired (enabled) set first. When spec.mcpTools is
	// nil we still run the cleanup pass so a tool that was deleted
	// from the CR gets torn down.
	//
	// Agent-disabled short-circuit (#1635): when the parent CR has
	// spec.enabled=false, the teardown path invokes this reconciler so
	// every owned mcp-<tool> Deployment + Service gets cleaned up. We
	// keep `desired` empty in that case so the cleanup pass below
	// reaps every IsControlledBy match — even tools whose nested
	// Enabled flag is still true.
	desired := map[string]*witwavev1alpha1.MCPToolSpec{}
	agentDisabled := agent.Spec.Enabled != nil && !*agent.Spec.Enabled
	if !agentDisabled && agent.Spec.MCPTools != nil {
		candidates := map[string]*witwavev1alpha1.MCPToolSpec{
			"kubernetes": agent.Spec.MCPTools.Kubernetes,
			"helm":       agent.Spec.MCPTools.Helm,
			"prometheus": agent.Spec.MCPTools.Prometheus, // #1354
		}
		for name, tool := range candidates {
			if tool != nil && tool.Enabled {
				desired[name] = tool
			}
		}
	}

	var errs []error
	for name, tool := range desired {
		if err := r.applyMCPToolDeployment(ctx, agent, name, tool); err != nil {
			errs = append(errs, fmt.Errorf("mcp-%s deployment: %w", name, err))
		}
		if err := r.applyMCPToolService(ctx, agent, name); err != nil {
			errs = append(errs, fmt.Errorf("mcp-%s service: %w", name, err))
		}
	}

	// Cleanup: list Deployments + Services labelled by THIS agent's
	// part-of/managed-by markers and scoped to the mcp- component
	// prefix, then delete anything that's either for a not-desired
	// tool or genuinely orphaned. IsControlledBy is checked before
	// every Delete so foreign-owned objects (another WitwaveAgent, or a
	// chart-managed sibling) are never touched — mirrors the
	// reconcileConfigMaps pattern (line ~766).
	if cleanupErr := r.cleanupMCPTools(ctx, agent, desired); cleanupErr != nil {
		errs = append(errs, cleanupErr)
	}
	return errors.Join(errs...)
}

// cleanupMCPTools deletes MCP-tool Deployments/Services owned by this
// WitwaveAgent whose tool name is not in the desired (enabled) set.
func (r *WitwaveAgentReconciler) cleanupMCPTools(ctx context.Context, agent *witwavev1alpha1.WitwaveAgent, desired map[string]*witwavev1alpha1.MCPToolSpec) error {
	// Only filter by labels we know every MCP-tool resource carries.
	// The per-tool `labelComponent` value differs (mcp-kubernetes vs
	// mcp-helm), so we match by partOf + managedBy + the agent name
	// and then filter by component-prefix in memory.
	selector := client.MatchingLabels{
		labelName:      "",
		labelPartOf:    partOf,
		labelManagedBy: managedBy,
	}
	// labelName is per-resource (mcpToolName), not the agent's own
	// name — drop it from the selector so the List actually matches
	// every mcp-tool child.
	delete(selector, labelName)

	deps := &appsv1.DeploymentList{}
	if err := r.List(ctx, deps, client.InNamespace(agent.Namespace), selector); err != nil {
		return fmt.Errorf("list MCP-tool Deployments for cleanup: %w", err)
	}
	svcs := &corev1.ServiceList{}
	if err := r.List(ctx, svcs, client.InNamespace(agent.Namespace), selector); err != nil {
		return fmt.Errorf("list MCP-tool Services for cleanup: %w", err)
	}

	toolNameFromComponent := func(component string) (string, bool) {
		const prefix = "mcp-"
		if len(component) <= len(prefix) || component[:len(prefix)] != prefix {
			return "", false
		}
		return component[len(prefix):], true
	}

	var errs []error
	for i := range deps.Items {
		d := &deps.Items[i]
		if !metav1.IsControlledBy(d, agent) {
			continue
		}
		tool, ok := toolNameFromComponent(d.Labels[labelComponent])
		if !ok {
			continue
		}
		if _, wanted := desired[tool]; wanted {
			continue
		}
		if err := r.Delete(ctx, d); err != nil && !apierrors.IsNotFound(err) {
			errs = append(errs, fmt.Errorf("delete stranded mcp-%s Deployment: %w", tool, err))
		}
	}
	for i := range svcs.Items {
		s := &svcs.Items[i]
		if !metav1.IsControlledBy(s, agent) {
			continue
		}
		tool, ok := toolNameFromComponent(s.Labels[labelComponent])
		if !ok {
			continue
		}
		if _, wanted := desired[tool]; wanted {
			continue
		}
		if err := r.Delete(ctx, s); err != nil && !apierrors.IsNotFound(err) {
			errs = append(errs, fmt.Errorf("delete stranded mcp-%s Service: %w", tool, err))
		}
	}
	return errors.Join(errs...)
}

// mcpToolName returns the `<agent>-mcp-<tool>` DNS-1123 name the chart
// uses for both the Deployment and the Service.
func mcpToolName(agent *witwavev1alpha1.WitwaveAgent, tool string) string {
	return fmt.Sprintf("%s-mcp-%s", agent.Name, tool)
}

func mcpToolLabels(agent *witwavev1alpha1.WitwaveAgent, tool string) map[string]string {
	return map[string]string{
		labelName:      mcpToolName(agent, tool),
		labelComponent: fmt.Sprintf("mcp-%s", tool),
		labelPartOf:    partOf,
		labelManagedBy: managedBy,
	}
}

func mcpToolSelector(agent *witwavev1alpha1.WitwaveAgent, tool string) map[string]string {
	return map[string]string{
		labelName: mcpToolName(agent, tool),
	}
}

// resolveMCPToolImage picks an ImageSpec for the named tool, filling in
// the canonical default repository when the spec omits it. The caller's
// fallback tag (the harness DefaultImageTag) is used only when Tag is
// empty — mirrors the buildDeployment helper's posture for other pods.
func resolveMCPToolImage(tool string, spec *witwavev1alpha1.MCPToolSpec, fallbackTag string) witwavev1alpha1.ImageSpec {
	if spec != nil && spec.Image != nil {
		img := *spec.Image
		if img.Repository == "" {
			img.Repository = defaultMCPToolImages[tool]
		}
		return img
	}
	return witwavev1alpha1.ImageSpec{Repository: defaultMCPToolImages[tool]}
}

func (r *WitwaveAgentReconciler) applyMCPToolDeployment(ctx context.Context, agent *witwavev1alpha1.WitwaveAgent, tool string, spec *witwavev1alpha1.MCPToolSpec) error {
	// #1173: honour an explicit Replicas=0 (pause the tool without
	// removing its Deployment). Previously any zero or negative value
	// was coerced to 1, so operators couldn't drain a tool during
	// cluster maintenance without also disabling it. The CRD caps
	// Replicas at Minimum=0, so negative values can't land here.
	replicas := int32(1)
	if spec.Replicas != nil {
		replicas = *spec.Replicas
	}
	img := resolveMCPToolImage(tool, spec, DefaultImageTag)
	labels := mcpToolLabels(agent, tool)
	selector := mcpToolSelector(agent, tool)

	desired := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mcpToolName(agent, tool),
			Namespace: agent.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: selector},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					// #1737: SA + token + image-pull-secrets parity with
					// the chart. ServiceAccountName / ImagePullSecrets are
					// passthrough; AutomountServiceAccountToken defaults
					// to true (Kubernetes default) when nil so a
					// chart-equivalent posture is preserved.
					ServiceAccountName:           spec.ServiceAccountName,
					AutomountServiceAccountToken: mcpToolAutomountSAToken(spec),
					ImagePullSecrets:             spec.ImagePullSecrets,
					// #1737: pod-level hardened default. PSS-restricted
					// admission rejects pods missing runAsNonRoot /
					// seccompProfile; matches the chart's pod
					// securityContext block in mcp-tools.yaml.
					SecurityContext: mcpToolPodSecurityContext(spec),
					Containers: []corev1.Container{{
						Name:            fmt.Sprintf("mcp-%s", tool),
						Image:           imageRef(img, DefaultImageTag),
						ImagePullPolicy: img.PullPolicy,
						Ports: []corev1.ContainerPort{
							{
								Name:          "http",
								ContainerPort: mcpToolPort,
								Protocol:      corev1.ProtocolTCP,
							},
							{
								// #1339: metrics port for PodMonitor/ServiceMonitor
								// discovery (chart parity).
								Name:          "metrics",
								ContainerPort: 9000,
								Protocol:      corev1.ProtocolTCP,
							},
						},
						// #1331: project MCP_TOOL_AUTH_TOKEN from
						// AuthTokenSecretRef when set, or signal
						// explicit dev-mode via MCP_TOOL_AUTH_DISABLED.
						// #1339: also set METRICS_ENABLED + METRICS_PORT
						// so the container's /metrics listener starts.
						Env: mcpToolEnv(spec),
						// Readiness probe posture (#1173): Kubernetes does
						// NOT expand env vars inside HTTPGet.HTTPHeaders, so
						// we can't stamp `Authorization: Bearer $(MCP_TOOL_AUTH_TOKEN)`
						// here and expect it to resolve at kubelet time. The
						// operator also has no visibility into the per-tool
						// auth token (MCPToolSpec is intentionally scaffold-
						// scoped — Enabled, Image, Replicas only), so we
						// cannot embed a plaintext header either. By
						// contract (AGENTS.md: `shared/mcp_auth.py` and the
						// chart's `/health` path) the MCP tool server
						// whitelists `/health` without auth precisely so
						// kubelet probes succeed under MCP_TOOL_AUTH_TOKEN.
						// If that contract changes, either expose a token
						// ref on MCPToolSpec or switch probes to TCP.
						ReadinessProbe: mcpToolReadinessProbe(spec),
						LivenessProbe:  mcpToolLivenessProbe(spec),
						Resources:      mcpToolResources(spec),
						// #1737: container-level hardened default —
						// AllowPrivilegeEscalation=false +
						// Capabilities.Drop=ALL + RunAsNonRoot=true +
						// ReadOnlyRootFilesystem=true +
						// SeccompProfile=RuntimeDefault. Mirrors the
						// chart's witwave.hardenedContainerSecurityContext
						// helper. The /tmp + /home/agent/.cache emptyDir
						// volumes below are the carve-out that lets
						// readOnlyRootFilesystem=true coexist with
						// helm-CLI cache and Python tempfile writes
						// (chart parity #1073).
						SecurityContext: mcpToolContainerSecurityContext(spec),
						VolumeMounts:    mcpToolVolumeMounts(),
					}},
					Volumes: mcpToolVolumes(),
				},
			},
		},
	}
	if err := controllerutil.SetControllerReference(agent, desired, r.Scheme); err != nil {
		return fmt.Errorf("set owner on mcp-%s Deployment: %w", tool, err)
	}
	// Use SSA (#751) so multi-owner coexistence works out of the box on
	// the MCP render path too; the agent controller owns only the fields
	// it stamps, leaving HPA / GitOps free to claim others.
	return applySSA(ctx, r.Client, desired)
}

// mcpToolReadinessProbe returns the user's override if set, else a
// sensible default that tolerates cold-start kube discovery (#1353).
func mcpToolReadinessProbe(spec *witwavev1alpha1.MCPToolSpec) *corev1.Probe {
	if spec != nil && spec.ReadinessProbe != nil {
		return spec.ReadinessProbe
	}
	return &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			HTTPGet: &corev1.HTTPGetAction{
				Path: "/health",
				Port: intstr.FromInt(int(mcpToolPort)),
			},
		},
		// #1353 defaults: absorb ~10s kube-discovery cold start.
		InitialDelaySeconds: 5,
		PeriodSeconds:       10,
		TimeoutSeconds:      3,
		FailureThreshold:    6,
		SuccessThreshold:    1,
	}
}

// mcpToolLivenessProbe returns the user's override or nil (no liveness
// by default — readiness alone handles cold-start churn). (#1353)
func mcpToolLivenessProbe(spec *witwavev1alpha1.MCPToolSpec) *corev1.Probe {
	if spec != nil && spec.LivenessProbe != nil {
		return spec.LivenessProbe
	}
	return nil
}

// mcpToolResources returns the user's override or a sensible default
// that gets the pod out of BestEffort QoS (#1353).
func mcpToolResources(spec *witwavev1alpha1.MCPToolSpec) corev1.ResourceRequirements {
	if spec != nil && spec.Resources != nil {
		return *spec.Resources
	}
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("50m"),
			corev1.ResourceMemory: resource.MustParse("64Mi"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("500m"),
			corev1.ResourceMemory: resource.MustParse("256Mi"),
		},
	}
}

// mcpToolEnv builds the env slice for an MCP tool container. Projects
// MCP_TOOL_AUTH_TOKEN from AuthTokenSecretRef when set; or stamps
// MCP_TOOL_AUTH_DISABLED=true when the operator explicitly opted out.
// When neither is configured, returns nil — mcp_auth middleware then
// fails closed on every request (visible via backend_mcp_command_rejected_total).
func mcpToolEnv(spec *witwavev1alpha1.MCPToolSpec) []corev1.EnvVar {
	if spec == nil {
		return nil
	}
	var env []corev1.EnvVar
	if spec.AuthTokenSecretRef != nil {
		env = append(env, corev1.EnvVar{
			Name: "MCP_TOOL_AUTH_TOKEN",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: spec.AuthTokenSecretRef,
			},
		})
	} else if spec.AuthDisabled {
		env = append(env, corev1.EnvVar{
			Name:  "MCP_TOOL_AUTH_DISABLED",
			Value: "true",
		})
	}
	// #1339: enable the metrics listener so Prometheus scrape via
	// PodMonitor/ServiceMonitor finds operator-managed MCP tools too.
	env = append(env,
		corev1.EnvVar{Name: "METRICS_ENABLED", Value: "true"},
		corev1.EnvVar{Name: "METRICS_PORT", Value: "9000"},
	)
	return env
}

// buildMCPToolService renders the desired Service for an agent's MCP tool.
// Extracted so unit tests can pin the port set without spinning the
// reconciler.
func buildMCPToolService(agent *witwavev1alpha1.WitwaveAgent, tool string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mcpToolName(agent, tool),
			Namespace: agent.Namespace,
			Labels:    mcpToolLabels(agent, tool),
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: mcpToolSelector(agent, tool),
			Ports: []corev1.ServicePort{
				{
					Name:       "http",
					Port:       mcpToolPort,
					TargetPort: intstr.FromInt(int(mcpToolPort)),
					Protocol:   corev1.ProtocolTCP,
				},
				// #1722: metrics ServicePort matches chart parity
				// (charts/witwave/templates/mcp-tools.yaml). The
				// applyMCPToolDeployment path already declares the
				// container "metrics" port and METRICS_ENABLED=true env
				// (#1336/#1339), so a ServiceMonitor can resolve the
				// scrape target by name.
				{
					Name:       "metrics",
					Port:       9000,
					TargetPort: intstr.FromString("metrics"),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}
}

// mcpToolAutomountSAToken returns the three-state pod-level
// AutomountServiceAccountToken value (#1737). When unset on the spec
// the operator defaults to true so the SA token mounts (Kubernetes
// default; chart parity). Operators using IRSA / workload-identity
// can set false to suppress the in-pod token mount while keeping the
// SA attached for IAM role metadata.
func mcpToolAutomountSAToken(spec *witwavev1alpha1.MCPToolSpec) *bool {
	if spec != nil && spec.AutomountServiceAccountToken != nil {
		return spec.AutomountServiceAccountToken
	}
	return boolPtr(true)
}

// mcpToolPodSecurityContext returns the user-supplied pod
// SecurityContext or a hardened default (RunAsNonRoot=true,
// RunAsUser/Group=1000, FSGroup=1000, SeccompProfile=RuntimeDefault).
// Mirrors the chart's pod securityContext block in
// charts/witwave/templates/mcp-tools.yaml (#1737).
func mcpToolPodSecurityContext(spec *witwavev1alpha1.MCPToolSpec) *corev1.PodSecurityContext {
	if spec != nil && spec.PodSecurityContext != nil {
		return spec.PodSecurityContext
	}
	return &corev1.PodSecurityContext{
		RunAsNonRoot: boolPtr(true),
		RunAsUser:    int64Ptr(1000),
		RunAsGroup:   int64Ptr(1000),
		FSGroup:      int64Ptr(1000),
		SeccompProfile: &corev1.SeccompProfile{
			Type: corev1.SeccompProfileTypeRuntimeDefault,
		},
	}
}

// mcpToolContainerSecurityContext returns the user-supplied container
// SecurityContext or a hardened default mirroring the chart's
// witwave.hardenedContainerSecurityContext helper: drop ALL caps,
// disable privilege escalation, run as non-root, mount root FS read-only,
// seccompProfile=RuntimeDefault (#1737).
func mcpToolContainerSecurityContext(spec *witwavev1alpha1.MCPToolSpec) *corev1.SecurityContext {
	if spec != nil && spec.SecurityContext != nil {
		return spec.SecurityContext
	}
	return &corev1.SecurityContext{
		AllowPrivilegeEscalation: boolPtr(false),
		Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		ReadOnlyRootFilesystem:   boolPtr(true),
		RunAsNonRoot:             boolPtr(true),
		SeccompProfile: &corev1.SeccompProfile{
			Type: corev1.SeccompProfileTypeRuntimeDefault,
		},
	}
}

// mcpToolVolumeMounts returns the emptyDir mounts that let
// readOnlyRootFilesystem=true coexist with helm-CLI cache and Python
// tempfile writes (chart parity #1073, #1737).
func mcpToolVolumeMounts() []corev1.VolumeMount {
	return []corev1.VolumeMount{
		{Name: "witwave-tmp", MountPath: "/tmp"},
		{Name: "witwave-home-cache", MountPath: "/home/agent/.cache"},
	}
}

// mcpToolVolumes returns the pod-level emptyDir volumes paired with
// mcpToolVolumeMounts (#1737).
func mcpToolVolumes() []corev1.Volume {
	return []corev1.Volume{
		{Name: "witwave-tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		{Name: "witwave-home-cache", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
	}
}

func (r *WitwaveAgentReconciler) applyMCPToolService(ctx context.Context, agent *witwavev1alpha1.WitwaveAgent, tool string) error {
	desired := buildMCPToolService(agent, tool)
	if err := controllerutil.SetControllerReference(agent, desired, r.Scheme); err != nil {
		return fmt.Errorf("set owner on mcp-%s Service: %w", tool, err)
	}
	// ClusterIP-preservation parity with applyService.
	existing := &corev1.Service{}
	switch err := r.Get(ctx, client.ObjectKeyFromObject(desired), existing); {
	case apierrors.IsNotFound(err):
	case err != nil:
		return err
	default:
		desired.Spec.ClusterIP = existing.Spec.ClusterIP
	}
	return applySSA(ctx, r.Client, desired)
}
