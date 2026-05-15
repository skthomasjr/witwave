/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Unit tests for the sharedStorage* helpers (#1706). These pure
// functions carry the backward-compat path for legacy SharedStorageRef-
// shaped CRs (claimName-only, no ExistingClaim/StorageType set), and
// the PVC builder's size-parsing error path. Both surfaces are easy to
// regress under refactoring; reconcile-loop integration tests don't
// isolate the decision logic.
package controller

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	witwavev1alpha1 "github.com/witwave-ai/witwave-operator/api/v1alpha1"
)

func mkAgent(spec witwavev1alpha1.WitwaveAgentSpec) *witwavev1alpha1.WitwaveAgent {
	return &witwavev1alpha1.WitwaveAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "iris", Namespace: "default"},
		Spec:       spec,
	}
}

// ----- sharedStorageEnabled --------------------------------------

func TestSharedStorageEnabledNilSpecFalse(t *testing.T) {
	a := mkAgent(witwavev1alpha1.WitwaveAgentSpec{SharedStorage: nil})
	if sharedStorageEnabled(a) {
		t.Errorf("nil SharedStorage must be disabled")
	}
}

func TestSharedStorageEnabledExplicitTrue(t *testing.T) {
	a := mkAgent(witwavev1alpha1.WitwaveAgentSpec{
		SharedStorage: &witwavev1alpha1.SharedStorageSpec{Enabled: true},
	})
	if !sharedStorageEnabled(a) {
		t.Errorf("Enabled=true must produce enabled=true")
	}
}

func TestSharedStorageEnabledLegacyClaimNameOnlyTrue(t *testing.T) {
	// Backward-compat path per the comment at line 280-285: a CR
	// authored against the deprecated SharedStorageRef shape (only
	// ClaimName set, no Enabled/StorageType/ExistingClaim) must
	// render as enabled — silently breaking these CRs on operator
	// upgrade would be an SLA violation.
	a := mkAgent(witwavev1alpha1.WitwaveAgentSpec{
		SharedStorage: &witwavev1alpha1.SharedStorageSpec{
			ClaimName: "legacy-shared-pvc",
		},
	})
	if !sharedStorageEnabled(a) {
		t.Errorf("legacy ClaimName-only spec must be treated as enabled (backward compat)")
	}
}

func TestSharedStorageEnabledLegacyClaimNamePlusExistingClaimFalse(t *testing.T) {
	// When BOTH ClaimName and ExistingClaim are set, the back-compat
	// branch's guard must NOT fire — the operator can't tell which
	// one the user means, so disabled-by-default is the safe behavior.
	a := mkAgent(witwavev1alpha1.WitwaveAgentSpec{
		SharedStorage: &witwavev1alpha1.SharedStorageSpec{
			ClaimName:     "old",
			ExistingClaim: "new",
		},
	})
	if sharedStorageEnabled(a) {
		t.Errorf("ambiguous (ClaimName + ExistingClaim) must NOT auto-enable")
	}
}

func TestSharedStorageEnabledEmptySpecFalse(t *testing.T) {
	a := mkAgent(witwavev1alpha1.WitwaveAgentSpec{
		SharedStorage: &witwavev1alpha1.SharedStorageSpec{},
	})
	if sharedStorageEnabled(a) {
		t.Errorf("empty SharedStorageSpec (no Enabled, no fields) must be disabled")
	}
}

// ----- sharedStorageType -----------------------------------------

func TestSharedStorageTypeDefaultPVC(t *testing.T) {
	a := mkAgent(witwavev1alpha1.WitwaveAgentSpec{
		SharedStorage: &witwavev1alpha1.SharedStorageSpec{Enabled: true},
	})
	if got := sharedStorageType(a); got != witwavev1alpha1.SharedStorageTypePVC {
		t.Errorf("default StorageType: want pvc, got %q", got)
	}
}

func TestSharedStorageTypeNilSpecDefaultPVC(t *testing.T) {
	a := mkAgent(witwavev1alpha1.WitwaveAgentSpec{SharedStorage: nil})
	if got := sharedStorageType(a); got != witwavev1alpha1.SharedStorageTypePVC {
		t.Errorf("nil spec default: want pvc, got %q", got)
	}
}

func TestSharedStorageTypeExplicitPassthrough(t *testing.T) {
	a := mkAgent(witwavev1alpha1.WitwaveAgentSpec{
		SharedStorage: &witwavev1alpha1.SharedStorageSpec{
			Enabled:     true,
			StorageType: witwavev1alpha1.SharedStorageTypeHostPath,
		},
	})
	if got := sharedStorageType(a); got != witwavev1alpha1.SharedStorageTypeHostPath {
		t.Errorf("explicit StorageType pass-through: want hostPath, got %q", got)
	}
}

