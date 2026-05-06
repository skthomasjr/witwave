/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package controller

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	witwavev1alpha1 "github.com/witwave-ai/witwave-operator/api/v1alpha1"
)

// fakeWitwaveWorkspaceScheme returns a runtime scheme registered with the witwave
// CRDs and the corev1 / apps types the workspace reconciler touches.
func fakeWitwaveWorkspaceScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := scheme.AddToScheme(s); err != nil {
		t.Fatalf("scheme: %v", err)
	}
	if err := witwavev1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("witwave scheme: %v", err)
	}
	return s
}

func TestWitwaveWorkspaceVolumePVCNameDeterministic(t *testing.T) {
	a := WitwaveWorkspaceVolumePVCName("witwave", "source")
	b := WitwaveWorkspaceVolumePVCName("witwave", "source")
	if a != b {
		t.Fatalf("non-deterministic: %q vs %q", a, b)
	}
	const want = "witwave-vol-source"
	if a != want {
		t.Fatalf("got %q want %q", a, want)
	}
}

func TestWitwaveWorkspaceInlineConfigMapNameSanitises(t *testing.T) {
	got := WitwaveWorkspaceInlineConfigMapName("witwave", "Repo.Info")
	const want = "witwave-cf-repo-info"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestWitwaveWorkspaceVolumeMountPathDefault(t *testing.T) {
	ws := &witwavev1alpha1.WitwaveWorkspace{
		ObjectMeta: metav1.ObjectMeta{Name: "witwave"},
	}
	vol := &witwavev1alpha1.WitwaveWorkspaceVolume{Name: "source"}
	got := workspaceVolumeMountPath(ws, vol)
	const want = "/workspaces/witwave/source"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestWitwaveWorkspaceVolumeMountPathExplicit(t *testing.T) {
	ws := &witwavev1alpha1.WitwaveWorkspace{
		ObjectMeta: metav1.ObjectMeta{Name: "witwave"},
	}
	vol := &witwavev1alpha1.WitwaveWorkspaceVolume{
		Name:      "source",
		MountPath: "/srv/source",
	}
	if got := workspaceVolumeMountPath(ws, vol); got != "/srv/source" {
		t.Fatalf("explicit mountPath ignored; got %q", got)
	}
}

// TestWitwaveWorkspaceReconcileVolumesProvisioning is a happy-path envtest-free
// reconcile using the controller-runtime fake client. It pins the
// invariant that one PVC per Spec.Volumes entry is created with the
// expected name + access mode + IsControlledBy chain.
func TestWitwaveWorkspaceReconcileVolumesProvisioning(t *testing.T) {
	s := fakeWitwaveWorkspaceScheme(t)
	size := resource.MustParse("10Gi")
	ws := &witwavev1alpha1.WitwaveWorkspace{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "witwave",
			Namespace: "team",
			UID:       types.UID("u-1"),
		},
		Spec: witwavev1alpha1.WitwaveWorkspaceSpec{
			Volumes: []witwavev1alpha1.WitwaveWorkspaceVolume{
				{
					Name:        "source",
					StorageType: witwavev1alpha1.WitwaveWorkspaceStorageTypePVC,
					Size:        &size,
					AccessMode:  corev1.ReadWriteMany,
				},
			},
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&witwavev1alpha1.WitwaveWorkspace{}).
		WithObjects(ws).
		Build()
	r := &WitwaveWorkspaceReconciler{Client: c, Scheme: s}
	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "team", Name: "witwave"},
	})
	if err != nil {
		t.Fatalf("first reconcile (finalizer add): %v", err)
	}
	// Second reconcile actually provisions the PVC.
	if _, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "team", Name: "witwave"},
	}); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	pvc := &corev1.PersistentVolumeClaim{}
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "team", Name: "witwave-vol-source"}, pvc); err != nil {
		t.Fatalf("PVC not provisioned: %v", err)
	}
	if !metav1.IsControlledBy(pvc, ws) {
		t.Fatalf("PVC missing controller ownerRef to WitwaveWorkspace")
	}
	if pvc.Spec.AccessModes[0] != corev1.ReadWriteMany {
		t.Fatalf("expected RWM access mode, got %v", pvc.Spec.AccessModes)
	}
	if got := pvc.Spec.Resources.Requests[corev1.ResourceStorage]; got.Cmp(size) != 0 {
		t.Fatalf("PVC size: got %s want %s", got.String(), size.String())
	}
}

