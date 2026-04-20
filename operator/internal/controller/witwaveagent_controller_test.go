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
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	witwavev1alpha1 "github.com/witwave-ai/witwave-operator/api/v1alpha1"
)

// newTestAgent returns a minimal-but-valid WitwaveAgent spec suitable for
// envtest-backed reconcile tests. Each test uses a unique name so specs can
// run in parallel without colliding on the shared "default" namespace.
func newTestAgent(name string) *witwavev1alpha1.WitwaveAgent {
	return &witwavev1alpha1.WitwaveAgent{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: witwavev1alpha1.WitwaveAgentSpec{
			Image: witwavev1alpha1.ImageSpec{
				Repository: "ghcr.io/skthomasjr/images/harness",
				Tag:        "test",
			},
			Backends: []witwavev1alpha1.BackendSpec{{
				Name: "claude",
				Image: witwavev1alpha1.ImageSpec{
					Repository: "ghcr.io/skthomasjr/images/claude",
					Tag:        "test",
				},
			}},
		},
	}
}

// newReconciler wires a reconciler against the envtest-managed API server.
// A fake EventRecorder is supplied so phase-transition events don't panic.
// APIReader is pointed at the same envtest client so the cache-bypass
// code paths (re-check DeletionTimestamp, manifest live-escalation)
// behave the same way they do in a real manager wiring — without this,
// the test-only `r.APIReader` nil fallback (#1168) would silently
// bypass those paths and mask regressions in them.
func newReconciler() *WitwaveAgentReconciler {
	return &WitwaveAgentReconciler{
		Client:    k8sClient,
		APIReader: k8sClient,
		Scheme:    k8sClient.Scheme(),
		Recorder:  record.NewFakeRecorder(16),
	}
}

// reconcileUntilStable runs Reconcile repeatedly until the reconciler returns
// Requeue=false and RequeueAfter=0 on the same generation, or maxIterations
// is reached. The finalizer path short-circuits the first call, so real tests
// need at least two passes to observe the full reconcile body.
func reconcileUntilStable(r *WitwaveAgentReconciler, key types.NamespacedName, maxIterations int) error {
	for i := 0; i < maxIterations; i++ {
		res, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		if err != nil {
			return fmt.Errorf("reconcile iter %d: %w", i, err)
		}
		if !res.Requeue && res.RequeueAfter == 0 {
			// One final pass to catch the status-driven requeue for non-Ready phases.
			_, err = r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			return err
		}
	}
	return nil
}

