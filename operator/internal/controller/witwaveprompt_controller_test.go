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

	witwavev1alpha1 "github.com/witwave-ai/witwave-operator/api/v1alpha1"
)

// Unit tests for the WitwavePromptReconciler's render path (#834). Envtest
// coverage for the full Reconcile loop is tracked separately — these
// tests exercise the **pure** rendering / naming helpers so regressions
// in ConfigMap bytes (which the kubelet compares for mount churn) or
// path layout (which the harness watches for) surface without spinning
// a full apiserver.

func TestWitwavePromptConfigMapName(t *testing.T) {
	got := witwavePromptConfigMapName("daily-summary", "iris")
	if got != "witwaveprompt-daily-summary-iris" {
		t.Fatalf("unexpected cm name: %q", got)
	}
}

func TestWitwavePromptFilenameHeartbeatIsSpecialCased(t *testing.T) {
	p := &witwavev1alpha1.WitwavePrompt{
		ObjectMeta: metav1.ObjectMeta{Name: "beat"},
		Spec: witwavev1alpha1.WitwavePromptSpec{
			Kind: witwavev1alpha1.WitwavePromptKindHeartbeat,
			Body: "body",
		},
	}
	ref := witwavev1alpha1.WitwavePromptAgentRef{Name: "iris"}
	got := witwavePromptFilename(p, ref)
	if got != "HEARTBEAT.md" {
		t.Fatalf("heartbeat must mint to HEARTBEAT.md exactly — the harness watches that literal filename; got %q", got)
	}
}

func TestWitwavePromptFilenameDefaultAndSuffix(t *testing.T) {
	p := &witwavev1alpha1.WitwavePrompt{
		ObjectMeta: metav1.ObjectMeta{Name: "daily"},
		Spec:       witwavev1alpha1.WitwavePromptSpec{Kind: witwavev1alpha1.WitwavePromptKindJob, Body: "x"},
	}
	if got := witwavePromptFilename(p, witwavev1alpha1.WitwavePromptAgentRef{Name: "iris"}); got != "witwaveprompt-daily.md" {
		t.Fatalf("default filename: got %q", got)
	}
	if got := witwavePromptFilename(p, witwavev1alpha1.WitwavePromptAgentRef{Name: "iris", FilenameSuffix: "morning"}); got != "witwaveprompt-daily-morning.md" {
		t.Fatalf("suffix filename: got %q", got)
	}
}

func TestWitwavePromptMountDirPerKind(t *testing.T) {
	cases := map[witwavev1alpha1.WitwavePromptKind]string{
		witwavev1alpha1.WitwavePromptKindJob:          "/home/agent/.witwave/jobs",
		witwavev1alpha1.WitwavePromptKindTask:         "/home/agent/.witwave/tasks",
		witwavev1alpha1.WitwavePromptKindTrigger:      "/home/agent/.witwave/triggers",
		witwavev1alpha1.WitwavePromptKindContinuation: "/home/agent/.witwave/continuations",
		witwavev1alpha1.WitwavePromptKindWebhook:      "/home/agent/.witwave/webhooks",
		witwavev1alpha1.WitwavePromptKindHeartbeat:    "/home/agent/.witwave",
	}
	for k, want := range cases {
		got := witwavePromptMountDir(k)
		if got != want {
			t.Errorf("kind=%q: got %q, want %q", k, got, want)
		}
	}
	// Unknown kind must return empty (so callers can fail loudly rather
	// than mount at /).
	if got := witwavePromptMountDir(witwavev1alpha1.WitwavePromptKind("bogus")); got != "" {
		t.Errorf("unknown kind must yield empty mountDir, got %q", got)
	}
}

func TestRenderWitwavePromptBody_NoFrontmatter(t *testing.T) {
	p := &witwavev1alpha1.WitwavePrompt{
		Spec: witwavev1alpha1.WitwavePromptSpec{
			Kind: witwavev1alpha1.WitwavePromptKindJob,
			Body: "do the thing",
		},
	}
	body, err := renderWitwavePromptBody(p)
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

func TestRenderWitwavePromptBody_FrontmatterIsSorted(t *testing.T) {
	p := &witwavev1alpha1.WitwavePrompt{
		Spec: witwavev1alpha1.WitwavePromptSpec{
			Kind: witwavev1alpha1.WitwavePromptKindJob,
			Body: "do the thing",
			Frontmatter: &apiextensionsv1.JSON{
				Raw: []byte(`{"zeta":1,"alpha":"one","middle":2}`),
			},
		},
	}
	body, err := renderWitwavePromptBody(p)
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

func TestRenderWitwavePromptBody_InvalidFrontmatterRaisesError(t *testing.T) {
	p := &witwavev1alpha1.WitwavePrompt{
		Spec: witwavev1alpha1.WitwavePromptSpec{
			Kind:        witwavev1alpha1.WitwavePromptKindJob,
			Body:        "x",
			Frontmatter: &apiextensionsv1.JSON{Raw: []byte(`[1,2,3]`)},
		},
	}
	if _, err := renderWitwavePromptBody(p); err == nil {
		t.Fatal("array frontmatter must be rejected — the renderer only supports maps")
	}
}

func TestBuildWitwavePromptConfigMap_LabelsAndOwnershipMetadata(t *testing.T) {
	p := &witwavev1alpha1.WitwavePrompt{
		ObjectMeta: metav1.ObjectMeta{Name: "daily", Namespace: "witwave"},
		Spec: witwavev1alpha1.WitwavePromptSpec{
			Kind: witwavev1alpha1.WitwavePromptKindJob,
			Body: "body",
		},
	}
	ref := witwavev1alpha1.WitwavePromptAgentRef{Name: "iris"}
	cm, err := buildWitwavePromptConfigMap(p, ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cm.Name != "witwaveprompt-daily-iris" {
		t.Errorf("unexpected cm name %q", cm.Name)
	}
	if cm.Namespace != "witwave" {
		t.Errorf("cm namespace should match prompt namespace; got %q", cm.Namespace)
	}
	// Data key is the filename — kubelet subPath mount lookups use
	// this exact key.
	if _, ok := cm.Data["witwaveprompt-daily.md"]; !ok {
		t.Errorf("expected 'witwaveprompt-daily.md' data key; got keys %v", keys(cm.Data))
	}
	// Labels must at least identify the owner prompt + target agent +
	// kind so operators can filter via kubectl get cm -l.
	for _, want := range []string{
		labelWitwavePromptName, labelWitwavePromptTargetAgent, labelWitwavePromptKind,
	} {
		if _, ok := cm.Labels[want]; !ok {
			t.Errorf("missing expected label %q; got %v", want, cm.Labels)
		}
	}
	if cm.Labels[labelWitwavePromptTargetAgent] != "iris" {
		t.Errorf("target-agent label should be 'iris'; got %q", cm.Labels[labelWitwavePromptTargetAgent])
	}
}

func TestBuildWitwavePromptConfigMap_HeartbeatBodyEndsUpInHEARTBEATdotmd(t *testing.T) {
	p := &witwavev1alpha1.WitwavePrompt{
		ObjectMeta: metav1.ObjectMeta{Name: "beat", Namespace: "witwave"},
		Spec: witwavev1alpha1.WitwavePromptSpec{
			Kind: witwavev1alpha1.WitwavePromptKindHeartbeat,
			Body: "# beat\ndo a thing",
		},
	}
	cm, err := buildWitwavePromptConfigMap(p, witwavev1alpha1.WitwavePromptAgentRef{Name: "iris"})
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