// TestWitwaveWorkspaceReconcileInlineConfigMapRenders covers the IsControlledBy +
// label dual-check pattern the design doc commits to.
func TestWitwaveWorkspaceReconcileInlineConfigMapRenders(t *testing.T) {
	s := fakeWitwaveWorkspaceScheme(t)
	ws := &witwavev1alpha1.WitwaveWorkspace{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "witwave",
			Namespace: "team",
			UID:       types.UID("u-2"),
		},
		Spec: witwavev1alpha1.WitwaveWorkspaceSpec{
			ConfigFiles: []witwavev1alpha1.WitwaveWorkspaceConfigFile{
				{
					Inline: &witwavev1alpha1.WitwaveWorkspaceInlineFile{
						Name:    "repo-info",
						Path:    "workspace.yaml",
						Content: "repos:\n  - name: witwave\n",
					},
					MountPath: "/workspaces/witwave/workspace.yaml",
				},
			},
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&witwavev1alpha1.WitwaveWorkspace{}).
		WithObjects(ws).
		Build()
	r := &WitwaveWorkspaceReconciler{Client: c, Scheme: s}
	if _, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "team", Name: "witwave"},
	}); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "team", Name: "witwave"},
	}); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	cm := &corev1.ConfigMap{}
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "team", Name: "witwave-cf-repo-info"}, cm); err != nil {
		t.Fatalf("ConfigMap not rendered: %v", err)
	}
	if !metav1.IsControlledBy(cm, ws) {
		t.Fatalf("ConfigMap missing controller ownerRef")
	}
	if cm.Labels[labelComponent] != componentWitwaveWorkspaceConfigFile {
		t.Fatalf("expected component=%q, got %q", componentWitwaveWorkspaceConfigFile, cm.Labels[labelComponent])
	}
	if cm.Labels[labelWitwaveWorkspaceName] != ws.Name {
		t.Fatalf("expected workspace label %q, got %q", ws.Name, cm.Labels[labelWitwaveWorkspaceName])
	}
	if got := cm.Data["workspace.yaml"]; got == "" {
		t.Fatalf("ConfigMap data missing workspace.yaml key")
	}
}

// TestWitwaveWorkspaceBoundAgentsIndex pins the inverted index update — when a
// WitwaveAgent gains/loses Spec.WorkspaceRefs entries, the workspace
// controller mirrors that into Status.BoundAgents.
func TestWitwaveWorkspaceBoundAgentsIndex(t *testing.T) {
	s := fakeWitwaveWorkspaceScheme(t)
	ws := &witwavev1alpha1.WitwaveWorkspace{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "witwave",
			Namespace: "team",
			UID:       types.UID("u-3"),
		},
	}
	agent := &witwavev1alpha1.WitwaveAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "iris", Namespace: "team"},
		Spec: witwavev1alpha1.WitwaveAgentSpec{
			Image:    witwavev1alpha1.ImageSpec{Repository: "witwave/harness", Tag: "x"},
			Backends: []witwavev1alpha1.BackendSpec{{Name: "echo", Image: witwavev1alpha1.ImageSpec{Repository: "witwave/echo", Tag: "x"}}},
			WorkspaceRefs: []witwavev1alpha1.WitwaveAgentWorkspaceRef{
				{Name: "witwave"},
			},
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&witwavev1alpha1.WitwaveWorkspace{}).
		WithObjects(ws, agent).
		Build()
	r := &WitwaveWorkspaceReconciler{Client: c, Scheme: s}
	for i := 0; i < 2; i++ {
		if _, err := r.Reconcile(context.Background(), reconcile.Request{
			NamespacedName: types.NamespacedName{Namespace: "team", Name: "witwave"},
		}); err != nil {
			t.Fatalf("reconcile %d: %v", i, err)
		}
	}
	got := &witwavev1alpha1.WitwaveWorkspace{}
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "team", Name: "witwave"}, got); err != nil {
		t.Fatalf("get workspace: %v", err)
	}
	if len(got.Status.BoundAgents) != 1 || got.Status.BoundAgents[0].Name != "iris" {
		t.Fatalf("expected BoundAgents=[iris], got %v", got.Status.BoundAgents)
	}
}