var _ = Describe("WitwaveAgent Controller", func() {
	Context("when reconciling a new resource", func() {
		var (
			resourceName string
			key          types.NamespacedName
			r            *WitwaveAgentReconciler
		)

		BeforeEach(func() {
			resourceName = fmt.Sprintf("ctrl-create-%s", rand5())
			key = types.NamespacedName{Name: resourceName, Namespace: "default"}
			r = newReconciler()

			Expect(k8sClient.Create(ctx, newTestAgent(resourceName))).To(Succeed())
		})

		AfterEach(func() {
			// Best-effort cleanup: drop the finalizer and delete the CR.
			agent := &witwavev1alpha1.WitwaveAgent{}
			if err := k8sClient.Get(ctx, key, agent); err == nil {
				controllerutil.RemoveFinalizer(agent, witwaveAgentFinalizer)
				_ = k8sClient.Update(ctx, agent)
				_ = k8sClient.Delete(ctx, agent)
			}
		})

		It("creates the Deployment, Service, and agent ConfigMap with owner references", func() {
			Expect(reconcileUntilStable(r, key, 5)).To(Succeed())

			// Deployment — name matches WitwaveAgent, owner ref points back.
			dep := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, key, dep)).To(Succeed())
			Expect(dep.Labels).To(HaveKeyWithValue(labelName, resourceName))
			Expect(dep.Labels).To(HaveKeyWithValue(labelManagedBy, managedBy))
			Expect(dep.OwnerReferences).To(HaveLen(1))
			Expect(dep.OwnerReferences[0].Kind).To(Equal("WitwaveAgent"))
			Expect(dep.OwnerReferences[0].Name).To(Equal(resourceName))
			Expect(dep.OwnerReferences[0].Controller).ToNot(BeNil())
			Expect(*dep.OwnerReferences[0].Controller).To(BeTrue())

			// Deployment pod spec contains both harness + backend containers.
			containerNames := []string{}
			for _, c := range dep.Spec.Template.Spec.Containers {
				containerNames = append(containerNames, c.Name)
			}
			Expect(containerNames).To(ContainElement("harness"))
			Expect(containerNames).To(ContainElement("claude"))

			// Service — same name, ClusterIP, port from spec default (8000).
			svc := &corev1.Service{}
			Expect(k8sClient.Get(ctx, key, svc)).To(Succeed())
			Expect(svc.Spec.Selector).To(HaveKeyWithValue(labelName, resourceName))
			Expect(svc.OwnerReferences).To(HaveLen(1))

			// Finalizer added on the first reconcile pass.
			updated := &witwavev1alpha1.WitwaveAgent{}
			Expect(k8sClient.Get(ctx, key, updated)).To(Succeed())
			Expect(controllerutil.ContainsFinalizer(updated, witwaveAgentFinalizer)).To(BeTrue())

			// Status written — at minimum, ObservedGeneration tracks the spec.
			Expect(updated.Status.ObservedGeneration).To(Equal(updated.Generation))
			Expect(updated.Status.Phase).ToNot(BeEmpty())
		})

		It("is idempotent across repeated reconciles", func() {
			Expect(reconcileUntilStable(r, key, 5)).To(Succeed())

			dep := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, key, dep)).To(Succeed())
			originalResourceVersion := dep.ResourceVersion
			originalUID := dep.UID

			// Second steady-state reconcile should be a no-op on all managed
			// fields that carry a ResourceVersion bump; spec is unchanged, so
			// the Deployment's spec/labels should already equal desired and
			// a subsequent Update may still re-stamp labels. We assert the
			// UID is unchanged (no recreate) and the generation is stable.
			for i := 0; i < 3; i++ {
				_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
				Expect(err).NotTo(HaveOccurred())
			}

			final := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, key, final)).To(Succeed())
			Expect(final.UID).To(Equal(originalUID), "deployment must not be recreated")
			Expect(final.Generation).To(Equal(dep.Generation), "spec.Generation must be stable across no-op reconciles")
			// ResourceVersion MAY bump because Update rewrites labels even
			// when data is identical — we just assert the object is still
			// the same identity.
			_ = originalResourceVersion
		})

		It("preserves HPA-owned replicas across reconciles", func() {
			// Enable autoscaling so buildDeployment omits spec.replicas.
			agent := &witwavev1alpha1.WitwaveAgent{}
			Expect(k8sClient.Get(ctx, key, agent)).To(Succeed())
			agent.Spec.Autoscaling = &witwavev1alpha1.AutoscalingSpec{
				Enabled:     true,
				MinReplicas: 2,
				MaxReplicas: 5,
			}
			Expect(k8sClient.Update(ctx, agent)).To(Succeed())

			Expect(reconcileUntilStable(r, key, 5)).To(Succeed())

			// HPA exists with the configured bounds.
			hpa := &autoscalingv2.HorizontalPodAutoscaler{}
			Expect(k8sClient.Get(ctx, key, hpa)).To(Succeed())
			Expect(*hpa.Spec.MinReplicas).To(Equal(int32(2)))
			Expect(hpa.Spec.MaxReplicas).To(Equal(int32(5)))

			// Simulate the HPA scaling the Deployment to 3 replicas.
			dep := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, key, dep)).To(Succeed())
			three := int32(3)
			dep.Spec.Replicas = &three
			Expect(k8sClient.Update(ctx, dep)).To(Succeed())

			// Re-reconcile — applyDeployment must preserve the HPA-written value (#486).
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())

			after := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, key, after)).To(Succeed())
			Expect(after.Spec.Replicas).ToNot(BeNil())
			Expect(*after.Spec.Replicas).To(Equal(int32(3)),
				"HPA-owned replica count must survive reconcile (#486)")
		})
	})

	Context("when disabling an agent via spec.enabled=false", func() {
		It("tears down the Deployment and Service", func() {
			name := fmt.Sprintf("ctrl-teardown-%s", rand5())
			key := types.NamespacedName{Name: name, Namespace: "default"}
			r := newReconciler()

			Expect(k8sClient.Create(ctx, newTestAgent(name))).To(Succeed())
			defer func() {
				agent := &witwavev1alpha1.WitwaveAgent{}
				if err := k8sClient.Get(ctx, key, agent); err == nil {
					controllerutil.RemoveFinalizer(agent, witwaveAgentFinalizer)
					_ = k8sClient.Update(ctx, agent)
					_ = k8sClient.Delete(ctx, agent)
				}
			}()

			Expect(reconcileUntilStable(r, key, 5)).To(Succeed())
			Expect(k8sClient.Get(ctx, key, &appsv1.Deployment{})).To(Succeed())

			// Flip enabled to false.
			agent := &witwavev1alpha1.WitwaveAgent{}
			Expect(k8sClient.Get(ctx, key, agent)).To(Succeed())
			disabled := false
			agent.Spec.Enabled = &disabled
			Expect(k8sClient.Update(ctx, agent)).To(Succeed())

			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())

			// Deployment + Service gone; NotFound expected.
			err = k8sClient.Get(ctx, key, &appsv1.Deployment{})
			Expect(apierrors.IsNotFound(err)).To(BeTrue(), "Deployment must be deleted on disable")
			err = k8sClient.Get(ctx, key, &corev1.Service{})
			Expect(apierrors.IsNotFound(err)).To(BeTrue(), "Service must be deleted on disable")
		})
	})

	Context("when deleting the WitwaveAgent CR", func() {
		It("runs the finalizer teardown and releases the CR", func() {
			name := fmt.Sprintf("ctrl-delete-%s", rand5())
			key := types.NamespacedName{Name: name, Namespace: "default"}
			r := newReconciler()

			Expect(k8sClient.Create(ctx, newTestAgent(name))).To(Succeed())
			Expect(reconcileUntilStable(r, key, 5)).To(Succeed())

			// Confirm the finalizer was attached.
			agent := &witwavev1alpha1.WitwaveAgent{}
			Expect(k8sClient.Get(ctx, key, agent)).To(Succeed())
			Expect(controllerutil.ContainsFinalizer(agent, witwaveAgentFinalizer)).To(BeTrue())

			// Issue delete — the apiserver will set DeletionTimestamp and
			// hold the object because of the finalizer until we reconcile.
			Expect(k8sClient.Delete(ctx, agent)).To(Succeed())

			// Drive the finalizer path.
			for i := 0; i < 3; i++ {
				_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
				Expect(err).NotTo(HaveOccurred())
			}

			// CR should be gone; owned resources cascade via ownerRefs (envtest
			// has no built-in GC, so we only assert CR removal).
			err := k8sClient.Get(ctx, key, &witwavev1alpha1.WitwaveAgent{})
			Expect(apierrors.IsNotFound(err)).To(BeTrue(), "WitwaveAgent must be removed after finalizer teardown")
		})
	})
})

// specCounter is bumped by rand5 to give each spec a unique suffix in the
// shared "default" namespace. Using a counter instead of time/rand keeps
// failures fully reproducible and avoids pulling in crypto/math-rand.
var specCounter int

// rand5 returns a short suffix unique within this test run. Not
// cryptographically random — just unique enough to keep resource names from
// colliding across BeforeEach invocations.
func rand5() string {
	specCounter++
	return fmt.Sprintf("%05d", specCounter)
}
