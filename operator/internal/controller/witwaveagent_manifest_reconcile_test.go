/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	witwavev1alpha1 "github.com/witwave-ai/witwave-operator/api/v1alpha1"
)

// Reconcile-path coverage for the team-manifest ConfigMap (#972):
// the pure builder tests in witwaveagent_manifest_ownership_test.go prove
// the desired-shape logic; these envtest specs prove the reconcile loop
// converges on that shape in practice — including the upgrade path from
// legacy single-owner CMs and the last-member GC that must not run too
// early.
//
// Scope: 4-6 representative cases is sufficient per #972. Scenarios
// covered:
//  1. legacy single-owner CM converges to multi-owner shape on the
//     next reconcile (upgrade path, #684).
//  2. deleting one of three team members keeps the CM in place with
//     two remaining non-controller OwnerReferences.
//  3. the manifestHash annotation short-circuits writes when membership
//     is unchanged across repeated reconciles (churn guard, #474).
//  4. an agent with the empty-string team key still produces a
//     manifest CM so agents that forgot the team label aren't silently
//     dropped from their peer's manifests.
var _ = Describe("manifest ConfigMap reconcile (#972)", func() {
	var (
		teamName string
		r        *WitwaveAgentReconciler
	)

	BeforeEach(func() {
		teamName = fmt.Sprintf("team-%s", rand5())
		r = newReconciler()
	})

	makeTeamMember := func(name string) *witwavev1alpha1.WitwaveAgent {
		a := newTestAgent(name)
		a.Labels = map[string]string{teamLabel: teamName}
		return a
	}

	teardown := func(keys []types.NamespacedName) {
		for _, key := range keys {
			agent := &witwavev1alpha1.WitwaveAgent{}
			if err := k8sClient.Get(ctx, key, agent); err == nil {
				controllerutil.RemoveFinalizer(agent, witwaveAgentFinalizer)
				_ = k8sClient.Update(ctx, agent)
				_ = k8sClient.Delete(ctx, agent)
			}
		}
	}

	It("converges a legacy single-owner CM to multi-owner shape on upgrade", func() {
		a1 := makeTeamMember("legacy-a-" + rand5())
		a2 := makeTeamMember("legacy-b-" + rand5())
		Expect(k8sClient.Create(ctx, a1)).To(Succeed())
		Expect(k8sClient.Create(ctx, a2)).To(Succeed())

		// Reconcile once to let both agents land their finalizer and the
		// manifest CM materialise in the multi-owner shape.
		k1 := types.NamespacedName{Name: a1.Name, Namespace: a1.Namespace}
		k2 := types.NamespacedName{Name: a2.Name, Namespace: a2.Namespace}
		defer teardown([]types.NamespacedName{k1, k2})
		Expect(reconcileUntilStable(r, k1, 5)).To(Succeed())
		Expect(reconcileUntilStable(r, k2, 5)).To(Succeed())

		// Re-fetch a1 so the server-assigned UID is available, then
		// rewrite the manifest CM's OwnerReferences to the legacy
		// single-controller shape to simulate an upgrade.
		Expect(k8sClient.Get(ctx, k1, a1)).To(Succeed())
		cmName := manifestConfigMapName(a1)
		cmKey := types.NamespacedName{Name: cmName, Namespace: a1.Namespace}
		cm := &corev1.ConfigMap{}
		Expect(k8sClient.Get(ctx, cmKey, cm)).To(Succeed())
		controllerRef := metav1.OwnerReference{
			APIVersion: witwavev1alpha1.GroupVersion.String(),
			Kind:       "WitwaveAgent",
			Name:       a1.Name,
			UID:        a1.UID,
			Controller: func() *bool { b := true; return &b }(),
		}
		cm.OwnerReferences = []metav1.OwnerReference{controllerRef}
		Expect(k8sClient.Update(ctx, cm)).To(Succeed())

		// Next reconcile must converge back to the multi-owner (2 refs)
		// shape with both entries marked non-controller.
		Expect(reconcileUntilStable(r, k1, 5)).To(Succeed())
		Expect(k8sClient.Get(ctx, cmKey, cm)).To(Succeed())
		Expect(cm.OwnerReferences).To(HaveLen(2),
			"upgrade path must converge the CM to one non-controller OwnerReference per member")
		for i, ref := range cm.OwnerReferences {
			Expect(ref.Controller).ToNot(BeNil(),
				"ownerRef[%d]: Controller pointer must be set (non-controller=false)", i)
			Expect(*ref.Controller).To(BeFalse(),
				"ownerRef[%d] %s: Controller must be false so CM survives intermediate deletions",
				i, ref.Name)
		}
	})

	It("keeps the manifest CM alive after deleting 1 of 3 members (last-member GC check)", func() {
		a1 := makeTeamMember("gc-a-" + rand5())
		a2 := makeTeamMember("gc-b-" + rand5())
		a3 := makeTeamMember("gc-c-" + rand5())
		Expect(k8sClient.Create(ctx, a1)).To(Succeed())
		Expect(k8sClient.Create(ctx, a2)).To(Succeed())
		Expect(k8sClient.Create(ctx, a3)).To(Succeed())

		k1 := types.NamespacedName{Name: a1.Name, Namespace: a1.Namespace}
		k2 := types.NamespacedName{Name: a2.Name, Namespace: a2.Namespace}
		k3 := types.NamespacedName{Name: a3.Name, Namespace: a3.Namespace}
		defer teardown([]types.NamespacedName{k1, k2, k3})
		for _, k := range []types.NamespacedName{k1, k2, k3} {
			Expect(reconcileUntilStable(r, k, 5)).To(Succeed())
		}

		// Confirm the CM carries 3 OwnerReferences.
		Expect(k8sClient.Get(ctx, k1, a1)).To(Succeed())
		cmName := manifestConfigMapName(a1)
		cmKey := types.NamespacedName{Name: cmName, Namespace: a1.Namespace}
		cm := &corev1.ConfigMap{}
		Expect(k8sClient.Get(ctx, cmKey, cm)).To(Succeed())
		Expect(cm.OwnerReferences).To(HaveLen(3))

		// Delete one member and run through the finalizer.
		Expect(k8sClient.Get(ctx, k3, a3)).To(Succeed())
		Expect(k8sClient.Delete(ctx, a3)).To(Succeed())
		Expect(reconcileUntilStable(r, k3, 5)).To(Succeed())
		// Reconcile a surviving teammate to rebuild the manifest CM.
		Expect(reconcileUntilStable(r, k1, 5)).To(Succeed())

		// CM must still exist with 2 OwnerReferences — last-member GC
		// MUST NOT fire when 2 members remain.
		Expect(k8sClient.Get(ctx, cmKey, cm)).To(Succeed())
		Expect(cm.OwnerReferences).To(HaveLen(2),
			"manifest CM must retain one non-controller OwnerReference per surviving member")
	})

	It("short-circuits the CM write when membership is unchanged (hash guard)", func() {
		a1 := makeTeamMember("hash-" + rand5())
		Expect(k8sClient.Create(ctx, a1)).To(Succeed())
		k1 := types.NamespacedName{Name: a1.Name, Namespace: a1.Namespace}
		defer teardown([]types.NamespacedName{k1})
		Expect(reconcileUntilStable(r, k1, 5)).To(Succeed())

		Expect(k8sClient.Get(ctx, k1, a1)).To(Succeed())
		cmKey := types.NamespacedName{Name: manifestConfigMapName(a1), Namespace: a1.Namespace}
		cm := &corev1.ConfigMap{}
		Expect(k8sClient.Get(ctx, cmKey, cm)).To(Succeed())
		rvBefore := cm.ResourceVersion

		// Five idempotent reconciles — the ResourceVersion must not advance
		// because membership (and therefore the manifest hash) is unchanged.
		for i := 0; i < 5; i++ {
			Expect(reconcileUntilStable(r, k1, 5)).To(Succeed())
		}
		Expect(k8sClient.Get(ctx, cmKey, cm)).To(Succeed())
		Expect(cm.ResourceVersion).To(Equal(rvBefore),
			"manifest CM ResourceVersion advanced across idempotent reconciles — hash short-circuit regressed")
	})

	It("materialises a manifest CM even when the agent omits the team label", func() {
		// Empty-team-key grouping — agents without the team label share
		// the empty-string manifest. Ensures an agent with no label still
		// gets its CM.
		a1 := newTestAgent("noteam-" + rand5())
		Expect(k8sClient.Create(ctx, a1)).To(Succeed())
		k1 := types.NamespacedName{Name: a1.Name, Namespace: a1.Namespace}
		defer teardown([]types.NamespacedName{k1})
		Expect(reconcileUntilStable(r, k1, 5)).To(Succeed())

		Expect(k8sClient.Get(ctx, k1, a1)).To(Succeed())
		cmKey := types.NamespacedName{Name: manifestConfigMapName(a1), Namespace: a1.Namespace}
		cm := &corev1.ConfigMap{}
		err := k8sClient.Get(ctx, cmKey, cm)
		Expect(apierrors.IsNotFound(err)).To(BeFalse(),
			"agent without team label must still get a manifest CM; got err=%v", err)
	})
})
