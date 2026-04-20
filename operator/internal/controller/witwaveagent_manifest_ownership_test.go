/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package controller

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	witwavev1alpha1 "github.com/witwave-ai/witwave-operator/api/v1alpha1"
)

// TestBuildManifestConfigMapMultiOwner verifies the fix for #684: the
// per-team manifest CM must carry one non-controller OwnerReference per
// live team member, so Kubernetes GC only removes the CM when the last
// team member is deleted. The legacy single-controller shape caused a
// cascade delete of the shared CM whenever the owning agent was
// removed, breaking mounts on surviving pods.
func TestBuildManifestConfigMapMultiOwner(t *testing.T) {
	newAgent := func(name string, uid string) *witwavev1alpha1.WitwaveAgent {
		return &witwavev1alpha1.WitwaveAgent{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: "witwave",
				UID:       types.UID(uid),
				Labels:    map[string]string{teamLabel: "alpha"},
			},
			Spec: witwavev1alpha1.WitwaveAgentSpec{Port: 8000},
		}
	}

	iris := newAgent("iris", "uid-iris")
	nova := newAgent("nova", "uid-nova")
	kira := newAgent("kira", "uid-kira")

	members := []manifestMember{
		{Name: "iris", Port: 8000},
		{Name: "kira", Port: 8000},
		{Name: "nova", Port: 8000},
	}

	cm, hash := buildManifestConfigMap(iris, []*witwavev1alpha1.WitwaveAgent{iris, nova, kira}, members)

	if len(cm.OwnerReferences) != 3 {
		t.Fatalf("expected 3 owner refs, got %d", len(cm.OwnerReferences))
	}
	for i, ref := range cm.OwnerReferences {
		if ref.Controller == nil || *ref.Controller {
			t.Errorf("ownerRef[%d] %s: Controller must be false (non-controller) to keep CM alive through partial team deletions",
				i, ref.Name)
		}
		if ref.BlockOwnerDeletion == nil || *ref.BlockOwnerDeletion {
			t.Errorf("ownerRef[%d] %s: BlockOwnerDeletion must be false — we do not want to block agent deletion on CM cleanup",
				i, ref.Name)
		}
		if ref.Kind != "WitwaveAgent" {
			t.Errorf("ownerRef[%d]: Kind=%q, want WitwaveAgent", i, ref.Kind)
		}
	}

	// Refs should be UID-sorted for stable hashing.
	for i := 1; i < len(cm.OwnerReferences); i++ {
		if cm.OwnerReferences[i-1].UID >= cm.OwnerReferences[i].UID {
			t.Errorf("owner refs not UID-sorted: [%d]=%s then [%d]=%s",
				i-1, cm.OwnerReferences[i-1].UID, i, cm.OwnerReferences[i].UID)
		}
	}

	if hash == "" {
		t.Fatal("manifest hash must be populated")
	}
	if cm.Annotations[manifestHashAnnotation] != hash {
		t.Fatalf("CM annotation hash %q != returned hash %q",
			cm.Annotations[manifestHashAnnotation], hash)
	}
}

// TestBuildManifestConfigMapHashSensitiveToMembership exercises the
// hash's sensitivity to both rendered content AND owner-UID changes.
// The second case would be invisible to a hash-of-body-only design
// (names + ports are identical between the two teams; only UIDs
// differ), so the fix adds owner UIDs to the hash input to catch
// membership flips that coincidentally produce the same manifest body.
func TestBuildManifestConfigMapHashSensitiveToMembership(t *testing.T) {
	mk := func(name, uid string) *witwavev1alpha1.WitwaveAgent {
		return &witwavev1alpha1.WitwaveAgent{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: "witwave",
				UID:       types.UID(uid),
				Labels:    map[string]string{teamLabel: "alpha"},
			},
			Spec: witwavev1alpha1.WitwaveAgentSpec{Port: 8000},
		}
	}
	members := []manifestMember{{Name: "iris", Port: 8000}}

	_, hashA := buildManifestConfigMap(mk("iris", "uid-a"), []*witwavev1alpha1.WitwaveAgent{mk("iris", "uid-a")}, members)
	_, hashB := buildManifestConfigMap(mk("iris", "uid-b"), []*witwavev1alpha1.WitwaveAgent{mk("iris", "uid-b")}, members)
	if hashA == hashB {
		t.Fatal("hash must differ when the SAME named member has a different UID (CR re-create) so ownerRefs are refreshed")
	}

	// Adding another member must also change the hash.
	twoMembers := []manifestMember{
		{Name: "iris", Port: 8000},
		{Name: "nova", Port: 8000},
	}
	_, hashTwo := buildManifestConfigMap(
		mk("iris", "uid-a"),
		[]*witwavev1alpha1.WitwaveAgent{mk("iris", "uid-a"), mk("nova", "uid-n")},
		twoMembers,
	)
	if hashA == hashTwo {
		t.Fatal("hash must differ when membership size changes")
	}
}

// TestBuildManifestOwnerRefsSkipsMissingUID guards the "agent not yet
// persisted" case: an apiserver-assigned UID takes a round-trip, and
// the first reconcile might see a member without one. We must skip
// the ownerRef for that iteration (rather than panic or emit an
// invalid ref) and pick it up on the next reconcile.
func TestBuildManifestOwnerRefsSkipsMissingUID(t *testing.T) {
	agents := []*witwavev1alpha1.WitwaveAgent{
		{ObjectMeta: metav1.ObjectMeta{Name: "iris", UID: "uid-iris"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "nova"}}, // no UID yet
		nil,
	}
	refs := buildManifestOwnerRefs(agents)
	if len(refs) != 1 {
		t.Fatalf("want 1 ref (skipping nil + missing-UID), got %d", len(refs))
	}
	if refs[0].Name != "iris" || refs[0].UID != "uid-iris" {
		t.Fatalf("unexpected surviving ref: %+v", refs[0])
	}
}

// TestManifestVolumeIsOptional guards the defensive mount: if the
// shared CM is briefly missing (e.g. mid-upgrade from the old
// single-controller shape, or the moment of deletion for a
// single-member team), pods should still launch. The harness
// tolerates a missing manifest as "empty team".
func TestManifestVolumeIsOptional(t *testing.T) {
	vol, _ := manifestVolumeAndMount("witwave-manifest-alpha")
	if vol.ConfigMap == nil {
		t.Fatal("expected ConfigMap volume source")
	}
	if vol.ConfigMap.Optional == nil || !*vol.ConfigMap.Optional {
		t.Fatal("manifest volume must be Optional=true so transient CM absence cannot fail pod start")
	}
}
