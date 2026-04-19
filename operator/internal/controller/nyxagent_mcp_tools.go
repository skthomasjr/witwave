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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	nyxv1alpha1 "github.com/nyx-ai/nyx-operator/api/v1alpha1"
)

// mcpToolPort is the single port every MCP tool container binds to
// (AGENTS.md, tools/*/Dockerfile). Exposing a constant keeps the renderer
// free of magic numbers and makes parity with the chart
// (`charts/nyx/templates/mcp-tools.yaml`) obvious at a glance.
const mcpToolPort int32 = 8000

// defaultMCPToolImages maps each tool key to the canonical ghcr.io image.
// Scaffold scope (#830): if a consumer omits spec.mcpTools.<name>.image or
// omits just image.repository, we supply the same repo the chart defaults
// to so operator-only installs and chart installs render identical pods.
var defaultMCPToolImages = map[string]string{
	"kubernetes": "ghcr.io/skthomasjr/images/mcp-kubernetes",
	"helm":       "ghcr.io/skthomasjr/images/mcp-helm",
}

// reconcileMCPTools walks every tool the spec asks for and reconciles a
// Deployment + Service pair. Tools with Enabled=false are skipped (delete
// path is a follow-up; chart-driven lifecycle remains the source of truth
// for operator-managed clusters running mcpTools in the chart alongside
// the operator).
func (r *NyxAgentReconciler) reconcileMCPTools(ctx context.Context, agent *nyxv1alpha1.NyxAgent) error {
	if agent.Spec.MCPTools == nil {
		return nil
	}
	tools := map[string]*nyxv1alpha1.MCPToolSpec{
		"kubernetes": agent.Spec.MCPTools.Kubernetes,
		"helm":       agent.Spec.MCPTools.Helm,
	}
	var errs []error
	for name, tool := range tools {
		if tool == nil || !tool.Enabled {
			continue
		}
		if err := r.applyMCPToolDeployment(ctx, agent, name, tool); err != nil {
			errs = append(errs, fmt.Errorf("mcp-%s deployment: %w", name, err))
		}
		if err := r.applyMCPToolService(ctx, agent, name); err != nil {
			errs = append(errs, fmt.Errorf("mcp-%s service: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

// mcpToolName returns the `<agent>-mcp-<tool>` DNS-1123 name the chart
// uses for both the Deployment and the Service.
func mcpToolName(agent *nyxv1alpha1.NyxAgent, tool string) string {
	return fmt.Sprintf("%s-mcp-%s", agent.Name, tool)
}

func mcpToolLabels(agent *nyxv1alpha1.NyxAgent, tool string) map[string]string {
	return map[string]string{
		labelName:      mcpToolName(agent, tool),
		labelComponent: fmt.Sprintf("mcp-%s", tool),
		labelPartOf:    partOf,
		labelManagedBy: managedBy,
	}
}

func mcpToolSelector(agent *nyxv1alpha1.NyxAgent, tool string) map[string]string {
	return map[string]string{
		labelName: mcpToolName(agent, tool),
	}
}

// resolveMCPToolImage picks an ImageSpec for the named tool, filling in
// the canonical default repository when the spec omits it. The caller's
// fallback tag (the harness DefaultImageTag) is used only when Tag is
// empty — mirrors the buildDeployment helper's posture for other pods.
func resolveMCPToolImage(tool string, spec *nyxv1alpha1.MCPToolSpec, fallbackTag string) nyxv1alpha1.ImageSpec {
	if spec != nil && spec.Image != nil {
		img := *spec.Image
		if img.Repository == "" {
			img.Repository = defaultMCPToolImages[tool]
		}
		return img
	}
	return nyxv1alpha1.ImageSpec{Repository: defaultMCPToolImages[tool]}
}

func (r *NyxAgentReconciler) applyMCPToolDeployment(ctx context.Context, agent *nyxv1alpha1.NyxAgent, tool string, spec *nyxv1alpha1.MCPToolSpec) error {
	replicas := int32(1)
	if spec.Replicas != nil && *spec.Replicas > 0 {
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
					Containers: []corev1.Container{{
						Name:            fmt.Sprintf("mcp-%s", tool),
						Image:           imageRef(img, DefaultImageTag),
						ImagePullPolicy: img.PullPolicy,
						Ports: []corev1.ContainerPort{{
							Name:          "http",
							ContainerPort: mcpToolPort,
							Protocol:      corev1.ProtocolTCP,
						}},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								HTTPGet: &corev1.HTTPGetAction{
									Path: "/health",
									Port: intstr.FromInt(int(mcpToolPort)),
								},
							},
						},
					}},
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

func (r *NyxAgentReconciler) applyMCPToolService(ctx context.Context, agent *nyxv1alpha1.NyxAgent, tool string) error {
	desired := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mcpToolName(agent, tool),
			Namespace: agent.Namespace,
			Labels:    mcpToolLabels(agent, tool),
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: mcpToolSelector(agent, tool),
			Ports: []corev1.ServicePort{{
				Name:       "http",
				Port:       mcpToolPort,
				TargetPort: intstr.FromInt(int(mcpToolPort)),
				Protocol:   corev1.ProtocolTCP,
			}},
		},
	}
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
