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
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// TestPaginatedListWalksEveryPage covers #1656: cleanup-path List calls must
// honour client.Limit/Continue tokens so that namespaces holding thousands of
// label-matched ConfigMaps/Secrets/PVCs are walked in bounded chunks. The
// fake client is wrapped in an interceptor that:
//
//  1. Asserts every page request carries a Limit (chunked pagination).
//  2. Returns three pages (2, 2, 1 items) with continue tokens between them.
//  3. Drops the continue token on the final page so the loop terminates.
//
// The visit callback collects names per page; we assert all 5 items are seen
// across exactly 3 pages, which is only possible if paginatedList advances
// the Continue token from the response metadata.
func TestPaginatedListWalksEveryPage(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add corev1: %v", err)
	}

	// Three deliberately ordered pages. The fake client's actual contents
	// don't matter — the interceptor short-circuits List entirely so we can
	// drive the pagination state machine directly.
	pages := [][]string{
		{"cm-a", "cm-b"},
		{"cm-c", "cm-d"},
		{"cm-e"},
	}
	tokens := []string{"tok-1", "tok-2", ""}

	var (
		listCalls  int
		seenLimits []int64
		seenTokens []string
		visitedAll []string
		visitCalls int
	)

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithInterceptorFuncs(interceptor.Funcs{
			List: func(ctx context.Context, cli client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				idx := listCalls
				listCalls++
				if idx >= len(pages) {
					t.Fatalf("paginatedList made %d list calls; expected at most %d", listCalls, len(pages))
				}

				// Inspect ListOptions: every page must carry a Limit; pages
				// 2+ must carry the previous response's Continue token.
				lo := &client.ListOptions{}
				for _, o := range opts {
					o.ApplyToList(lo)
				}
				seenLimits = append(seenLimits, lo.Limit)
				seenTokens = append(seenTokens, lo.Continue)

				cml, ok := list.(*corev1.ConfigMapList)
				if !ok {
					t.Fatalf("unexpected list type %T", list)
				}
				cml.Items = cml.Items[:0]
				for _, n := range pages[idx] {
					cml.Items = append(cml.Items, corev1.ConfigMap{
						ObjectMeta: metav1.ObjectMeta{Name: n, Namespace: "default"},
					})
				}
				cml.Continue = tokens[idx]
				return nil
			},
		}).
		Build()

	list := &corev1.ConfigMapList{}
	err := paginatedList(context.Background(), c, list, func() error {
		visitCalls++
		for i := range list.Items {
			visitedAll = append(visitedAll, list.Items[i].Name)
		}
		return nil
	}, client.InNamespace("default"))
	if err != nil {
		t.Fatalf("paginatedList: %v", err)
	}

	if listCalls != 3 {
		t.Fatalf("list calls: got %d, want 3", listCalls)
	}
	if visitCalls != 3 {
		t.Fatalf("visit calls: got %d, want 3", visitCalls)
	}

	// Every page MUST request a bounded chunk; an unset Limit (0) defeats
	// the whole point of #1656.
	for i, lim := range seenLimits {
		if lim != paginatedListPageSize {
			t.Fatalf("page %d limit: got %d, want %d", i, lim, paginatedListPageSize)
		}
	}

	// First call has no Continue (start of stream); subsequent calls must
	// echo the previous response's token verbatim.
	wantTokens := []string{"", "tok-1", "tok-2"}
	for i, tok := range seenTokens {
		if tok != wantTokens[i] {
			t.Fatalf("page %d continue token: got %q, want %q", i, tok, wantTokens[i])
		}
	}

	wantNames := []string{"cm-a", "cm-b", "cm-c", "cm-d", "cm-e"}
	if len(visitedAll) != len(wantNames) {
		t.Fatalf("visited items: got %v, want %v", visitedAll, wantNames)
	}
	for i := range wantNames {
		if visitedAll[i] != wantNames[i] {
			t.Fatalf("item %d: got %q, want %q", i, visitedAll[i], wantNames[i])
		}
	}
}

