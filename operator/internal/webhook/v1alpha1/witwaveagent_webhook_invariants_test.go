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

	witwavev1alpha1 "github.com/witwave-ai/witwave-operator/api/v1alpha1"
)

// Tests for the #832 admission-webhook invariants. Each validator is
// exercised in its own t.Run subtest — verifies that a known-bad spec
// is rejected, a known-good spec passes, and edge cases around optional
// fields do not generate spurious errors.

func newBaseAgent() *witwavev1alpha1.WitwaveAgent {
	return &witwavev1alpha1.WitwaveAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "iris", Namespace: "witwave"},
		Spec: witwavev1alpha1.WitwaveAgentSpec{
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
		a.Spec.PreStop = &witwavev1alpha1.PreStopSpec{Enabled: true, DelaySeconds: 25}
		if err := validatePreStopGrace(a); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("enabled + delay >= grace rejected", func(t *testing.T) {
		a := newBaseAgent()
		grace := int64(30)
		a.Spec.TerminationGracePeriodSeconds = &grace
		a.Spec.PreStop = &witwavev1alpha1.PreStopSpec{Enabled: true, DelaySeconds: 30}
		assertRejectedWith(t, validatePreStopGrace(a), "strictly less than")
	})
	t.Run("enabled + delay == default grace rejected", func(t *testing.T) {
		a := newBaseAgent()
		// TerminationGracePeriodSeconds unset -> default 30
		a.Spec.PreStop = &witwavev1alpha1.PreStopSpec{Enabled: true, DelaySeconds: 30}
		assertRejectedWith(t, validatePreStopGrace(a), "K8s default 30")
	})
}

