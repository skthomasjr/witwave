/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	witwavev1alpha1 "github.com/witwave-ai/witwave-operator/api/v1alpha1"
)

func TestMCPToolName(t *testing.T) {
	agent := &witwavev1alpha1.WitwaveAgent{ObjectMeta: metav1.ObjectMeta{Name: "iris"}}
	if got, want := mcpToolName(agent, "kubernetes"), "iris-mcp-kubernetes"; got != want {
		t.Fatalf("mcpToolName: got %q want %q", got, want)
	}
	if got, want := mcpToolName(agent, "helm"), "iris-mcp-helm"; got != want {
		t.Fatalf("mcpToolName: got %q want %q", got, want)
	}
}

func TestResolveMCPToolImage_DefaultsWhenSpecNil(t *testing.T) {
	img := resolveMCPToolImage("kubernetes", nil, "v1.2.3")
	if img.Repository != "ghcr.io/witwave-ai/images/mcp-kubernetes" {
		t.Fatalf("expected default repository, got %q", img.Repository)
	}
	if img.Tag != "" {
		t.Fatalf("expected empty tag (resolved later by imageRef), got %q", img.Tag)
	}
}

func TestResolveMCPToolImage_FillsMissingRepository(t *testing.T) {
	spec := &witwavev1alpha1.MCPToolSpec{
		Image: &witwavev1alpha1.ImageSpec{Tag: "pinned"},
	}
	img := resolveMCPToolImage("helm", spec, "v1.2.3")
	if img.Repository != "ghcr.io/witwave-ai/images/mcp-helm" {
		t.Fatalf("expected default repository, got %q", img.Repository)
	}
	if img.Tag != "pinned" {
		t.Fatalf("expected caller-provided tag preserved, got %q", img.Tag)
	}
}

func TestResolveMCPToolImage_PreservesExplicitSpec(t *testing.T) {
	spec := &witwavev1alpha1.MCPToolSpec{
		Image: &witwavev1alpha1.ImageSpec{
			Repository: "registry.example.com/mirror/mcp-kubernetes",
			Tag:        "custom",
		},
	}
	img := resolveMCPToolImage("kubernetes", spec, "v1.2.3")
	if img.Repository != "registry.example.com/mirror/mcp-kubernetes" {
		t.Fatalf("repository should pass through unchanged, got %q", img.Repository)
	}
	if img.Tag != "custom" {
		t.Fatalf("tag should pass through unchanged, got %q", img.Tag)
	}
}

func TestMCPToolLabelsIncludeComponent(t *testing.T) {
	agent := &witwavev1alpha1.WitwaveAgent{ObjectMeta: metav1.ObjectMeta{Name: "iris"}}
	labels := mcpToolLabels(agent, "kubernetes")
	if labels[labelComponent] != "mcp-kubernetes" {
		t.Fatalf("labelComponent: got %q want mcp-kubernetes", labels[labelComponent])
	}
	sel := mcpToolSelector(agent, "kubernetes")
	if sel[labelName] != "iris-mcp-kubernetes" {
		t.Fatalf("selector name mismatch: got %q", sel[labelName])
	}
	// Selector intentionally omits managed-by / part-of for forward compat.
	if _, ok := sel[labelManagedBy]; ok {
		t.Fatalf("selector should not include managed-by label")
	}
}

// TestBuildMCPToolServiceIncludesMetricsPort covers #1722: operator-rendered
// MCP-tool Services must expose both http (8000) and metrics (9000) ports
// so a ServiceMonitor can scrape via Service endpoints — chart parity with
// charts/witwave/templates/mcp-tools.yaml.
func TestBuildMCPToolServiceIncludesMetricsPort(t *testing.T) {
	agent := &witwavev1alpha1.WitwaveAgent{ObjectMeta: metav1.ObjectMeta{Name: "iris", Namespace: "default"}}
	svc := buildMCPToolService(agent, "kubernetes")
	if svc == nil {
		t.Fatalf("buildMCPToolService returned nil")
	}
	byName := map[string]bool{}
	for _, p := range svc.Spec.Ports {
		byName[p.Name] = true
	}
	if !byName["http"] {
		t.Errorf("expected http ServicePort, got ports = %+v", svc.Spec.Ports)
	}
	if !byName["metrics"] {
		t.Errorf("expected metrics ServicePort (#1722 chart parity), got ports = %+v", svc.Spec.Ports)
	}
	for _, p := range svc.Spec.Ports {
		if p.Name == "metrics" {
			if p.Port != 9000 {
				t.Errorf("metrics ServicePort port = %d, want 9000", p.Port)
			}
			if p.TargetPort.StrVal != "metrics" {
				t.Errorf("metrics targetPort = %v, want named target 'metrics'", p.TargetPort)
			}
		}
	}
}

