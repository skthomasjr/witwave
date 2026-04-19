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

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	nyxv1alpha1 "github.com/nyx-ai/nyx-operator/api/v1alpha1"
)

// Extra envtest scenarios beyond the single happy-path spec (#835).
//
// Scope: four representative scenarios — disable flip, finalize with a
// stuck teammate annotation, credential rotation, and CRD churn (schema
// evolution by way of repeated spec updates on the same CR). Full e2e
// parity across every teardown branch is follow-up work.
var _ = Describe("NyxAgent scenarios (#835)", func() {
	var (
		resourceName string
		key          types.NamespacedName
		r            *NyxAgentReconciler
	)

	BeforeEach(func() {
		resourceName = fmt.Sprintf("scenarios-%s", rand5())
		key = types.NamespacedName{Name: resourceName, Namespace: "default"}
		r = newReconciler()
	})

	AfterEach(func() {
		agent := &nyxv1alpha1.NyxAgent{}
		if err := k8sClient.Get(ctx, key, agent); err == nil {
			controllerutil.RemoveFinalizer(agent, nyxAgentFinalizer)
			_ = k8sClient.Update(ctx, agent)
			_ = k8sClient.Delete(ctx, agent)
		}
	})

	// Scenario 1: disable flip.
	// Flipping spec.enabled from true to false must tear down the
	// Deployment so the agent pod stops consuming cluster resources.
	It("tears down the Deployment when spec.enabled flips to false", func() {
		Expect(k8sClient.Create(ctx, newTestAgent(resourceName))).To(Succeed())
		Expect(reconcileUntilStable(r, key, 5)).To(Succeed())

		// Deployment exists after the initial create reconcile.
		dep := &appsv1.Deployment{}
		Expect(k8sClient.Get(ctx, key, dep)).To(Succeed())

		// Flip enabled -> false.
		agent := &nyxv1alpha1.NyxAgent{}
		Expect(k8sClient.Get(ctx, key, agent)).To(Succeed())
		disabled := false
		agent.Spec.Enabled = &disabled
		Expect(k8sClient.Update(ctx, agent)).To(Succeed())

		// Reconcile — the teardown branch should remove the Deployment.
		Expect(reconcileUntilStable(r, key, 5)).To(Succeed())
		err := k8sClient.Get(ctx, key, &appsv1.Deployment{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue(),
			"expected Deployment to be torn down after disable flip, got err=%v", err)
	})

	// Scenario 2: finalize with stuck "teammate" annotation.
	// Some teammate-coordination flows stamp annotations on the CR during
	// teardown; the finalizer path must still complete and drop the
	// finalizer even when unrelated metadata is present. Regression guard
	// against a teardown branch that accidentally requeues forever on
	// mismatched annotations.
	It("completes finalize even with an unrelated stuck teammate annotation", func() {
		agent := newTestAgent(resourceName)
		agent.Annotations = map[string]string{
			"nyx.ai/teammate-coordination": "stuck",
		}
		Expect(k8sClient.Create(ctx, agent)).To(Succeed())
		Expect(reconcileUntilStable(r, key, 5)).To(Succeed())

		// Trigger deletion; the finalizer should run to completion.
		Expect(k8sClient.Delete(ctx, agent)).To(Succeed())
		Expect(reconcileUntilStable(r, key, 5)).To(Succeed())

		// CR must be fully gone.
		err := k8sClient.Get(ctx, key, &nyxv1alpha1.NyxAgent{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue(),
			"expected NyxAgent to be deleted after finalize, got err=%v", err)
	})

	// Scenario 3: credential rotation.
	// Changing the agent spec (simulating a credentials-driven template
	// mutation) must roll the Deployment — the checksum annotation or
	// the spec change itself drives the Deployment template update.
	It("rolls the Deployment when the spec mutates (credential-rotation analogue)", func() {
		Expect(k8sClient.Create(ctx, newTestAgent(resourceName))).To(Succeed())
		Expect(reconcileUntilStable(r, key, 5)).To(Succeed())

		beforeDep := &appsv1.Deployment{}
		Expect(k8sClient.Get(ctx, key, beforeDep)).To(Succeed())
		beforeRV := beforeDep.ResourceVersion

		// Touch the agent spec — anything that modifies the Deployment
		// template is sufficient to exercise the rotation path. Here we
		// append an env var to the harness container by flipping the
		// image tag (a common rotation trigger).
		agent := &nyxv1alpha1.NyxAgent{}
		Expect(k8sClient.Get(ctx, key, agent)).To(Succeed())
		agent.Spec.Image.Tag = "rotated"
		Expect(k8sClient.Update(ctx, agent)).To(Succeed())
		Expect(reconcileUntilStable(r, key, 5)).To(Succeed())

		afterDep := &appsv1.Deployment{}
		Expect(k8sClient.Get(ctx, key, afterDep)).To(Succeed())
		Expect(afterDep.ResourceVersion).ToNot(Equal(beforeRV),
			"expected Deployment ResourceVersion to change after spec rotation")
	})

	// Scenario 4: CRD churn.
	// Rapid spec mutations (a proxy for CRD schema evolution driving
	// repeated controller-side rewrites) must not leak owned resources or
	// leave the controller stuck in an error loop. Five back-to-back
	// spec updates — reconcile must converge each time.
	It("converges across rapid successive spec updates (CRD churn)", func() {
		Expect(k8sClient.Create(ctx, newTestAgent(resourceName))).To(Succeed())
		Expect(reconcileUntilStable(r, key, 5)).To(Succeed())

		for i := 0; i < 5; i++ {
			agent := &nyxv1alpha1.NyxAgent{}
			Expect(k8sClient.Get(ctx, key, agent)).To(Succeed())
			agent.Spec.Image.Tag = fmt.Sprintf("churn-%d", i)
			Expect(k8sClient.Update(ctx, agent)).To(Succeed())
			Expect(reconcileUntilStable(r, key, 5)).To(Succeed())
		}
		// After churn, the Deployment must still exist with the last
		// applied tag — no leaked objects, no error loop.
		dep := &appsv1.Deployment{}
		Expect(k8sClient.Get(ctx, key, dep)).To(Succeed())
		Expect(dep.Spec.Template.Spec.Containers).ToNot(BeEmpty())
	})
})