func TestValidatePodDisruptionBudget(t *testing.T) {
	t.Run("disabled is OK", func(t *testing.T) {
		a := newBaseAgent()
		a.Spec.PodDisruptionBudget = &witwavev1alpha1.PodDisruptionBudgetSpec{Enabled: false}
		if err := validatePodDisruptionBudget(a); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("enabled + only minAvailable OK", func(t *testing.T) {
		a := newBaseAgent()
		min := int32(1)
		a.Spec.PodDisruptionBudget = &witwavev1alpha1.PodDisruptionBudgetSpec{Enabled: true, MinAvailable: &min}
		if err := validatePodDisruptionBudget(a); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("enabled + only maxUnavailable OK", func(t *testing.T) {
		a := newBaseAgent()
		max := int32(1)
		a.Spec.PodDisruptionBudget = &witwavev1alpha1.PodDisruptionBudgetSpec{Enabled: true, MaxUnavailable: &max}
		if err := validatePodDisruptionBudget(a); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("enabled + neither rejected", func(t *testing.T) {
		a := newBaseAgent()
		a.Spec.PodDisruptionBudget = &witwavev1alpha1.PodDisruptionBudgetSpec{Enabled: true}
		assertRejectedWith(t, validatePodDisruptionBudget(a), "exactly one of")
	})
	t.Run("enabled + both rejected", func(t *testing.T) {
		a := newBaseAgent()
		min := int32(1)
		max := int32(1)
		a.Spec.PodDisruptionBudget = &witwavev1alpha1.PodDisruptionBudgetSpec{
			Enabled: true, MinAvailable: &min, MaxUnavailable: &max,
		}
		assertRejectedWith(t, validatePodDisruptionBudget(a), "exactly one of")
	})
}

func TestValidateBackendStorageSize(t *testing.T) {
	backendWith := func(storage *witwavev1alpha1.BackendStorageSpec) *witwavev1alpha1.WitwaveAgent {
		a := newBaseAgent()
		a.Spec.Backends = []witwavev1alpha1.BackendSpec{
			{Name: "claude", Storage: storage},
		}
		return a
	}

	t.Run("storage disabled is OK", func(t *testing.T) {
		if err := validateBackendStorageSize(backendWith(&witwavev1alpha1.BackendStorageSpec{Enabled: false})); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("enabled + valid size OK", func(t *testing.T) {
		if err := validateBackendStorageSize(backendWith(&witwavev1alpha1.BackendStorageSpec{Enabled: true, Size: "1Gi"})); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("enabled + existingClaim OK (size ignored)", func(t *testing.T) {
		if err := validateBackendStorageSize(backendWith(&witwavev1alpha1.BackendStorageSpec{Enabled: true, ExistingClaim: "existing"})); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("enabled + empty size rejected", func(t *testing.T) {
		assertRejectedWith(t, validateBackendStorageSize(backendWith(&witwavev1alpha1.BackendStorageSpec{Enabled: true, Size: ""})), "required when enabled")
	})
	t.Run("enabled + garbage size rejected", func(t *testing.T) {
		assertRejectedWith(t, validateBackendStorageSize(backendWith(&witwavev1alpha1.BackendStorageSpec{Enabled: true, Size: "one-gigabyte"})), "invalid resource.Quantity")
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
		a.Spec.GitSyncs = []witwavev1alpha1.GitSyncSpec{{Name: "prompts"}}
		a.Spec.Backends = []witwavev1alpha1.BackendSpec{
			{Name: "claude", GitMappings: []witwavev1alpha1.GitMappingSpec{
				{GitSync: "prompts", Src: "hello.md", Dest: "/home/agent/.claude/skills/hello.md"},
			}},
		}
		if err := validateGitMappingRefs(a); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("mapping referencing undeclared sync rejected", func(t *testing.T) {
		a := newBaseAgent()
		a.Spec.GitSyncs = []witwavev1alpha1.GitSyncSpec{{Name: "prompts"}}
		a.Spec.Backends = []witwavev1alpha1.BackendSpec{
			{Name: "claude", GitMappings: []witwavev1alpha1.GitMappingSpec{
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
		a.Spec.SharedStorage = &witwavev1alpha1.SharedStorageSpec{
			Enabled: true, StorageType: witwavev1alpha1.SharedStorageTypePVC,
		}
		if err := validateSharedStorageHostPath(a); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("hostPath + valid absolute path OK", func(t *testing.T) {
		a := newBaseAgent()
		a.Spec.SharedStorage = &witwavev1alpha1.SharedStorageSpec{
			Enabled: true, StorageType: witwavev1alpha1.SharedStorageTypeHostPath, HostPath: "/var/data/witwave",
		}
		if err := validateSharedStorageHostPath(a); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("hostPath + empty rejected", func(t *testing.T) {
		a := newBaseAgent()
		a.Spec.SharedStorage = &witwavev1alpha1.SharedStorageSpec{
			Enabled: true, StorageType: witwavev1alpha1.SharedStorageTypeHostPath,
		}
		assertRejectedWith(t, validateSharedStorageHostPath(a), "required when storageType=hostPath")
	})
	t.Run("hostPath + relative path rejected", func(t *testing.T) {
		a := newBaseAgent()
		a.Spec.SharedStorage = &witwavev1alpha1.SharedStorageSpec{
			Enabled: true, StorageType: witwavev1alpha1.SharedStorageTypeHostPath, HostPath: "relative/path",
		}
		assertRejectedWith(t, validateSharedStorageHostPath(a), "absolute path")
	})
	t.Run("hostPath + dotdot rejected", func(t *testing.T) {
		a := newBaseAgent()
		a.Spec.SharedStorage = &witwavev1alpha1.SharedStorageSpec{
			Enabled: true, StorageType: witwavev1alpha1.SharedStorageTypeHostPath, HostPath: "/var/../etc",
		}
		assertRejectedWith(t, validateSharedStorageHostPath(a), "must not contain '..'")
	})
}

// TestValidatePorts covers the metrics-port reservation enforced on top
// of the CRD-level Maximum=64535 ceiling. The implicit metrics port is
// app_port+1000 (#687/#836) — anything above 64535 risks an out-of-range
// metrics listener, anything above 63535 with metrics enabled is the
// same problem one step earlier.
func TestValidatePorts(t *testing.T) {
	t.Run("port 8000 with metrics enabled accepted", func(t *testing.T) {
		a := newBaseAgent()
		a.Spec.Port = 8000
		a.Spec.Metrics = witwavev1alpha1.MetricsSpec{Enabled: true}
		if err := validateAppPorts(a); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("port 64535 with metrics disabled accepted", func(t *testing.T) {
		a := newBaseAgent()
		a.Spec.Port = 64535
		a.Spec.Metrics = witwavev1alpha1.MetricsSpec{Enabled: false}
		if err := validateAppPorts(a); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("port 64535 with metrics enabled rejected", func(t *testing.T) {
		a := newBaseAgent()
		a.Spec.Port = 64535
		a.Spec.Metrics = witwavev1alpha1.MetricsSpec{Enabled: true}
		assertRejectedWith(t, validateAppPorts(a), "metrics port is app_port+1000")
	})
	t.Run("port 65000 rejected regardless of metrics", func(t *testing.T) {
		a := newBaseAgent()
		a.Spec.Port = 65000
		a.Spec.Metrics = witwavev1alpha1.MetricsSpec{Enabled: false}
		assertRejectedWith(t, validateAppPorts(a), "exceeds maximum allowed port")

		a2 := newBaseAgent()
		a2.Spec.Port = 65000
		a2.Spec.Metrics = witwavev1alpha1.MetricsSpec{Enabled: true}
		assertRejectedWith(t, validateAppPorts(a2), "exceeds maximum allowed port")
	})
	t.Run("backend port over-ceiling rejected", func(t *testing.T) {
		a := newBaseAgent()
		a.Spec.Port = 8000
		a.Spec.Metrics = witwavev1alpha1.MetricsSpec{Enabled: true}
		a.Spec.Backends = []witwavev1alpha1.BackendSpec{{
			Name: "claude",
			Port: 64535,
		}}
		assertRejectedWith(t, validateAppPorts(a), "spec.backends[0].port")
	})
	t.Run("zero port skipped", func(t *testing.T) {
		a := newBaseAgent()
		a.Spec.Port = 0
		a.Spec.Metrics = witwavev1alpha1.MetricsSpec{Enabled: true}
		if err := validateAppPorts(a); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}