// #1737: hardened pod + container securityContext defaults must be
// stamped when the spec leaves them unset, mirroring the chart's
// mcp-tools.yaml posture so PSS-restricted admission accepts the pod.
func TestMCPToolPodSecurityContextDefaults(t *testing.T) {
	got := mcpToolPodSecurityContext(nil)
	if got == nil || got.RunAsNonRoot == nil || !*got.RunAsNonRoot {
		t.Fatalf("default pod securityContext must set RunAsNonRoot=true; got %+v", got)
	}
	if got.SeccompProfile == nil || got.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Fatalf("default pod securityContext must set SeccompProfile=RuntimeDefault; got %+v", got.SeccompProfile)
	}
	if got.RunAsUser == nil || *got.RunAsUser != 1000 {
		t.Fatalf("default pod securityContext must set RunAsUser=1000; got %v", got.RunAsUser)
	}
}

func TestMCPToolPodSecurityContextOverride(t *testing.T) {
	custom := &corev1.PodSecurityContext{RunAsUser: int64Ptr(2000)}
	spec := &witwavev1alpha1.MCPToolSpec{PodSecurityContext: custom}
	got := mcpToolPodSecurityContext(spec)
	if got != custom {
		t.Fatalf("user-supplied pod securityContext must pass through verbatim; got %+v", got)
	}
}

func TestMCPToolContainerSecurityContextDefaults(t *testing.T) {
	got := mcpToolContainerSecurityContext(nil)
	if got == nil {
		t.Fatal("default container securityContext must not be nil")
	}
	if got.AllowPrivilegeEscalation == nil || *got.AllowPrivilegeEscalation {
		t.Fatalf("default container securityContext must set AllowPrivilegeEscalation=false; got %+v", got.AllowPrivilegeEscalation)
	}
	if got.ReadOnlyRootFilesystem == nil || !*got.ReadOnlyRootFilesystem {
		t.Fatalf("default container securityContext must set ReadOnlyRootFilesystem=true; got %+v", got.ReadOnlyRootFilesystem)
	}
	if got.RunAsNonRoot == nil || !*got.RunAsNonRoot {
		t.Fatalf("default container securityContext must set RunAsNonRoot=true; got %+v", got.RunAsNonRoot)
	}
	if got.Capabilities == nil || len(got.Capabilities.Drop) == 0 || got.Capabilities.Drop[0] != "ALL" {
		t.Fatalf("default container securityContext must Drop=[ALL]; got %+v", got.Capabilities)
	}
	if got.SeccompProfile == nil || got.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Fatalf("default container securityContext must set SeccompProfile=RuntimeDefault; got %+v", got.SeccompProfile)
	}
}

func TestMCPToolContainerSecurityContextOverride(t *testing.T) {
	custom := &corev1.SecurityContext{AllowPrivilegeEscalation: boolPtr(true)}
	spec := &witwavev1alpha1.MCPToolSpec{SecurityContext: custom}
	if mcpToolContainerSecurityContext(spec) != custom {
		t.Fatal("user-supplied container securityContext must pass through verbatim")
	}
}

// #1737: AutomountServiceAccountToken is three-state. Nil means "default
// to true" (chart parity); explicit false suppresses the in-pod token.
func TestMCPToolAutomountSAToken(t *testing.T) {
	if got := mcpToolAutomountSAToken(nil); got == nil || !*got {
		t.Fatalf("nil spec → default true; got %+v", got)
	}
	if got := mcpToolAutomountSAToken(&witwavev1alpha1.MCPToolSpec{}); got == nil || !*got {
		t.Fatalf("empty spec → default true; got %+v", got)
	}
	f := false
	spec := &witwavev1alpha1.MCPToolSpec{AutomountServiceAccountToken: &f}
	if got := mcpToolAutomountSAToken(spec); got == nil || *got {
		t.Fatalf("explicit false must pass through verbatim; got %+v", got)
	}
}

// #1737: emptyDir carve-outs for /tmp + /home/agent/.cache so
// readOnlyRootFilesystem=true coexists with helm-CLI cache + Python
// tempfile writes.
func TestMCPToolVolumeMountsAndVolumes(t *testing.T) {
	mounts := mcpToolVolumeMounts()
	if len(mounts) != 2 {
		t.Fatalf("expected 2 volume mounts (/tmp + ~/.cache), got %d", len(mounts))
	}
	mountPaths := map[string]bool{}
	for _, m := range mounts {
		mountPaths[m.MountPath] = true
	}
	if !mountPaths["/tmp"] || !mountPaths["/home/agent/.cache"] {
		t.Fatalf("expected /tmp + /home/agent/.cache mounts; got %+v", mounts)
	}

	vols := mcpToolVolumes()
	if len(vols) != 2 {
		t.Fatalf("expected 2 emptyDir volumes paired with mounts, got %d", len(vols))
	}
	for _, v := range vols {
		if v.EmptyDir == nil {
			t.Fatalf("volume %q must be EmptyDir-backed; got %+v", v.Name, v.VolumeSource)
		}
	}
}
