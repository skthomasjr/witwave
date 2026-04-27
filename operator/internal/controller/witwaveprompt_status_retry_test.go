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

// TestWitwavePromptStatusRetryPreservesObservedGenerationWhenSpecMoves covers #1677:
// when the status patch loop hits a 409 conflict and re-Gets a WitwavePrompt
// whose spec generation has advanced, the next patch attempt must NOT stamp
// Status.ObservedGeneration with the new generation. The bindings/readyCount
// values being patched were computed against the OLD generation; advancing
// ObservedGeneration would falsely claim the new spec was reconciled while
// Bindings still reflect the old one.
//
// The correct behavior: apply()'s mismatch branch (target.Generation !=
// reconciledGeneration) preserves the prior ObservedGeneration. The next
// watch-fired reconcile recomputes bindings against the current spec and
// stamps ObservedGeneration honestly.
//
// This replaces the earlier (incorrect) #1636 assertion that ObservedGeneration
// should advance to the new spec generation on retry.
func TestWitwavePromptStatusRetryPreservesObservedGenerationWhenSpecMoves(t *testing.T) {
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
					// status patch. This is the scenario the retry loop
					// must NOT misinterpret as "the new generation was
					// reconciled."
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
	// top of the loop: generation 1, with bindings computed against that
	// spec.
	bindings := []witwavev1alpha1.WitwavePromptBinding{
		{AgentName: "iris", ConfigMapName: "witwaveprompt-rotates-iris", Ready: true},
	}

	if err := r.patchStatusWithConflictRetry(context.Background(), prompt.DeepCopy(), bindings, 1, false); err != nil {
		t.Fatalf("patchStatusWithConflictRetry returned err: %v", err)
	}

	if patchCalls < 2 {
		t.Fatalf("expected at least one retry after the simulated 409; got patchCalls=%d", patchCalls)
	}

	// Re-read from the store and assert ObservedGeneration is NOT 2 (the
	// post-conflict spec generation). The bindings reflect generation 1;
	// stamping ObservedGeneration=2 would lie. The acceptable values are
	// 0 (never written — initial state preserved) or 1 (the generation
	// the bindings were actually reconciled against). The watch-fired
	// reconcile will advance to 2 honestly.
	var got witwavev1alpha1.WitwavePrompt
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: name}, &got); err != nil {
		t.Fatalf("re-Get after patch: %v", err)
	}
	if got.Status.ObservedGeneration == 2 {
		t.Fatalf("ObservedGeneration must NOT advance to the post-conflict generation (2); status would lie about which spec produced Bindings. got=%d", got.Status.ObservedGeneration)
	}

	// Bindings should reflect what we patched in (the iris binding from
	// generation 1's reconcile), regardless of what generation 2's spec
	// looks like.
	if len(got.Status.Bindings) != 1 || got.Status.Bindings[0].AgentName != "iris" {
		t.Fatalf("expected bindings preserved across retry (1 entry, AgentName=iris); got=%+v", got.Status.Bindings)
	}
}

// errStr is a tiny adapter that lets us pass a string where the
// apierrors.NewConflict signature wants an error value.
type errStr string

func (e errStr) Error() string { return string(e) }
