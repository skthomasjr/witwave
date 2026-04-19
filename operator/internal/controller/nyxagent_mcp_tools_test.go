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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	nyxv1alpha1 "github.com/nyx-ai/nyx-operator/api/v1alpha1"
)

func TestMCPToolName(t *testing.T) {
	agent := &nyxv1alpha1.NyxAgent{ObjectMeta: metav1.ObjectMeta{Name: "iris"}}
	if got, want := mcpToolName(agent, "kubernetes"), "iris-mcp-kubernetes"; got != want {
		t.Fatalf("mcpToolName: got %q want %q", got, want)
	}
	if got, want := mcpToolName(agent, "helm"), "iris-mcp-helm"; got != want {
		t.Fatalf("mcpToolName: got %q want %q", got, want)
	}
}

func TestResolveMCPToolImage_DefaultsWhenSpecNil(t *testing.T) {
	img := resolveMCPToolImage("kubernetes", nil, "v1.2.3")
	if img.Repository != "ghcr.io/skthomasjr/images/mcp-kubernetes" {
		t.Fatalf("expected default repository, got %q", img.Repository)
	}
	if img.Tag != "" {
		t.Fatalf("expected empty tag (resolved later by imageRef), got %q", img.Tag)
	}
}

func TestResolveMCPToolImage_FillsMissingRepository(t *testing.T) {
	spec := &nyxv1alpha1.MCPToolSpec{
		Image: &nyxv1alpha1.ImageSpec{Tag: "pinned"},
	}
	img := resolveMCPToolImage("helm", spec, "v1.2.3")
	if img.Repository != "ghcr.io/skthomasjr/images/mcp-helm" {
		t.Fatalf("expected default repository, got %q", img.Repository)
	}
	if img.Tag != "pinned" {
		t.Fatalf("expected caller-provided tag preserved, got %q", img.Tag)
	}
}

func TestResolveMCPToolImage_PreservesExplicitSpec(t *testing.T) {
	spec := &nyxv1alpha1.MCPToolSpec{
		Image: &nyxv1alpha1.ImageSpec{
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
	agent := &nyxv1alpha1.NyxAgent{ObjectMeta: metav1.ObjectMeta{Name: "iris"}}
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
