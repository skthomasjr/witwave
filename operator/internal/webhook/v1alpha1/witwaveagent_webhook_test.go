/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
...
*/

package v1alpha1

import (
	"context"
	"strings"
	"testing"

	witwavev1alpha1 "github.com/witwave-ai/witwave-operator/api/v1alpha1"
)

func TestDefaulterPopulatesPortWhenZero(t *testing.T) {
	d := &WitwaveAgentCustomDefaulter{}
	agent := &witwavev1alpha1.WitwaveAgent{}
	if err := d.Default(context.Background(), agent); err != nil {
		t.Fatalf("Default returned err: %v", err)
	}
	if agent.Spec.Port != 8000 {
		t.Fatalf("expected default port 8000, got %d", agent.Spec.Port)
	}
}

func TestDefaulterPreservesExplicitPort(t *testing.T) {
	d := &WitwaveAgentCustomDefaulter{}
	agent := &witwavev1alpha1.WitwaveAgent{Spec: witwavev1alpha1.WitwaveAgentSpec{Port: 9000}}
	if err := d.Default(context.Background(), agent); err != nil {
		t.Fatalf("Default returned err: %v", err)
	}
	if agent.Spec.Port != 9000 {
		t.Fatalf("expected preserved port 9000, got %d", agent.Spec.Port)
	}
}

func TestValidatorRejectsDuplicateBackendNames(t *testing.T) {
	v := &WitwaveAgentCustomValidator{}
	agent := &witwavev1alpha1.WitwaveAgent{
		Spec: witwavev1alpha1.WitwaveAgentSpec{
			Backends: []witwavev1alpha1.BackendSpec{
				{Name: "claude"},
				{Name: "codex"},
				{Name: "claude"}, // duplicate
			},
		},
	}
	_, err := v.ValidateCreate(context.Background(), agent)
	if err == nil {
		t.Fatal("expected error for duplicate backend name, got nil")
	}
	if !strings.Contains(err.Error(), "duplicates") {
		t.Fatalf("expected error to mention duplicates, got: %v", err)
	}
}

func TestValidatorAllowsUniqueBackendNames(t *testing.T) {
	v := &WitwaveAgentCustomValidator{}
	agent := &witwavev1alpha1.WitwaveAgent{
		Spec: witwavev1alpha1.WitwaveAgentSpec{
			Backends: []witwavev1alpha1.BackendSpec{
				{Name: "claude"},
				{Name: "codex"},
				{Name: "gemini"},
			},
		},
	}
	if _, err := v.ValidateCreate(context.Background(), agent); err != nil {
		t.Fatalf("ValidateCreate returned err: %v", err)
	}
	if _, err := v.ValidateUpdate(context.Background(), nil, agent); err != nil {
		t.Fatalf("ValidateUpdate returned err: %v", err)
	}
}

// TestValidatorWarnsOnInlineCredentialsRBAC asserts the webhook returns
// (but does not error on) an admission warning when a backend or gitSync
// opts in to inline credentials via AcknowledgeInsecureInline=true. The
// warning tells the operator to verify the chart was installed with
// rbac.secretsWrite=true, since the webhook can't introspect its own RBAC.
// See #1623, #1613.
func TestValidatorWarnsOnInlineCredentialsRBAC(t *testing.T) {
	v := &WitwaveAgentCustomValidator{}
	agent := &witwavev1alpha1.WitwaveAgent{
		Spec: witwavev1alpha1.WitwaveAgentSpec{
			Backends: []witwavev1alpha1.BackendSpec{
				{
					Name: "claude",
					Credentials: &witwavev1alpha1.BackendCredentialsSpec{
						Secrets:                   map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "sk-x"},
						AcknowledgeInsecureInline: true,
					},
				},
			},
			GitSyncs: []witwavev1alpha1.GitSyncSpec{
				{
					Name: "config",
					Repo: "https://example.com/x.git",
					Credentials: &witwavev1alpha1.GitSyncCredentialsSpec{
						Username:                  "alice",
						Token:                     "ghp_xxx",
						AcknowledgeInsecureInline: true,
					},
				},
			},
		},
	}
	warnings, err := v.ValidateCreate(context.Background(), agent)
	if err != nil {
		t.Fatalf("ValidateCreate returned err: %v", err)
	}
	if len(warnings) != 2 {
		t.Fatalf("expected 2 warnings (one per inline-creds entry), got %d: %v", len(warnings), warnings)
	}
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "rbac.secretsWrite=true") {
		t.Fatalf("expected warning to mention rbac.secretsWrite=true, got: %v", warnings)
	}
	if !strings.Contains(joined, "spec.backends[0].credentials") {
		t.Fatalf("expected warning to point at spec.backends[0].credentials, got: %v", warnings)
	}
	if !strings.Contains(joined, "spec.gitSyncs[0].credentials") {
		t.Fatalf("expected warning to point at spec.gitSyncs[0].credentials, got: %v", warnings)
	}

	// ValidateUpdate path emits the same warnings.
	updateWarnings, err := v.ValidateUpdate(context.Background(), nil, agent)
	if err != nil {
		t.Fatalf("ValidateUpdate returned err: %v", err)
	}
	if len(updateWarnings) != 2 {
		t.Fatalf("ValidateUpdate: expected 2 warnings, got %d: %v", len(updateWarnings), updateWarnings)
	}
}

// TestValidatorNoWarningWhenExistingSecret asserts that credentials
// blocks using ExistingSecret (the production path) emit no warnings —
// the operator only needs Secret read verbs in that case, which is the
// default RBAC posture.
func TestValidatorNoWarningWhenExistingSecret(t *testing.T) {
	v := &WitwaveAgentCustomValidator{}
	agent := &witwavev1alpha1.WitwaveAgent{
		Spec: witwavev1alpha1.WitwaveAgentSpec{
			Backends: []witwavev1alpha1.BackendSpec{
				{
					Name: "claude",
					Credentials: &witwavev1alpha1.BackendCredentialsSpec{
						ExistingSecret: "claude-creds",
					},
				},
			},
		},
	}
	warnings, err := v.ValidateCreate(context.Background(), agent)
	if err != nil {
		t.Fatalf("ValidateCreate returned err: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for existingSecret path, got %d: %v", len(warnings), warnings)
	}
}
