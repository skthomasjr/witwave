/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package v1alpha1

import (
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	nyxv1alpha1 "github.com/nyx-ai/nyx-operator/api/v1alpha1"
)

// Tests for the #832 admission-webhook invariants. Each validator is
// exercised in its own t.Run subtest — verifies that a known-bad spec
// is rejected, a known-good spec passes, and edge cases around optional
// fields do not generate spurious errors.

func newBaseAgent() *nyxv1alpha1.NyxAgent {
	return &nyxv1alpha1.NyxAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "iris", Namespace: "nyx"},
		Spec: nyxv1alpha1.NyxAgentSpec{
			Port: 8000,
		},
	}
}

func assertRejectedWith(t *testing.T, err error, substr string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error containing %q, got nil", substr)
	}
	if !strings.Contains(err.Error(), substr) {
		t.Fatalf("expected error containing %q, got: %v", substr, err)
	}
}

func TestValidatePreStopGrace(t *testing.T) {
	t.Run("disabled is always OK", func(t *testing.T) {
		a := newBaseAgent()
		if err := validatePreStopGrace(a); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("enabled + grace default (30s) + delay 25s OK", func(t *testing.T) {
		a := newBaseAgent()
		a.Spec.PreStop = &nyxv1alpha1.PreStopSpec{Enabled: true, DelaySeconds: 25}
		if err := validatePreStopGrace(a); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("enabled + delay >= grace rejected", func(t *testing.T) {
		a := newBaseAgent()
		grace := int64(30)
		a.Spec.TerminationGracePeriodSeconds = &grace
		a.Spec.PreStop = &nyxv1alpha1.PreStopSpec{Enabled: true, DelaySeconds: 30}
		assertRejectedWith(t, validatePreStopGrace(a), "strictly less than")
	})
	t.Run("enabled + delay == default grace rejected", func(t *testing.T) {
		a := newBaseAgent()
		// TerminationGracePeriodSeconds unset -> default 30
		a.Spec.PreStop = &nyxv1alpha1.PreStopSpec{Enabled: true, DelaySeconds: 30}
		assertRejectedWith(t, validatePreStopGrace(a), "K8s default 30")
	})
}

func TestValidatePodDisruptionBudget(t *testing.T) {
	t.Run("disabled is OK", func(t *testing.T) {
		a := newBaseAgent()
		a.Spec.PodDisruptionBudget = &nyxv1alpha1.PodDisruptionBudgetSpec{Enabled: false}
		if err := validatePodDisruptionBudget(a); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("enabled + only minAvailable OK", func(t *testing.T) {
		a := newBaseAgent()
		min := int32(1)
		a.Spec.PodDisruptionBudget = &nyxv1alpha1.PodDisruptionBudgetSpec{Enabled: true, MinAvailable: &min}
		if err := validatePodDisruptionBudget(a); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("enabled + only maxUnavailable OK", func(t *testing.T) {
		a := newBaseAgent()
		max := int32(1)
		a.Spec.PodDisruptionBudget = &nyxv1alpha1.PodDisruptionBudgetSpec{Enabled: true, MaxUnavailable: &max}
		if err := validatePodDisruptionBudget(a); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("enabled + neither rejected", func(t *testing.T) {
		a := newBaseAgent()
		a.Spec.PodDisruptionBudget = &nyxv1alpha1.PodDisruptionBudgetSpec{Enabled: true}
		assertRejectedWith(t, validatePodDisruptionBudget(a), "exactly one of")
	})
	t.Run("enabled + both rejected", func(t *testing.T) {
		a := newBaseAgent()
		min := int32(1)
		max := int32(1)
		a.Spec.PodDisruptionBudget = &nyxv1alpha1.PodDisruptionBudgetSpec{
			Enabled: true, MinAvailable: &min, MaxUnavailable: &max,
		}
		assertRejectedWith(t, validatePodDisruptionBudget(a), "exactly one of")
	})
}

func TestValidateBackendStorageSize(t *testing.T) {
	backendWith := func(storage *nyxv1alpha1.BackendStorageSpec) *nyxv1alpha1.NyxAgent {
		a := newBaseAgent()
		a.Spec.Backends = []nyxv1alpha1.BackendSpec{
			{Name: "claude", Storage: storage},
		}
		return a
	}

	t.Run("storage disabled is OK", func(t *testing.T) {
		if err := validateBackendStorageSize(backendWith(&nyxv1alpha1.BackendStorageSpec{Enabled: false})); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("enabled + valid size OK", func(t *testing.T) {
		if err := validateBackendStorageSize(backendWith(&nyxv1alpha1.BackendStorageSpec{Enabled: true, Size: "1Gi"})); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("enabled + existingClaim OK (size ignored)", func(t *testing.T) {
		if err := validateBackendStorageSize(backendWith(&nyxv1alpha1.BackendStorageSpec{Enabled: true, ExistingClaim: "existing"})); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("enabled + empty size rejected", func(t *testing.T) {
		assertRejectedWith(t, validateBackendStorageSize(backendWith(&nyxv1alpha1.BackendStorageSpec{Enabled: true, Size: ""})), "required when enabled")
	})
	t.Run("enabled + garbage size rejected", func(t *testing.T) {
		assertRejectedWith(t, validateBackendStorageSize(backendWith(&nyxv1alpha1.BackendStorageSpec{Enabled: true, Size: "one-gigabyte"})), "invalid resource.Quantity")
	})
}

func TestValidateGitMappingRefs(t *testing.T) {
	t.Run("no mappings OK", func(t *testing.T) {
		if err := validateGitMappingRefs(newBaseAgent()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("mapping referencing declared sync OK", func(t *testing.T) {
		a := newBaseAgent()
		a.Spec.GitSyncs = []nyxv1alpha1.GitSyncSpec{{Name: "prompts"}}
		a.Spec.Backends = []nyxv1alpha1.BackendSpec{
			{Name: "claude", GitMappings: []nyxv1alpha1.GitMappingSpec{
				{GitSync: "prompts", Src: "hello.md", Dest: "/home/agent/.claude/skills/hello.md"},
			}},
		}
		if err := validateGitMappingRefs(a); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("mapping referencing undeclared sync rejected", func(t *testing.T) {
		a := newBaseAgent()
		a.Spec.GitSyncs = []nyxv1alpha1.GitSyncSpec{{Name: "prompts"}}
		a.Spec.Backends = []nyxv1alpha1.BackendSpec{
			{Name: "claude", GitMappings: []nyxv1alpha1.GitMappingSpec{
				{GitSync: "missing", Src: "x", Dest: "/x"},
			}},
		}
		assertRejectedWith(t, validateGitMappingRefs(a), "does not name any entry in spec.gitSyncs")
	})
}

func TestValidateSharedStorageHostPath(t *testing.T) {
	t.Run("disabled OK", func(t *testing.T) {
		if err := validateSharedStorageHostPath(newBaseAgent()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("pvc mode skips hostPath check", func(t *testing.T) {
		a := newBaseAgent()
		a.Spec.SharedStorage = &nyxv1alpha1.SharedStorageSpec{
			Enabled: true, StorageType: nyxv1alpha1.SharedStorageTypePVC,
		}
		if err := validateSharedStorageHostPath(a); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("hostPath + valid absolute path OK", func(t *testing.T) {
		a := newBaseAgent()
		a.Spec.SharedStorage = &nyxv1alpha1.SharedStorageSpec{
			Enabled: true, StorageType: nyxv1alpha1.SharedStorageTypeHostPath, HostPath: "/var/data/nyx",
		}
		if err := validateSharedStorageHostPath(a); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("hostPath + empty rejected", func(t *testing.T) {
		a := newBaseAgent()
		a.Spec.SharedStorage = &nyxv1alpha1.SharedStorageSpec{
			Enabled: true, StorageType: nyxv1alpha1.SharedStorageTypeHostPath,
		}
		assertRejectedWith(t, validateSharedStorageHostPath(a), "required when storageType=hostPath")
	})
	t.Run("hostPath + relative path rejected", func(t *testing.T) {
		a := newBaseAgent()
		a.Spec.SharedStorage = &nyxv1alpha1.SharedStorageSpec{
			Enabled: true, StorageType: nyxv1alpha1.SharedStorageTypeHostPath, HostPath: "relative/path",
		}
		assertRejectedWith(t, validateSharedStorageHostPath(a), "absolute path")
	})
	t.Run("hostPath + dotdot rejected", func(t *testing.T) {
		a := newBaseAgent()
		a.Spec.SharedStorage = &nyxv1alpha1.SharedStorageSpec{
			Enabled: true, StorageType: nyxv1alpha1.SharedStorageTypeHostPath, HostPath: "/var/../etc",
		}
		assertRejectedWith(t, validateSharedStorageHostPath(a), "must not contain '..'")
	})
}
