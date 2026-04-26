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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	witwavev1alpha1 "github.com/witwave-ai/witwave-operator/api/v1alpha1"
)

// TestWitwavePromptStatusRetryRefreshesGeneration covers #1636: when the
// status patch loop hits a 409 conflict and re-Gets a WitwavePrompt whose
// spec generation has advanced, the next patch attempt must stamp
// ObservedGeneration with the latest generation rather than the value
// captured at the top of patchStatusWithConflictRetry.
//
// Without the in-loop refresh, ObservedGeneration would remain pinned to
// the original generation even though the patch successfully wrote
// against the newer object — leaving status pointing at a spec that no
// longer exists.
func TestWitwavePromptStatusRetryRefreshesGeneration(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := witwavev1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add scheme: %v", err)
	}

	const ns, name = "default", "rotates"

	prompt := &witwavev1alpha1.WitwavePrompt{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:  ns,
			Name:       name,
			Generation: 1,
		},
		Spec: witwavev1alpha1.WitwavePromptSpec{
			Kind: witwavev1alpha1.WitwavePromptKindJob,
			Body: "x",
			AgentRefs: []witwavev1alpha1.WitwavePromptAgentRef{
				{Name: "iris"},
			},
		},
	}

	// patchCalls counts SubResourcePatch invocations so we can fail the
	// first one with a Conflict and let later ones through.
	var patchCalls int

	gvr := schema.GroupResource{Group: witwavev1alpha1.GroupVersion.Group, Resource: "witwaveprompts"}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&witwavev1alpha1.WitwavePrompt{}).
		WithObjects(prompt.DeepCopy()).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourcePatch: func(ctx context.Context, cli client.Client, sub string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
				patchCalls++
				if patchCalls == 1 {
					// Simulate a concurrent writer that bumped the spec
					// generation between Reconcile's initial Get and our
					// status patch. This is the scenario the in-loop
					// refresh must observe on the next Get.
					var live witwavev1alpha1.WitwavePrompt
					if err := cli.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, &live); err != nil {
						return err
					}
					live.Generation = 2
					if err := cli.Update(ctx, &live); err != nil {
						return err
					}
					return apierrors.NewConflict(gvr, name, errStr("simulated 409"))
				}
				// On subsequent calls, defer to the underlying store.
				return cli.Status().Patch(ctx, obj, patch, opts...)
			},
		}).
		Build()

	r := &WitwavePromptReconciler{Client: c, Scheme: scheme}

	// Caller-side prompt mirrors what Reconcile would have observed at the
	// top of the loop: generation 1.
	bindings := []witwavev1alpha1.WitwavePromptBinding{
		{AgentName: "iris", ConfigMapName: "witwaveprompt-rotates-iris", Ready: true},
	}

	if err := r.patchStatusWithConflictRetry(context.Background(), prompt.DeepCopy(), bindings, 1, false); err != nil {
		t.Fatalf("patchStatusWithConflictRetry returned err: %v", err)
	}

	if patchCalls < 2 {
		t.Fatalf("expected at least one retry after the simulated 409; got patchCalls=%d", patchCalls)
	}

	// Re-read from the store and assert ObservedGeneration tracks the
	// post-conflict spec generation (#1636). Without the in-loop refresh
	// the apply() branch would short-circuit on target.Generation !=
	// reconciledGeneration and preserve a stale ObservedGeneration of 0.
	var got witwavev1alpha1.WitwavePrompt
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: name}, &got); err != nil {
		t.Fatalf("re-Get after patch: %v", err)
	}
	if got.Status.ObservedGeneration != 2 {
		t.Fatalf("ObservedGeneration must reflect the latest spec generation (2); got %d", got.Status.ObservedGeneration)
	}
}

// errStr is a tiny adapter that lets us pass a string where the
// apierrors.NewConflict signature wants an error value.
type errStr string

func (e errStr) Error() string { return string(e) }