// TestWitwaveWorkspaceRefuseDeleteWithBoundAgents pins the refuse-delete invariant:
// the finalizer is not cleared while at least one agent still references
// the workspace.
func TestWitwaveWorkspaceRefuseDeleteWithBoundAgents(t *testing.T) {
	s := fakeWitwaveWorkspaceScheme(t)
	now := metav1.Now()
	ws := &witwavev1alpha1.WitwaveWorkspace{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "witwave",
			Namespace:         "team",
			UID:               types.UID("u-4"),
			DeletionTimestamp: &now,
			Finalizers:        []string{witwaveWorkspaceFinalizer},
		},
	}
	agent := &witwavev1alpha1.WitwaveAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "iris", Namespace: "team"},
		Spec: witwavev1alpha1.WitwaveAgentSpec{
			Image:    witwavev1alpha1.ImageSpec{Repository: "witwave/harness", Tag: "x"},
			Backends: []witwavev1alpha1.BackendSpec{{Name: "echo", Image: witwavev1alpha1.ImageSpec{Repository: "witwave/echo", Tag: "x"}}},
			WorkspaceRefs: []witwavev1alpha1.WitwaveAgentWorkspaceRef{
				{Name: "witwave"},
			},
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&witwavev1alpha1.WitwaveWorkspace{}).
		WithObjects(ws, agent).
		Build()
	r := &WitwaveWorkspaceReconciler{Client: c, Scheme: s}
	if _, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "team", Name: "witwave"},
	}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got := &witwavev1alpha1.WitwaveWorkspace{}
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "team", Name: "witwave"}, got); err != nil {
		t.Fatalf("expected workspace to still exist (finalizer should block deletion): %v", err)
	}
	hasFinalizer := false
	for _, f := range got.Finalizers {
		if f == witwaveWorkspaceFinalizer {
			hasFinalizer = true
			break
		}
	}
	if !hasFinalizer {
		t.Fatalf("finalizer cleared while agent %q still references workspace", agent.Name)
	}
}

// TestWitwaveWorkspaceReclaimRetainKeepsPVC covers the per-volume ReclaimPolicy:
// Retain'd PVCs survive WitwaveWorkspace deletion (the operator strips its owner
// ref so the apiserver's GC doesn't sweep the PVC).
func TestWitwaveWorkspaceReclaimRetainKeepsPVC(t *testing.T) {
	s := fakeWitwaveWorkspaceScheme(t)
	now := metav1.Now()
	size := resource.MustParse("1Gi")
	ws := &witwavev1alpha1.WitwaveWorkspace{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "witwave",
			Namespace:         "team",
			UID:               types.UID("u-5"),
			DeletionTimestamp: &now,
			Finalizers:        []string{witwaveWorkspaceFinalizer},
		},
		Spec: witwavev1alpha1.WitwaveWorkspaceSpec{
			Volumes: []witwavev1alpha1.WitwaveWorkspaceVolume{
				{
					Name:          "memory",
					Size:          &size,
					AccessMode:    corev1.ReadWriteMany,
					ReclaimPolicy: witwavev1alpha1.WitwaveWorkspaceVolumeReclaimPolicyRetain,
				},
			},
		},
	}
	tr := true
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      WitwaveWorkspaceVolumePVCName(ws.Name, "memory"),
			Namespace: ws.Namespace,
			Labels: map[string]string{
				labelManagedBy:                  managedBy,
				labelWitwaveWorkspaceName:       ws.Name,
				labelComponent:                  componentWitwaveWorkspaceVolume,
				labelWitwaveWorkspaceVolumeName: "memory",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: witwavev1alpha1.GroupVersion.String(),
					Kind:       "WitwaveWorkspace",
					Name:       ws.Name,
					UID:        ws.UID,
					Controller: &tr,
				},
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: size},
			},
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&witwavev1alpha1.WitwaveWorkspace{}).
		WithObjects(ws, pvc).
		Build()
	r := &WitwaveWorkspaceReconciler{Client: c, Scheme: s}
	if _, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "team", Name: "witwave"},
	}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got := &corev1.PersistentVolumeClaim{}
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "team", Name: pvc.Name}, got); err != nil {
		t.Fatalf("expected retained PVC to survive: %v", err)
	}
	for _, ref := range got.OwnerReferences {
		if ref.UID == ws.UID {
			t.Fatalf("expected workspace ownerRef to be stripped; still present: %#v", ref)
		}
	}
}

