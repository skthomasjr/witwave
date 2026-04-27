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

	witwavev1alpha1 "github.com/witwave-ai/witwave-operator/api/v1alpha1"
)

// TestBuildDeploymentBackendThreeProbeModel covers #1719: operator-rendered
// backend containers must follow the three-probe split documented in
// AGENTS.md and present in the chart.
//
//   - startupProbe → /health/start
//   - livenessProbe → /health
//   - readinessProbe → /health/ready
//
// The echo backend is the deliberate exception: it only exposes /health (per
// backends/echo/README.md intentional-non-scope), so it retains /health for
// liveness AND readiness, and gets no startupProbe.
func TestBuildDeploymentBackendThreeProbeModel(t *testing.T) {
	agent := &witwavev1alpha1.WitwaveAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "iris", Namespace: "default"},
		Spec: witwavev1alpha1.WitwaveAgentSpec{
			Image: witwavev1alpha1.ImageSpec{
				Repository: "ghcr.io/witwave-ai/images/harness",
				Tag:        "test",
			},
			Backends: []witwavev1alpha1.BackendSpec{
				{
					Name: "claude",
					Image: witwavev1alpha1.ImageSpec{
						Repository: "ghcr.io/witwave-ai/images/claude",
						Tag:        "test",
					},
				},
				{
					Name: "codex",
					Image: witwavev1alpha1.ImageSpec{
						Repository: "ghcr.io/witwave-ai/images/codex",
						Tag:        "test",
					},
				},
				{
					Name: "gemini",
					Image: witwavev1alpha1.ImageSpec{
						Repository: "ghcr.io/witwave-ai/images/gemini",
						Tag:        "test",
					},
				},
				{
					Name: "echo",
					Image: witwavev1alpha1.ImageSpec{
						Repository: "ghcr.io/witwave-ai/images/echo",
						Tag:        "test",
					},
				},
			},
		},
	}

	dep := buildDeployment(agent, "test", nil)
	if dep == nil {
		t.Fatalf("buildDeployment returned nil")
	}

	containers := dep.Spec.Template.Spec.Containers
	byName := map[string]int{}
	for i, c := range containers {
		byName[c.Name] = i
	}

	for _, name := range []string{"claude", "codex", "gemini"} {
		idx, ok := byName[name]
		if !ok {
			t.Fatalf("%s container missing from deployment", name)
		}
		c := containers[idx]
		if c.StartupProbe == nil || c.StartupProbe.HTTPGet == nil {
			t.Errorf("%s: expected startupProbe httpGet, got nil", name)
		} else if c.StartupProbe.HTTPGet.Path != "/health/start" {
			t.Errorf("%s: startupProbe path = %q, want /health/start", name, c.StartupProbe.HTTPGet.Path)
		}
		if c.LivenessProbe == nil || c.LivenessProbe.HTTPGet == nil {
			t.Errorf("%s: expected livenessProbe httpGet, got nil", name)
		} else if c.LivenessProbe.HTTPGet.Path != "/health" {
			t.Errorf("%s: livenessProbe path = %q, want /health", name, c.LivenessProbe.HTTPGet.Path)
		}
		if c.ReadinessProbe == nil || c.ReadinessProbe.HTTPGet == nil {
			t.Errorf("%s: expected readinessProbe httpGet, got nil", name)
		} else if c.ReadinessProbe.HTTPGet.Path != "/health/ready" {
			t.Errorf("%s: readinessProbe path = %q, want /health/ready", name, c.ReadinessProbe.HTTPGet.Path)
		}
	}

	// Echo: skip startupProbe, both other probes target /health.
	idx, ok := byName["echo"]
	if !ok {
		t.Fatalf("echo container missing from deployment")
	}
	c := containers[idx]
	if c.StartupProbe != nil {
		t.Errorf("echo: expected no startupProbe, got %+v", c.StartupProbe.HTTPGet)
	}
	if c.LivenessProbe == nil || c.LivenessProbe.HTTPGet == nil || c.LivenessProbe.HTTPGet.Path != "/health" {
		t.Errorf("echo: livenessProbe = %+v, want path /health", c.LivenessProbe)
	}
	if c.ReadinessProbe == nil || c.ReadinessProbe.HTTPGet == nil || c.ReadinessProbe.HTTPGet.Path != "/health" {
		t.Errorf("echo: readinessProbe = %+v, want path /health", c.ReadinessProbe)
	}
}
