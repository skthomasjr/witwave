/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Unit tests for the credential resolver decision matrix (#1705). The
// resolvers are the single nucleus through which every backend's auth
// envFrom rendering flows; integration tests via reconcile loop don't
// isolate the mode-selection logic from the surrounding apply path.
// Direct table-driven tests catch precedence regressions
// (ExistingSecret over inline, falsy fields → empty resolver) without
// needing a fake apiserver.
package controller

import (
	"testing"

	witwavev1alpha1 "github.com/witwave-ai/witwave-operator/api/v1alpha1"
)

// ----- name builders ----------------------------------------------

func TestGitsyncCredentialsSecretNameFormat(t *testing.T) {
	got := gitsyncCredentialsSecretName("iris", "config-repo")
	want := "iris-config-repo-gitsync-credentials"
	if got != want {
		t.Errorf("gitsyncCredentialsSecretName: want %q, got %q", want, got)
	}
}

func TestBackendCredentialsSecretNameFormat(t *testing.T) {
	got := backendCredentialsSecretName("iris", "claude")
	want := "iris-claude-backend-credentials"
	if got != want {
		t.Errorf("backendCredentialsSecretName: want %q, got %q", want, got)
	}
}

// ----- resolveGitSyncCredentials decision matrix ------------------

func TestResolveGitSyncCredentialsNilReturnsEmpty(t *testing.T) {
	gs := witwavev1alpha1.GitSyncSpec{Name: "config", Credentials: nil}
	r := resolveGitSyncCredentials("iris", gs)
	if r.SecretName != "" || r.Managed {
		t.Errorf("nil credentials must produce empty resolver; got %+v", r)
	}
}

func TestResolveGitSyncCredentialsEmptyCredsReturnsEmpty(t *testing.T) {
	gs := witwavev1alpha1.GitSyncSpec{
		Name:        "config",
		Credentials: &witwavev1alpha1.GitSyncCredentialsSpec{},
	}
	r := resolveGitSyncCredentials("iris", gs)
	if r.SecretName != "" || r.Managed {
		t.Errorf("empty credentials must produce empty resolver; got %+v", r)
	}
}

func TestResolveGitSyncCredentialsExistingSecret(t *testing.T) {
	gs := witwavev1alpha1.GitSyncSpec{
		Name: "config",
		Credentials: &witwavev1alpha1.GitSyncCredentialsSpec{
			ExistingSecret: "my-pat-secret",
		},
	}
	r := resolveGitSyncCredentials("iris", gs)
	if r.SecretName != "my-pat-secret" {
		t.Errorf("ExistingSecret pass-through: want my-pat-secret, got %q", r.SecretName)
	}
	if r.Managed {
		t.Errorf("ExistingSecret must NOT set Managed=true (operator does not own it)")
	}
}

func TestResolveGitSyncCredentialsInlineUsernameOnly(t *testing.T) {
	gs := witwavev1alpha1.GitSyncSpec{
		Name: "config",
		Credentials: &witwavev1alpha1.GitSyncCredentialsSpec{
			Username: "alice",
		},
	}
	r := resolveGitSyncCredentials("iris", gs)
	if r.SecretName != "iris-config-gitsync-credentials" {
		t.Errorf("inline Username: want generated secret name, got %q", r.SecretName)
	}
	if !r.Managed {
		t.Errorf("inline credentials must set Managed=true")
	}
}

func TestResolveGitSyncCredentialsInlineTokenOnly(t *testing.T) {
	gs := witwavev1alpha1.GitSyncSpec{
		Name: "config",
		Credentials: &witwavev1alpha1.GitSyncCredentialsSpec{
			Token: "ghp_xxx",
		},
	}
	r := resolveGitSyncCredentials("iris", gs)
	if r.SecretName != "iris-config-gitsync-credentials" || !r.Managed {
		t.Errorf("inline Token: want generated managed name; got %+v", r)
	}
}

func TestResolveGitSyncCredentialsExistingSecretWinsOverInline(t *testing.T) {
	gs := witwavev1alpha1.GitSyncSpec{
		Name: "config",
		Credentials: &witwavev1alpha1.GitSyncCredentialsSpec{
			ExistingSecret: "preferred",
			Username:       "alice",
			Token:          "ghp_xxx",
		},
	}
	r := resolveGitSyncCredentials("iris", gs)
	if r.SecretName != "preferred" {
		t.Errorf("ExistingSecret must win when both modes set; got %q", r.SecretName)
	}
	if r.Managed {
		t.Errorf("ExistingSecret precedence must keep Managed=false")
	}
}

// ----- resolveBackendCredentials decision matrix ------------------

func TestResolveBackendCredentialsNilReturnsEmpty(t *testing.T) {
	b := witwavev1alpha1.BackendSpec{Name: "claude", Credentials: nil}
	r := resolveBackendCredentials("iris", b)
	if r.SecretName != "" || r.Managed {
		t.Errorf("nil credentials must produce empty resolver; got %+v", r)
	}
}

func TestResolveBackendCredentialsEmptyCredsReturnsEmpty(t *testing.T) {
	b := witwavev1alpha1.BackendSpec{
		Name:        "claude",
		Credentials: &witwavev1alpha1.BackendCredentialsSpec{},
	}
	r := resolveBackendCredentials("iris", b)
	if r.SecretName != "" || r.Managed {
		t.Errorf("empty credentials must produce empty resolver; got %+v", r)
	}
}

func TestResolveBackendCredentialsExistingSecret(t *testing.T) {
	b := witwavev1alpha1.BackendSpec{
		Name: "claude",
		Credentials: &witwavev1alpha1.BackendCredentialsSpec{
			ExistingSecret: "claude-prod-secret",
		},
	}
	r := resolveBackendCredentials("iris", b)
	if r.SecretName != "claude-prod-secret" || r.Managed {
		t.Errorf("ExistingSecret: want claude-prod-secret + Managed=false; got %+v", r)
	}
}

func TestResolveBackendCredentialsInlineSecrets(t *testing.T) {
	b := witwavev1alpha1.BackendSpec{
		Name: "claude",
		Credentials: &witwavev1alpha1.BackendCredentialsSpec{
			Secrets: map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "sk-test"},
		},
	}
	r := resolveBackendCredentials("iris", b)
	if r.SecretName != "iris-claude-backend-credentials" || !r.Managed {
		t.Errorf("inline Secrets: want generated managed name; got %+v", r)
	}
}