// ----- sharedStorageExistingClaim --------------------------------

func TestSharedStorageExistingClaimNilSpec(t *testing.T) {
	a := mkAgent(witwavev1alpha1.WitwaveAgentSpec{SharedStorage: nil})
	if got := sharedStorageExistingClaim(a); got != "" {
		t.Errorf("nil spec: want empty, got %q", got)
	}
}

func TestSharedStorageExistingClaimPrecedenceOrder(t *testing.T) {
	// ExistingClaim wins, then ClaimName, then operator-managed default.
	a := mkAgent(witwavev1alpha1.WitwaveAgentSpec{
		SharedStorage: &witwavev1alpha1.SharedStorageSpec{
			ExistingClaim: "primary",
			ClaimName:     "ignored",
		},
	})
	if got := sharedStorageExistingClaim(a); got != "primary" {
		t.Errorf("ExistingClaim precedence: want primary, got %q", got)
	}
}

func TestSharedStorageExistingClaimFallsBackToClaimName(t *testing.T) {
	a := mkAgent(witwavev1alpha1.WitwaveAgentSpec{
		SharedStorage: &witwavev1alpha1.SharedStorageSpec{ClaimName: "legacy"},
	})
	if got := sharedStorageExistingClaim(a); got != "legacy" {
		t.Errorf("ClaimName fallback: want legacy, got %q", got)
	}
}

func TestSharedStorageExistingClaimFallsBackToOperatorManaged(t *testing.T) {
	a := mkAgent(witwavev1alpha1.WitwaveAgentSpec{
		SharedStorage: &witwavev1alpha1.SharedStorageSpec{Enabled: true},
	})
	if got := sharedStorageExistingClaim(a); got != "iris-shared" {
		t.Errorf("operator-managed fallback: want iris-shared, got %q", got)
	}
}

// ----- sharedStorageMountPath ------------------------------------

func TestSharedStorageMountPathDefault(t *testing.T) {
	a := mkAgent(witwavev1alpha1.WitwaveAgentSpec{
		SharedStorage: &witwavev1alpha1.SharedStorageSpec{Enabled: true},
	})
	if got := sharedStorageMountPath(a); got == "" {
		t.Errorf("default MountPath must be non-empty (chart default /data/shared)")
	}
}

func TestSharedStorageMountPathExplicit(t *testing.T) {
	a := mkAgent(witwavev1alpha1.WitwaveAgentSpec{
		SharedStorage: &witwavev1alpha1.SharedStorageSpec{
			Enabled:   true,
			MountPath: "/mnt/team",
		},
	})
	if got := sharedStorageMountPath(a); got != "/mnt/team" {
		t.Errorf("explicit MountPath: want /mnt/team, got %q", got)
	}
}

// ----- sharedStorageOperatorManaged ------------------------------

func TestSharedStorageOperatorManagedDefaultPVC(t *testing.T) {
	a := mkAgent(witwavev1alpha1.WitwaveAgentSpec{
		SharedStorage: &witwavev1alpha1.SharedStorageSpec{Enabled: true},
	})
	if !sharedStorageOperatorManaged(a) {
		t.Errorf("Enabled + default StorageType must be operator-managed")
	}
}

func TestSharedStorageOperatorManagedFalseWhenExistingClaim(t *testing.T) {
	a := mkAgent(witwavev1alpha1.WitwaveAgentSpec{
		SharedStorage: &witwavev1alpha1.SharedStorageSpec{
			Enabled:       true,
			ExistingClaim: "user-supplied",
		},
	})
	if sharedStorageOperatorManaged(a) {
		t.Errorf("ExistingClaim must short-circuit operator-managed=false")
	}
}

func TestSharedStorageOperatorManagedFalseWhenHostPath(t *testing.T) {
	a := mkAgent(witwavev1alpha1.WitwaveAgentSpec{
		SharedStorage: &witwavev1alpha1.SharedStorageSpec{
			Enabled:     true,
			StorageType: witwavev1alpha1.SharedStorageTypeHostPath,
		},
	})
	if sharedStorageOperatorManaged(a) {
		t.Errorf("hostPath StorageType must short-circuit operator-managed=false")
	}
}

// ----- buildSharedStoragePVC -------------------------------------

func TestBuildSharedStoragePVCNotManagedReturnsNil(t *testing.T) {
	a := mkAgent(witwavev1alpha1.WitwaveAgentSpec{
		SharedStorage: &witwavev1alpha1.SharedStorageSpec{
			Enabled:       true,
			ExistingClaim: "user-supplied",
		},
	})
	pvc, err := buildSharedStoragePVC(a)
	if err != nil {
		t.Fatalf("not-managed must NOT raise an error; got %v", err)
	}
	if pvc != nil {
		t.Errorf("not-managed must produce nil PVC; got %+v", pvc)
	}
}

