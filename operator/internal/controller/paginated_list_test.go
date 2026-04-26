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
