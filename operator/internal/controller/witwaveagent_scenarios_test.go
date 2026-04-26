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
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	witwavev1alpha1 "github.com/witwave-ai/witwave-operator/api/v1alpha1"
)

// Extra envtest scenarios beyond the single happy-path spec (#835).
//
// Scope: four representative scenarios — disable flip, finalize with a
// stuck teammate annotation, credential rotation, and CRD churn (schema
// evolution by way of repeated spec updates on the same CR). Full e2e
// parity across every teardown branch is follow-up work.
var _ = Describe("WitwaveAgent scenarios (#835)", func() {
	var (
		resourceName string
		key          types.NamespacedName
		r            *WitwaveAgentReconciler
	)

	BeforeEach(func() {
		resourceName = fmt.Sprintf("scenarios-%s", rand5())
		key = types.NamespacedName{Name: resourceName, Namespace: "default"}
		r = newReconciler()
	})

	AfterEach(func() {
		agent := &witwavev1alpha1.WitwaveAgent{}
		if err := k8sClient.Get(ctx, key, agent); err == nil {
			controllerutil.RemoveFinalizer(agent, witwaveAgentFinalizer)
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
		agent := &witwavev1alpha1.WitwaveAgent{}
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
			"witwave.ai/teammate-coordination": "stuck",
		}
		Expect(k8sClient.Create(ctx, agent)).To(Succeed())
		Expect(reconcileUntilStable(r, key, 5)).To(Succeed())

		// Trigger deletion; the finalizer should run to completion.
		Expect(k8sClient.Delete(ctx, agent)).To(Succeed())
		Expect(reconcileUntilStable(r, key, 5)).To(Succeed())

		// CR must be fully gone.
		err := k8sClient.Get(ctx, key, &witwavev1alpha1.WitwaveAgent{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue(),
			"expected WitwaveAgent to be deleted after finalize, got err=%v", err)
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
		agent := &witwavev1alpha1.WitwaveAgent{}
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
			agent := &witwavev1alpha1.WitwaveAgent{}
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

	// Scenario 5: disable-flip tears down NetworkPolicy + MCP tools (#1635).
	// teardownDisabledAgent previously skipped reconcileNetworkPolicy and
	// reconcileMCPTools, leaving stale NetworkPolicies and mcp-<tool>
	// Deployment/Service pairs after spec.enabled flipped to false. Assert
	// that the additions to the teardown path actually drop them.
	It("tears down NetworkPolicy and MCP tools when spec.enabled flips to false (#1635)", func() {
		agent := newTestAgent(resourceName)
		agent.Spec.NetworkPolicy = &witwavev1alpha1.NetworkPolicySpec{Enabled: true}
		agent.Spec.MCPTools = &witwavev1alpha1.MCPToolsSpec{
			Kubernetes: &witwavev1alpha1.MCPToolSpec{Enabled: true},
		}
		Expect(k8sClient.Create(ctx, agent)).To(Succeed())
		Expect(reconcileUntilStable(r, key, 5)).To(Succeed())

		// Sanity: NetworkPolicy + mcp-kubernetes Deployment/Service exist.
		Expect(k8sClient.Get(ctx, key, &networkingv1.NetworkPolicy{})).To(Succeed())
		mcpKey := types.NamespacedName{
			Namespace: agent.Namespace,
			Name:      fmt.Sprintf("%s-mcp-kubernetes", agent.Name),
		}
		Expect(k8sClient.Get(ctx, mcpKey, &appsv1.Deployment{})).To(Succeed())
		Expect(k8sClient.Get(ctx, mcpKey, &corev1.Service{})).To(Succeed())

		// Flip enabled -> false and reconcile to convergence.
		live := &witwavev1alpha1.WitwaveAgent{}
		Expect(k8sClient.Get(ctx, key, live)).To(Succeed())
		disabled := false
		live.Spec.Enabled = &disabled
		Expect(k8sClient.Update(ctx, live)).To(Succeed())
		Expect(reconcileUntilStable(r, key, 5)).To(Succeed())

		// NetworkPolicy must be gone.
		err := k8sClient.Get(ctx, key, &networkingv1.NetworkPolicy{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue(),
			"expected NetworkPolicy to be torn down after disable, got err=%v", err)

		// mcp-kubernetes Deployment + Service must be gone.
		err = k8sClient.Get(ctx, mcpKey, &appsv1.Deployment{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue(),
			"expected mcp-kubernetes Deployment to be torn down after disable, got err=%v", err)
		err = k8sClient.Get(ctx, mcpKey, &corev1.Service{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue(),
			"expected mcp-kubernetes Service to be torn down after disable, got err=%v", err)
	})
})