// TestStampWitwaveWorkspacesOnDeploymentVolumes pins the agent-side stamping:
// Spec.WorkspaceRefs adds workspace volumes onto every backend container.
func TestStampWitwaveWorkspacesOnDeploymentVolumes(t *testing.T) {
	dep := &appsv1.Deployment{
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "harness"},
						{Name: "echo"},
					},
				},
			},
		},
	}
	size := resource.MustParse("1Gi")
	workspaces := []witwavev1alpha1.WitwaveWorkspace{{
		ObjectMeta: metav1.ObjectMeta{Name: "witwave", Namespace: "team"},
		Spec: witwavev1alpha1.WitwaveWorkspaceSpec{
			Volumes: []witwavev1alpha1.WitwaveWorkspaceVolume{
				{
					Name:        "source",
					StorageType: witwavev1alpha1.WitwaveWorkspaceStorageTypePVC,
					Size:        &size,
					AccessMode:  corev1.ReadWriteMany,
				},
			},
		},
	}}
	stampWitwaveWorkspacesOnDeployment(dep, workspaces)
	if len(dep.Spec.Template.Spec.Volumes) != 1 {
		t.Fatalf("expected 1 stamped volume, got %d", len(dep.Spec.Template.Spec.Volumes))
	}
	v := dep.Spec.Template.Spec.Volumes[0]
	if v.PersistentVolumeClaim == nil || v.PersistentVolumeClaim.ClaimName != "witwave-vol-source" {
		t.Fatalf("expected PVC ref witwave-vol-source, got %#v", v)
	}
	// Harness container untouched, backend container gets the mount.
	if len(dep.Spec.Template.Spec.Containers[0].VolumeMounts) != 0 {
		t.Fatalf("harness container should not receive workspace mounts: %#v", dep.Spec.Template.Spec.Containers[0].VolumeMounts)
	}
	if len(dep.Spec.Template.Spec.Containers[1].VolumeMounts) != 1 {
		t.Fatalf("expected backend mount stamped, got %d", len(dep.Spec.Template.Spec.Containers[1].VolumeMounts))
	}
	if got := dep.Spec.Template.Spec.Containers[1].VolumeMounts[0].MountPath; got != "/workspaces/witwave/source" {
		t.Fatalf("default mount path: got %q", got)
	}
}

func TestStampWitwaveWorkspacesEnvFromSecret(t *testing.T) {
	dep := &appsv1.Deployment{
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "harness"},
						{Name: "echo"},
					},
				},
			},
		},
	}
	workspaces := []witwavev1alpha1.WitwaveWorkspace{{
		ObjectMeta: metav1.ObjectMeta{Name: "witwave", Namespace: "team"},
		Spec: witwavev1alpha1.WitwaveWorkspaceSpec{
			Secrets: []witwavev1alpha1.WitwaveWorkspaceSecret{
				{Name: "team-tokens", EnvFrom: true},
			},
		},
	}}
	stampWitwaveWorkspacesOnDeployment(dep, workspaces)
	if got := len(dep.Spec.Template.Spec.Containers[1].EnvFrom); got != 1 {
		t.Fatalf("expected 1 envFrom on backend, got %d", got)
	}
	ef := dep.Spec.Template.Spec.Containers[1].EnvFrom[0]
	if ef.SecretRef == nil || ef.SecretRef.Name != "team-tokens" {
		t.Fatalf("expected SecretRef team-tokens, got %#v", ef)
	}
	if got := len(dep.Spec.Template.Spec.Containers[0].EnvFrom); got != 0 {
		t.Fatalf("harness should not receive workspace envFrom: %d", got)
	}
}

// TestFetchWitwaveWorkspacesForAgentSilentlySkipsMissing covers the design's
// "missing workspaces are silently skipped" semantic: a not-yet-created
// WitwaveWorkspace reference doesn't error the reconcile.
func TestFetchWitwaveWorkspacesForAgentSilentlySkipsMissing(t *testing.T) {
	s := fakeWitwaveWorkspaceScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).Build()
	agent := &witwavev1alpha1.WitwaveAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "iris", Namespace: "team"},
		Spec: witwavev1alpha1.WitwaveAgentSpec{
			WorkspaceRefs: []witwavev1alpha1.WitwaveAgentWorkspaceRef{
				{Name: "ghost"},
			},
		},
	}
	got, err := fetchWitwaveWorkspacesForAgent(context.Background(), c, agent)
	if err != nil {
		t.Fatalf("missing workspace must not error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty result, got %d", len(got))
	}
}

// _ catches accidental unused imports that the compiler would otherwise
// flag noisily during incremental test additions.
var _ = client.IgnoreNotFound
