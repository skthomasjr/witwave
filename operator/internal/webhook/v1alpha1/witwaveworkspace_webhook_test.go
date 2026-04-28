/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package v1alpha1

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	witwavev1alpha1 "github.com/witwave-ai/witwave-operator/api/v1alpha1"
)

func validatorAndCtx() (*WitwaveWorkspaceCustomValidator, context.Context) {
	return &WitwaveWorkspaceCustomValidator{}, context.Background()
}

func mustReject(t *testing.T, err error, mustContain string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected admission to reject; got nil")
	}
	if !strings.Contains(err.Error(), mustContain) {
		t.Fatalf("expected error containing %q, got %q", mustContain, err.Error())
	}
}

func TestWitwaveWorkspaceValidate_VolumeNameDuplicate(t *testing.T) {
	v, ctx := validatorAndCtx()
	size := resource.MustParse("1Gi")
	ws := &witwavev1alpha1.WitwaveWorkspace{
		ObjectMeta: metav1.ObjectMeta{Name: "witwave"},
		Spec: witwavev1alpha1.WitwaveWorkspaceSpec{
			Volumes: []witwavev1alpha1.WitwaveWorkspaceVolume{
				{Name: "source", Size: &size, AccessMode: corev1.ReadWriteMany},
				{Name: "source", Size: &size, AccessMode: corev1.ReadWriteMany},
			},
		},
	}
	_, err := v.ValidateCreate(ctx, ws)
	mustReject(t, err, "duplicates")
}

func TestWitwaveWorkspaceValidate_HostPathRejected(t *testing.T) {
	v, ctx := validatorAndCtx()
	size := resource.MustParse("1Gi")
	ws := &witwavev1alpha1.WitwaveWorkspace{
		ObjectMeta: metav1.ObjectMeta{Name: "witwave"},
		Spec: witwavev1alpha1.WitwaveWorkspaceSpec{
			Volumes: []witwavev1alpha1.WitwaveWorkspaceVolume{
				{
					Name:        "source",
					StorageType: witwavev1alpha1.WitwaveWorkspaceStorageTypeHostPath,
					Size:        &size,
					AccessMode:  corev1.ReadWriteMany,
				},
			},
		},
	}
	_, err := v.ValidateCreate(ctx, ws)
	mustReject(t, err, "hostPath is reserved")
}

func TestWitwaveWorkspaceValidate_RWORejected(t *testing.T) {
	v, ctx := validatorAndCtx()
	size := resource.MustParse("1Gi")
	ws := &witwavev1alpha1.WitwaveWorkspace{
		ObjectMeta: metav1.ObjectMeta{Name: "witwave"},
		Spec: witwavev1alpha1.WitwaveWorkspaceSpec{
			Volumes: []witwavev1alpha1.WitwaveWorkspaceVolume{
				{Name: "source", Size: &size, AccessMode: corev1.ReadWriteOnce},
			},
		},
	}
	_, err := v.ValidateCreate(ctx, ws)
	mustReject(t, err, "ReadWriteMany")
}

func TestWitwaveWorkspaceValidate_RWMAccepted(t *testing.T) {
	v, ctx := validatorAndCtx()
	size := resource.MustParse("1Gi")
	ws := &witwavev1alpha1.WitwaveWorkspace{
		ObjectMeta: metav1.ObjectMeta{Name: "witwave"},
		Spec: witwavev1alpha1.WitwaveWorkspaceSpec{
			Volumes: []witwavev1alpha1.WitwaveWorkspaceVolume{
				{Name: "source", Size: &size, AccessMode: corev1.ReadWriteMany},
			},
		},
	}
	if _, err := v.ValidateCreate(ctx, ws); err != nil {
		t.Fatalf("expected accept, got %v", err)
	}
}

func TestWitwaveWorkspaceValidate_AccessModeDefaultedAcceptsEmpty(t *testing.T) {
	v, ctx := validatorAndCtx()
	size := resource.MustParse("1Gi")
	ws := &witwavev1alpha1.WitwaveWorkspace{
		ObjectMeta: metav1.ObjectMeta{Name: "witwave"},
		Spec: witwavev1alpha1.WitwaveWorkspaceSpec{
			Volumes: []witwavev1alpha1.WitwaveWorkspaceVolume{
				// AccessMode unset → defaulted to RWM
				{Name: "source", Size: &size},
			},
		},
	}
	if _, err := v.ValidateCreate(ctx, ws); err != nil {
		t.Fatalf("expected accept (default RWM), got %v", err)
	}
}

func TestWitwaveWorkspaceValidate_VolumeMountPathDuplicate(t *testing.T) {
	v, ctx := validatorAndCtx()
	size := resource.MustParse("1Gi")
	ws := &witwavev1alpha1.WitwaveWorkspace{
		ObjectMeta: metav1.ObjectMeta{Name: "witwave"},
		Spec: witwavev1alpha1.WitwaveWorkspaceSpec{
			Volumes: []witwavev1alpha1.WitwaveWorkspaceVolume{
				{Name: "a", Size: &size, AccessMode: corev1.ReadWriteMany, MountPath: "/data"},
				{Name: "b", Size: &size, AccessMode: corev1.ReadWriteMany, MountPath: "/data"},
			},
		},
	}
	_, err := v.ValidateCreate(ctx, ws)
	mustReject(t, err, "mountPath")
}

