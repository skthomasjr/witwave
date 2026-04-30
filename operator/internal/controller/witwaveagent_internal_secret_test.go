/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Unit tests for the internal-secret reconciler. Covers the
// generate-once / preserve-on-reconcile contract, the heal-missing-keys
// behavior, and the envFrom helper composition.
package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	witwavev1alpha1 "github.com/witwave-ai/witwave-operator/api/v1alpha1"
)

func TestInternalSecretNameFormat(t *testing.T) {
	got := internalSecretName("iris")
	want := "iris-internal"
	if got != want {
		t.Errorf("internalSecretName: want %q, got %q", want, got)
	}
}

func TestGenerateInternalAuthTokenNonEmptyAndUnique(t *testing.T) {
	a, err := generateInternalAuthToken()
	if err != nil {
		t.Fatalf("generateInternalAuthToken: %v", err)
	}
	if a == "" {
		t.Fatal("generateInternalAuthToken returned empty string")
	}
	b, err := generateInternalAuthToken()
	if err != nil {
		t.Fatalf("generateInternalAuthToken (second call): %v", err)
	}
	if a == b {
		t.Errorf("two consecutive calls returned identical tokens — entropy regression?")
	}
	if len(a) < 30 {
		t.Errorf("token unexpectedly short (%d chars); 32 random bytes should base64-encode to >= 43 chars", len(a))
	}
}

func TestBuildInternalSecretCarriesBothAuthTokenKeys(t *testing.T) {
	agent := &witwavev1alpha1.WitwaveAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "iris", Namespace: "witwave"},
	}
	sec := buildInternalSecret(agent, "abc-token")
	if sec.Name != "iris-internal" {
		t.Errorf("Secret.Name = %q; want iris-internal", sec.Name)
	}
	if sec.Namespace != "witwave" {
		t.Errorf("Secret.Namespace = %q; want witwave", sec.Namespace)
	}
	for _, k := range []string{internalSecretAuthTokenKey, internalSecretBackendAuthTokenKey} {
		v, ok := sec.Data[k]
		if !ok {
			t.Errorf("Secret.Data missing key %q", k)
			continue
		}
		if string(v) != "abc-token" {
			t.Errorf("Secret.Data[%q] = %q; want abc-token", k, string(v))
		}
	}
	if sec.Labels[labelComponent] != componentInternal {
		t.Errorf("missing or wrong %s label: got %q, want %q",
			labelComponent, sec.Labels[labelComponent], componentInternal)
	}
}

func TestHealInternalSecretKeysFullyEmptyMintsFresh(t *testing.T) {
	updates, needs, err := healInternalSecretKeys(map[string][]byte{})
	if err != nil {
		t.Fatalf("healInternalSecretKeys: %v", err)
	}
	if !needs {
		t.Fatal("expected heal to need an update for empty Data")
	}
	a, ok := updates[internalSecretAuthTokenKey]
	if !ok || len(a) == 0 {
		t.Fatalf("HOOK_EVENTS_AUTH_TOKEN missing in heal updates")
	}
	b, ok := updates[internalSecretBackendAuthTokenKey]
	if !ok || len(b) == 0 {
		t.Fatalf("HARNESS_EVENTS_AUTH_TOKEN missing in heal updates")
	}
	if string(a) != string(b) {
		t.Errorf("both keys should carry the same canonical token; got %q vs %q", a, b)
	}
}

func TestHealInternalSecretKeysPartiallyPopulatedReusesCanonical(t *testing.T) {
	existing := map[string][]byte{
		internalSecretAuthTokenKey: []byte("preserved-token"),
		// internalSecretBackendAuthTokenKey is missing.
	}
	updates, needs, err := healInternalSecretKeys(existing)
	if err != nil {
		t.Fatalf("healInternalSecretKeys: %v", err)
	}
	if !needs {
		t.Fatal("expected heal to need update for the missing key")
	}
	if _, present := updates[internalSecretAuthTokenKey]; present {
		t.Errorf("non-empty existing key %q should not be in updates",
			internalSecretAuthTokenKey)
	}
	if v, ok := updates[internalSecretBackendAuthTokenKey]; !ok || string(v) != "preserved-token" {
		t.Errorf("missing key should heal to canonical %q; got %v",
			"preserved-token", v)
	}
}

func TestHealInternalSecretKeysAllPopulatedNoOp(t *testing.T) {
	existing := map[string][]byte{
		internalSecretAuthTokenKey:        []byte("a"),
		internalSecretBackendAuthTokenKey: []byte("a"),
	}
	updates, needs, err := healInternalSecretKeys(existing)
	if err != nil {
		t.Fatalf("healInternalSecretKeys: %v", err)
	}
	if needs {
		t.Errorf("fully-populated Data should not need heal; got updates=%v", updates)
	}
}

func TestHarnessEnvFromWithInternalPrependsSecretRef(t *testing.T) {
	userEnvFrom := []corev1.EnvFromSource{
		{ConfigMapRef: &corev1.ConfigMapEnvSource{
			LocalObjectReference: corev1.LocalObjectReference{Name: "user-cm"},
		}},
	}
	agent := &witwavev1alpha1.WitwaveAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "iris", Namespace: "witwave"},
		Spec:       witwavev1alpha1.WitwaveAgentSpec{EnvFrom: userEnvFrom},
	}
	out := harnessEnvFromWithInternal(agent)
	if len(out) != 2 {
		t.Fatalf("expected 2 envFrom entries (internal + user); got %d", len(out))
	}
	if out[0].SecretRef == nil || out[0].SecretRef.Name != "iris-internal" {
		t.Errorf("first entry should be the internal Secret ref; got %+v", out[0])
	}
	if out[1].ConfigMapRef == nil || out[1].ConfigMapRef.Name != "user-cm" {
		t.Errorf("second entry should preserve the user's envFrom; got %+v", out[1])
	}
}

func TestBackendEnvFromWithInternalIncludesBoth(t *testing.T) {
	agent := &witwavev1alpha1.WitwaveAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "iris", Namespace: "witwave"},
	}
	b := witwavev1alpha1.BackendSpec{
		Name: "claude",
		Credentials: &witwavev1alpha1.BackendCredentialsSpec{
			ExistingSecret: "iris-claude",
		},
	}
	out := backendEnvFromWithInternal(agent, b)
	if len(out) < 2 {
		t.Fatalf("expected at least 2 entries (internal + creds); got %d", len(out))
	}
	if out[0].SecretRef == nil || out[0].SecretRef.Name != "iris-internal" {
		t.Errorf("first entry should be the internal Secret ref; got %+v", out[0])
	}
	// Subsequent entry should reference the backend's credential Secret.
	if out[1].SecretRef == nil || out[1].SecretRef.Name != "iris-claude" {
		t.Errorf("second entry should be the credential Secret ref; got %+v", out[1])
	}
}