func TestResolveBackendCredentialsExistingSecretWinsOverInline(t *testing.T) {
	b := witwavev1alpha1.BackendSpec{
		Name: "claude",
		Credentials: &witwavev1alpha1.BackendCredentialsSpec{
			ExistingSecret: "preferred-secret",
			Secrets:        map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "sk-test"},
		},
	}
	r := resolveBackendCredentials("iris", b)
	if r.SecretName != "preferred-secret" {
		t.Errorf("ExistingSecret precedence: want preferred-secret, got %q", r.SecretName)
	}
	if r.Managed {
		t.Errorf("ExistingSecret precedence keeps Managed=false")
	}
}

func TestResolveBackendCredentialsEmptySecretsMapReturnsEmpty(t *testing.T) {
	// `Secrets: map[string]string{}` (empty map) should NOT trigger
	// managed mode — same as nil/missing. Inline mode requires at
	// least one entry.
	b := witwavev1alpha1.BackendSpec{
		Name: "claude",
		Credentials: &witwavev1alpha1.BackendCredentialsSpec{
			Secrets: map[string]string{},
		},
	}
	r := resolveBackendCredentials("iris", b)
	if r.SecretName != "" || r.Managed {
		t.Errorf("empty Secrets map must produce empty resolver; got %+v", r)
	}
}

// ----- envFromSource builders -------------------------------------

func TestGitsyncCredentialsEnvFromSourceEmptyReturnsNil(t *testing.T) {
	got := gitsyncCredentialsEnvFromSource(gitsyncCredentialsResolved{})
	if got != nil {
		t.Errorf("empty resolver must produce nil EnvFromSource; got %+v", got)
	}
}

func TestGitsyncCredentialsEnvFromSourcePopulatesSecretRef(t *testing.T) {
	got := gitsyncCredentialsEnvFromSource(gitsyncCredentialsResolved{SecretName: "abc"})
	if got == nil || got.SecretRef == nil {
		t.Fatalf("populated resolver must produce SecretRef-bearing EnvFromSource; got %+v", got)
	}
	if got.SecretRef.Name != "abc" {
		t.Errorf("SecretRef.Name: want abc, got %q", got.SecretRef.Name)
	}
}

func TestBackendCredentialsEnvFromSourceEmptyReturnsNil(t *testing.T) {
	got := backendCredentialsEnvFromSource(backendCredentialsResolved{})
	if got != nil {
		t.Errorf("empty resolver must produce nil EnvFromSource; got %+v", got)
	}
}

func TestBackendCredentialsEnvFromSourcePopulatesSecretRef(t *testing.T) {
	got := backendCredentialsEnvFromSource(backendCredentialsResolved{SecretName: "xyz"})
	if got == nil || got.SecretRef == nil || got.SecretRef.Name != "xyz" {
		t.Errorf("populated backend resolver must populate SecretRef.Name; got %+v", got)
	}
}