func TestWitwaveWorkspaceValidate_SecretMutualExclusion(t *testing.T) {
	v, ctx := validatorAndCtx()
	ws := &witwavev1alpha1.WitwaveWorkspace{
		ObjectMeta: metav1.ObjectMeta{Name: "witwave"},
		Spec: witwavev1alpha1.WitwaveWorkspaceSpec{
			Secrets: []witwavev1alpha1.WitwaveWorkspaceSecret{
				{Name: "tokens", MountPath: "/etc/secret", EnvFrom: true},
			},
		},
	}
	_, err := v.ValidateCreate(ctx, ws)
	mustReject(t, err, "mutually exclusive")
}

func TestWitwaveWorkspaceValidate_SecretRequiresEither(t *testing.T) {
	v, ctx := validatorAndCtx()
	ws := &witwavev1alpha1.WitwaveWorkspace{
		ObjectMeta: metav1.ObjectMeta{Name: "witwave"},
		Spec: witwavev1alpha1.WitwaveWorkspaceSpec{
			Secrets: []witwavev1alpha1.WitwaveWorkspaceSecret{
				{Name: "tokens"},
			},
		},
	}
	_, err := v.ValidateCreate(ctx, ws)
	mustReject(t, err, "must set either")
}

func TestWitwaveWorkspaceValidate_SecretEnvDuplicate(t *testing.T) {
	v, ctx := validatorAndCtx()
	ws := &witwavev1alpha1.WitwaveWorkspace{
		ObjectMeta: metav1.ObjectMeta{Name: "witwave"},
		Spec: witwavev1alpha1.WitwaveWorkspaceSpec{
			Secrets: []witwavev1alpha1.WitwaveWorkspaceSecret{
				{Name: "tokens", EnvFrom: true},
				{Name: "tokens", EnvFrom: true},
			},
		},
	}
	_, err := v.ValidateCreate(ctx, ws)
	mustReject(t, err, "duplicates")
}

func TestWitwaveWorkspaceValidate_ConfigFileExactlyOne(t *testing.T) {
	v, ctx := validatorAndCtx()
	// neither configMap nor inline set
	ws := &witwavev1alpha1.WitwaveWorkspace{
		ObjectMeta: metav1.ObjectMeta{Name: "witwave"},
		Spec: witwavev1alpha1.WitwaveWorkspaceSpec{
			ConfigFiles: []witwavev1alpha1.WitwaveWorkspaceConfigFile{
				{MountPath: "/x"},
			},
		},
	}
	_, err := v.ValidateCreate(ctx, ws)
	mustReject(t, err, "exactly one")

	// both set
	ws2 := &witwavev1alpha1.WitwaveWorkspace{
		ObjectMeta: metav1.ObjectMeta{Name: "witwave"},
		Spec: witwavev1alpha1.WitwaveWorkspaceSpec{
			ConfigFiles: []witwavev1alpha1.WitwaveWorkspaceConfigFile{
				{
					ConfigMap: "cm",
					Inline:    &witwavev1alpha1.WitwaveWorkspaceInlineFile{Name: "n", Path: "p", Content: "c"},
					MountPath: "/x",
				},
			},
		},
	}
	_, err = v.ValidateCreate(ctx, ws2)
	mustReject(t, err, "exactly one")
}

func TestWitwaveWorkspaceValidate_ConfigFileMountPathDuplicate(t *testing.T) {
	v, ctx := validatorAndCtx()
	ws := &witwavev1alpha1.WitwaveWorkspace{
		ObjectMeta: metav1.ObjectMeta{Name: "witwave"},
		Spec: witwavev1alpha1.WitwaveWorkspaceSpec{
			ConfigFiles: []witwavev1alpha1.WitwaveWorkspaceConfigFile{
				{ConfigMap: "a", MountPath: "/a"},
				{ConfigMap: "b", MountPath: "/a"},
			},
		},
	}
	_, err := v.ValidateCreate(ctx, ws)
	mustReject(t, err, "duplicates")
}

// TestWitwaveAgentValidate_WorkspaceRefsDuplicate covers the agent-side
// admission gate added alongside the WitwaveWorkspace webhook.
func TestWitwaveAgentValidate_WorkspaceRefsDuplicate(t *testing.T) {
	agent := &witwavev1alpha1.WitwaveAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "iris"},
		Spec: witwavev1alpha1.WitwaveAgentSpec{
			Image:    witwavev1alpha1.ImageSpec{Repository: "witwave/harness", Tag: "x"},
			Backends: []witwavev1alpha1.BackendSpec{{Name: "echo", Image: witwavev1alpha1.ImageSpec{Repository: "witwave/echo", Tag: "x"}}},
			WorkspaceRefs: []witwavev1alpha1.WitwaveAgentWorkspaceRef{
				{Name: "witwave"},
				{Name: "witwave"},
			},
		},
	}
	if err := validateWitwaveAgent(agent); err == nil {
		t.Fatalf("expected duplicate WorkspaceRefs to be rejected")
	} else if !strings.Contains(err.Error(), "duplicates") {
		t.Fatalf("expected duplicates error, got %v", err)
	}
}

func TestWitwaveAgentValidate_WorkspaceRefsAcceptedDistinct(t *testing.T) {
	agent := &witwavev1alpha1.WitwaveAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "iris"},
		Spec: witwavev1alpha1.WitwaveAgentSpec{
			Image:    witwavev1alpha1.ImageSpec{Repository: "witwave/harness", Tag: "x"},
			Backends: []witwavev1alpha1.BackendSpec{{Name: "echo", Image: witwavev1alpha1.ImageSpec{Repository: "witwave/echo", Tag: "x"}}},
			WorkspaceRefs: []witwavev1alpha1.WitwaveAgentWorkspaceRef{
				{Name: "witwave"},
				{Name: "shared-data"},
			},
		},
	}
	if err := validateWitwaveAgent(agent); err != nil {
		t.Fatalf("expected distinct WorkspaceRefs to validate, got %v", err)
	}
}
