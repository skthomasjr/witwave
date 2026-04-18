/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package controller

import (
	"strings"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	nyxv1alpha1 "github.com/nyx-ai/nyx-operator/api/v1alpha1"
)

// Unit tests for the NyxPromptReconciler's render path (#834). Envtest
// coverage for the full Reconcile loop is tracked separately — these
// tests exercise the **pure** rendering / naming helpers so regressions
// in ConfigMap bytes (which the kubelet compares for mount churn) or
// path layout (which the harness watches for) surface without spinning
// a full apiserver.

func TestNyxPromptConfigMapName(t *testing.T) {
	got := nyxPromptConfigMapName("daily-summary", "iris")
	if got != "nyxprompt-daily-summary-iris" {
		t.Fatalf("unexpected cm name: %q", got)
	}
}

func TestNyxPromptFilenameHeartbeatIsSpecialCased(t *testing.T) {
	p := &nyxv1alpha1.NyxPrompt{
		ObjectMeta: metav1.ObjectMeta{Name: "beat"},
		Spec: nyxv1alpha1.NyxPromptSpec{
			Kind: nyxv1alpha1.NyxPromptKindHeartbeat,
			Body: "body",
		},
	}
	ref := nyxv1alpha1.NyxPromptAgentRef{Name: "iris"}
	got := nyxPromptFilename(p, ref)
	if got != "HEARTBEAT.md" {
		t.Fatalf("heartbeat must mint to HEARTBEAT.md exactly — the harness watches that literal filename; got %q", got)
	}
}

func TestNyxPromptFilenameDefaultAndSuffix(t *testing.T) {
	p := &nyxv1alpha1.NyxPrompt{
		ObjectMeta: metav1.ObjectMeta{Name: "daily"},
		Spec:       nyxv1alpha1.NyxPromptSpec{Kind: nyxv1alpha1.NyxPromptKindJob, Body: "x"},
	}
	if got := nyxPromptFilename(p, nyxv1alpha1.NyxPromptAgentRef{Name: "iris"}); got != "nyxprompt-daily.md" {
		t.Fatalf("default filename: got %q", got)
	}
	if got := nyxPromptFilename(p, nyxv1alpha1.NyxPromptAgentRef{Name: "iris", FilenameSuffix: "morning"}); got != "nyxprompt-daily-morning.md" {
		t.Fatalf("suffix filename: got %q", got)
	}
}

func TestNyxPromptMountDirPerKind(t *testing.T) {
	cases := map[nyxv1alpha1.NyxPromptKind]string{
		nyxv1alpha1.NyxPromptKindJob:          "/home/agent/.nyx/jobs",
		nyxv1alpha1.NyxPromptKindTask:         "/home/agent/.nyx/tasks",
		nyxv1alpha1.NyxPromptKindTrigger:      "/home/agent/.nyx/triggers",
		nyxv1alpha1.NyxPromptKindContinuation: "/home/agent/.nyx/continuations",
		nyxv1alpha1.NyxPromptKindWebhook:      "/home/agent/.nyx/webhooks",
		nyxv1alpha1.NyxPromptKindHeartbeat:    "/home/agent/.nyx",
	}
	for k, want := range cases {
		got := nyxPromptMountDir(k)
		if got != want {
			t.Errorf("kind=%q: got %q, want %q", k, got, want)
		}
	}
	// Unknown kind must return empty (so callers can fail loudly rather
	// than mount at /).
	if got := nyxPromptMountDir(nyxv1alpha1.NyxPromptKind("bogus")); got != "" {
		t.Errorf("unknown kind must yield empty mountDir, got %q", got)
	}
}