func TestBuildSharedStoragePVCDefaultSize(t *testing.T) {
	a := mkAgent(witwavev1alpha1.WitwaveAgentSpec{
		SharedStorage: &witwavev1alpha1.SharedStorageSpec{Enabled: true},
	})
	pvc, err := buildSharedStoragePVC(a)
	if err != nil {
		t.Fatalf("default Size must parse cleanly; got %v", err)
	}
	if pvc == nil {
		t.Fatalf("operator-managed Enabled=true must produce a PVC")
	}
	if pvc.Spec.Resources.Requests.Storage().IsZero() {
		t.Errorf("PVC Storage request must be populated from default Size")
	}
}

func TestBuildSharedStoragePVCInvalidSizeReturnsError(t *testing.T) {
	a := mkAgent(witwavev1alpha1.WitwaveAgentSpec{
		SharedStorage: &witwavev1alpha1.SharedStorageSpec{
			Enabled: true,
			Size:    "not-a-quantity",
		},
	})
	pvc, err := buildSharedStoragePVC(a)
	if err == nil {
		t.Fatalf("invalid Size must return an error; got pvc=%+v", pvc)
	}
	if !strings.Contains(err.Error(), "sharedStorage.size") {
		t.Errorf("error must name the offending field; got %q", err.Error())
	}
}

func TestBuildSharedStoragePVCDefaultAccessModesReadWriteMany(t *testing.T) {
	a := mkAgent(witwavev1alpha1.WitwaveAgentSpec{
		SharedStorage: &witwavev1alpha1.SharedStorageSpec{Enabled: true},
	})
	pvc, err := buildSharedStoragePVC(a)
	if err != nil || pvc == nil {
		t.Fatalf("expected operator-managed PVC; err=%v pvc=%v", err, pvc)
	}
	if len(pvc.Spec.AccessModes) != 1 || pvc.Spec.AccessModes[0] != corev1.ReadWriteMany {
		t.Errorf("default AccessModes: want [ReadWriteMany], got %v", pvc.Spec.AccessModes)
	}
}

func TestBuildSharedStoragePVCExplicitStorageClassName(t *testing.T) {
	a := mkAgent(witwavev1alpha1.WitwaveAgentSpec{
		SharedStorage: &witwavev1alpha1.SharedStorageSpec{
			Enabled:          true,
			StorageClassName: "fast-ssd",
		},
	})
	pvc, err := buildSharedStoragePVC(a)
	if err != nil || pvc == nil {
		t.Fatalf("expected PVC; err=%v", err)
	}
	if pvc.Spec.StorageClassName == nil || *pvc.Spec.StorageClassName != "fast-ssd" {
		t.Errorf("StorageClassName: want pointer-to-fast-ssd; got %v", pvc.Spec.StorageClassName)
	}
}

// ----- sharedStorageLabels stamps componentSharedStorage --------

func TestSharedStorageLabelsUsesComponentSharedStorage(t *testing.T) {
	// Per the comment at line 351-355, the distinct component label keeps
	// backend-PVC cleanup from sweeping the shared PVC.
	a := mkAgent(witwavev1alpha1.WitwaveAgentSpec{
		SharedStorage: &witwavev1alpha1.SharedStorageSpec{Enabled: true},
	})
	got := sharedStorageLabels(a)
	if got[labelComponent] != componentSharedStorage {
		t.Errorf("labelComponent: want %q, got %q", componentSharedStorage, got[labelComponent])
	}
	if got[labelComponent] == componentAgent {
		t.Errorf("labelComponent must NOT collide with componentAgent (cleanup-sweep safety)")
	}
}

// ----- runtimeStorage helpers -----------------------------------

func TestRuntimeStorageDefaults(t *testing.T) {
	a := mkAgent(witwavev1alpha1.WitwaveAgentSpec{
		RuntimeStorage: &witwavev1alpha1.RuntimeStorageSpec{Enabled: true},
	})
	if !runtimeStorageEnabled(a) {
		t.Fatalf("Enabled=true must enable runtime storage")
	}
	if got := runtimeStoragePVCName(a); got != "iris-runtime-data" {
		t.Errorf("runtimeStoragePVCName = %q, want iris-runtime-data", got)
	}
	mounts := runtimeStorageMounts(a)
	if len(mounts) != 2 {
		t.Fatalf("default runtime mounts = %d, want 2", len(mounts))
	}
	if mounts[0].SubPath != runtimeLogsSubPath || mounts[0].MountPath != runtimeLogsMountPath {
		t.Errorf("logs mount = %+v", mounts[0])
	}
	if mounts[1].SubPath != runtimeStateSubPath || mounts[1].MountPath != runtimeStateMountPath {
		t.Errorf("state mount = %+v", mounts[1])
	}
	if !runtimeStorageMountsPath(a, runtimeStateMountPath) {
		t.Errorf("runtime storage should report state mount present")
	}
}