// TestPaginatedListVisitErrorShortCircuits covers #1708: when the visit
// callback returns an error mid-stream, paginatedList must propagate
// that error immediately and NOT make further List calls. Cleanup
// paths (label-selector deletes across thousands of resources) rely
// on this short-circuit so a transient visit error doesn't silently
// half-complete the sweep.
func TestPaginatedListVisitErrorShortCircuits(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add corev1: %v", err)
	}

	var listCalls, visitCalls int
	visitErr := errStr("visit-failed")

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithInterceptorFuncs(interceptor.Funcs{
			List: func(ctx context.Context, cli client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				listCalls++
				cml := list.(*corev1.ConfigMapList)
				cml.Items = []corev1.ConfigMap{
					{ObjectMeta: metav1.ObjectMeta{Name: "page-1-item", Namespace: "default"}},
				}
				// Continue token is non-empty so a non-short-circuiting
				// implementation would loop and call List again.
				cml.Continue = "more-data-token"
				return nil
			},
		}).
		Build()

	list := &corev1.ConfigMapList{}
	err := paginatedList(context.Background(), c, list, func() error {
		visitCalls++
		return visitErr
	})

	if err != visitErr {
		t.Fatalf("expected visitErr to propagate verbatim; got %v", err)
	}
	if listCalls != 1 {
		t.Fatalf("visit error must short-circuit before the next List call; got %d List calls (expected 1)", listCalls)
	}
	if visitCalls != 1 {
		t.Fatalf("visit must be called exactly once before erroring out; got %d", visitCalls)
	}
}

// TestPaginatedListListErrorPropagates: a List error on any page should
// propagate immediately, NOT swallow + continue. This is the failure
// mode where the apiserver becomes briefly unavailable mid-sweep — we
// want the caller (a reconcile loop with its own retry policy) to see
// the error so it can back off, not silently stop with partial state.
func TestPaginatedListListErrorPropagates(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add corev1: %v", err)
	}

	var listCalls int
	listErr := errStr("apiserver-unreachable")

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithInterceptorFuncs(interceptor.Funcs{
			List: func(ctx context.Context, cli client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				listCalls++
				if listCalls == 2 {
					// First page succeeded, second page fails.
					return listErr
				}
				cml := list.(*corev1.ConfigMapList)
				cml.Items = []corev1.ConfigMap{
					{ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: "default"}},
				}
				cml.Continue = "tok-1"
				return nil
			},
		}).
		Build()

	list := &corev1.ConfigMapList{}
	err := paginatedList(context.Background(), c, list, func() error { return nil })
	if err != listErr {
		t.Fatalf("expected List error to propagate; got %v", err)
	}
	if listCalls != 2 {
		t.Fatalf("List should have been called twice (success then failure); got %d", listCalls)
	}
}

// TestPaginatedListSinglePage covers the common case: when the apiserver
// returns everything in one response (Continue == ""), paginatedList must
// invoke visit exactly once and stop, not loop forever.
func TestPaginatedListSinglePage(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add corev1: %v", err)
	}

	var listCalls, visitCalls int

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithInterceptorFuncs(interceptor.Funcs{
			List: func(ctx context.Context, cli client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				listCalls++
				cml := list.(*corev1.ConfigMapList)
				cml.Items = []corev1.ConfigMap{
					{ObjectMeta: metav1.ObjectMeta{Name: "only", Namespace: "default"}},
				}
				cml.Continue = ""
				return nil
			},
		}).
		Build()

	list := &corev1.ConfigMapList{}
	err := paginatedList(context.Background(), c, list, func() error {
		visitCalls++
		return nil
	})
	if err != nil {
		t.Fatalf("paginatedList: %v", err)
	}
	if listCalls != 1 {
		t.Fatalf("list calls: got %d, want 1", listCalls)
	}
	if visitCalls != 1 {
		t.Fatalf("visit calls: got %d, want 1", visitCalls)
	}
}