func TestRenderNyxPromptBody_NoFrontmatter(t *testing.T) {
	p := &nyxv1alpha1.NyxPrompt{
		Spec: nyxv1alpha1.NyxPromptSpec{
			Kind: nyxv1alpha1.NyxPromptKindJob,
			Body: "do the thing",
		},
	}
	body, err := renderNyxPromptBody(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(body, "---") {
		t.Errorf("no frontmatter was supplied but body contains '---' fences: %q", body)
	}
	if !strings.HasSuffix(body, "\n") {
		t.Errorf("body must end in a newline so line-oriented tools don't drop the last line")
	}
}

func TestRenderNyxPromptBody_FrontmatterIsSorted(t *testing.T) {
	p := &nyxv1alpha1.NyxPrompt{
		Spec: nyxv1alpha1.NyxPromptSpec{
			Kind: nyxv1alpha1.NyxPromptKindJob,
			Body: "do the thing",
			Frontmatter: &apiextensionsv1.JSON{
				Raw: []byte(`{"zeta":1,"alpha":"one","middle":2}`),
			},
		},
	}
	body, err := renderNyxPromptBody(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Frontmatter must sit between --- fences.
	if !strings.HasPrefix(body, "---\n") {
		t.Fatalf("frontmatter branch must open with '---' fence; got %q", body[:8])
	}
	// Keys must appear in sorted order so the rendered CM is
	// byte-stable across reconciles — kubelet compares CM bytes, and
	// unstable ordering would churn the mounted volume on every
	// reconcile.
	alpha := strings.Index(body, "alpha:")
	mid := strings.Index(body, "middle:")
	zeta := strings.Index(body, "zeta:")
	if !(alpha < mid && mid < zeta) {
		t.Fatalf("frontmatter keys must be in sorted order for byte-stability; got %q", body)
	}
}

func TestRenderNyxPromptBody_InvalidFrontmatterRaisesError(t *testing.T) {
	p := &nyxv1alpha1.NyxPrompt{
		Spec: nyxv1alpha1.NyxPromptSpec{
			Kind:        nyxv1alpha1.NyxPromptKindJob,
			Body:        "x",
			Frontmatter: &apiextensionsv1.JSON{Raw: []byte(`[1,2,3]`)},
		},
	}
	if _, err := renderNyxPromptBody(p); err == nil {
		t.Fatal("array frontmatter must be rejected — the renderer only supports maps")
	}
}

func TestBuildNyxPromptConfigMap_LabelsAndOwnershipMetadata(t *testing.T) {
	p := &nyxv1alpha1.NyxPrompt{
		ObjectMeta: metav1.ObjectMeta{Name: "daily", Namespace: "nyx"},
		Spec: nyxv1alpha1.NyxPromptSpec{
			Kind: nyxv1alpha1.NyxPromptKindJob,
			Body: "body",
		},
	}
	ref := nyxv1alpha1.NyxPromptAgentRef{Name: "iris"}
	cm, err := buildNyxPromptConfigMap(p, ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cm.Name != "nyxprompt-daily-iris" {
		t.Errorf("unexpected cm name %q", cm.Name)
	}
	if cm.Namespace != "nyx" {
		t.Errorf("cm namespace should match prompt namespace; got %q", cm.Namespace)
	}
	// Data key is the filename — kubelet subPath mount lookups use
	// this exact key.
	if _, ok := cm.Data["nyxprompt-daily.md"]; !ok {
		t.Errorf("expected 'nyxprompt-daily.md' data key; got keys %v", keys(cm.Data))
	}
	// Labels must at least identify the owner prompt + target agent +
	// kind so operators can filter via kubectl get cm -l.
	for _, want := range []string{
		labelNyxPromptName, labelNyxPromptTargetAgent, labelNyxPromptKind,
	} {
		if _, ok := cm.Labels[want]; !ok {
			t.Errorf("missing expected label %q; got %v", want, cm.Labels)
		}
	}
	if cm.Labels[labelNyxPromptTargetAgent] != "iris" {
		t.Errorf("target-agent label should be 'iris'; got %q", cm.Labels[labelNyxPromptTargetAgent])
	}
}

func TestBuildNyxPromptConfigMap_HeartbeatBodyEndsUpInHEARTBEATdotmd(t *testing.T) {
	p := &nyxv1alpha1.NyxPrompt{
		ObjectMeta: metav1.ObjectMeta{Name: "beat", Namespace: "nyx"},
		Spec: nyxv1alpha1.NyxPromptSpec{
			Kind: nyxv1alpha1.NyxPromptKindHeartbeat,
			Body: "# beat\ndo a thing",
		},
	}
	cm, err := buildNyxPromptConfigMap(p, nyxv1alpha1.NyxPromptAgentRef{Name: "iris"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := cm.Data["HEARTBEAT.md"]; !ok {
		t.Fatalf("heartbeat kind must land under HEARTBEAT.md key; got %v", keys(cm.Data))
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