func TestBuildRuntimeStoragePVCDefaultAccessModesReadWriteOnce(t *testing.T) {
	a := mkAgent(witwavev1alpha1.WitwaveAgentSpec{
		RuntimeStorage: &witwavev1alpha1.RuntimeStorageSpec{Enabled: true},
	})
	pvc, err := buildRuntimeStoragePVC(a)
	if err != nil || pvc == nil {
		t.Fatalf("expected runtime PVC; err=%v pvc=%v", err, pvc)
	}
	if pvc.Labels[labelComponent] != componentRuntimeStorage {
		t.Errorf("labelComponent: want %q, got %q", componentRuntimeStorage, pvc.Labels[labelComponent])
	}
	if len(pvc.Spec.AccessModes) != 1 || pvc.Spec.AccessModes[0] != corev1.ReadWriteOnce {
		t.Errorf("default AccessModes: want [ReadWriteOnce], got %v", pvc.Spec.AccessModes)
	}
}

func TestBuildRuntimeStoragePVCInvalidSizeReturnsError(t *testing.T) {
	a := mkAgent(witwavev1alpha1.WitwaveAgentSpec{
		RuntimeStorage: &witwavev1alpha1.RuntimeStorageSpec{
			Enabled: true,
			Size:    "not-a-quantity",
		},
	})
	pvc, err := buildRuntimeStoragePVC(a)
	if err == nil {
		t.Fatalf("invalid Size must return an error; got pvc=%+v", pvc)
	}
	if !strings.Contains(err.Error(), "runtimeStorage.size") {
		t.Errorf("error must name the offending field; got %q", err.Error())
	}
}

func TestBuildDeploymentRuntimeStorageMountsHarnessAndSetsTaskStore(t *testing.T) {
	a := mkAgent(witwavev1alpha1.WitwaveAgentSpec{
		Image: witwavev1alpha1.ImageSpec{Repository: "ghcr.io/witwave-ai/images/harness", Tag: "test"},
		RuntimeStorage: &witwavev1alpha1.RuntimeStorageSpec{
			Enabled: true,
		},
		Backends: []witwavev1alpha1.BackendSpec{{
			Name:  "claude",
			Image: witwavev1alpha1.ImageSpec{Repository: "ghcr.io/witwave-ai/images/claude", Tag: "test"},
		}},
	})
	dep := buildDeployment(a, "test", nil)
	harness := dep.Spec.Template.Spec.Containers[0]
	if !volumeMountsHavePath(harness.VolumeMounts, runtimeLogsMountPath) {
		t.Errorf("harness missing runtime logs mount: %+v", harness.VolumeMounts)
	}
	if !volumeMountsHavePath(harness.VolumeMounts, runtimeStateMountPath) {
		t.Errorf("harness missing runtime state mount: %+v", harness.VolumeMounts)
	}
	if !envListHasName(harness.Env, "TASK_STORE_PATH") {
		t.Errorf("harness missing TASK_STORE_PATH env: %+v", harness.Env)
	}
}

func TestBuildDeploymentBackendStateMountSetsTaskStore(t *testing.T) {
	a := mkAgent(witwavev1alpha1.WitwaveAgentSpec{
		Image: witwavev1alpha1.ImageSpec{Repository: "ghcr.io/witwave-ai/images/harness", Tag: "test"},
		Backends: []witwavev1alpha1.BackendSpec{{
			Name:  "claude",
			Image: witwavev1alpha1.ImageSpec{Repository: "ghcr.io/witwave-ai/images/claude", Tag: "test"},
			Storage: &witwavev1alpha1.BackendStorageSpec{
				Enabled: true,
				Mounts: []witwavev1alpha1.BackendStorageMount{{
					SubPath:   runtimeStateSubPath,
					MountPath: runtimeStateMountPath,
				}},
			},
		}},
	})
	dep := buildDeployment(a, "test", nil)
	backend := dep.Spec.Template.Spec.Containers[1]
	if !envListHasName(backend.Env, "TASK_STORE_PATH") {
		t.Errorf("backend missing TASK_STORE_PATH env: %+v", backend.Env)
	}
}
