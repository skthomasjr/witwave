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

	"k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// paginatedListPageSize bounds how many objects the apiserver returns per
// chunk when paginating List calls in cleanup paths (#1656). 500 mirrors the
// default kubectl/client-go chunk size and keeps a single response well under
// the apiserver's 1MiB watchcache pressure threshold even for large
// ConfigMap/Secret/PVC payloads.
const paginatedListPageSize int64 = 500

// paginatedList walks every page of a List call, invoking visit once per
// page with the freshly-populated list object (#1656).
//
// Caller passes a typed list (e.g. *corev1.ConfigMapList) plus the same
// ListOptions they would pass to client.List — InNamespace, MatchingLabels,
// etc. The helper appends client.Limit + client.Continue on each iteration,
// invokes visit, then advances using the response's continue token. visit
// MUST process or copy the page contents before returning; the next iteration
// reuses the same list object and overwrites Items.
//
// This pattern matters in cleanup paths where the cluster may hold thousands
// of label-matched objects: a single unbounded List can pin the apiserver's
// watchcache and time out the reconcile. With Limit set, controller-runtime
// returns a Continue token whenever the result is truncated; we keep going
// until the token is empty.
//
// IMPORTANT (#1738): pass ``r.APIReader``, NOT ``r.Client``. The cached
// client backed by the informer cache returns ALL matching items in a
// single call regardless of ``client.Limit`` — pagination flags are
// silently ignored and the response's Continue token is empty, so the
// loop exits after one (potentially huge) page. APIReader bypasses the
// cache and goes straight to the apiserver, which honours Limit / Continue
// as documented. Use ``r.Client`` only for primary-resource reads where
// the cached snapshot is desirable; use ``r.APIReader`` for cleanup
// sweeps where pagination MUST actually fire.
//
// The helper accepts the narrower ``client.Reader`` interface so callers
// can pass either ``r.APIReader`` (preferred for cleanup) or, in tests,
// any fake.Client built via the controller-runtime fake builder (which
// honours Limit / Continue against its in-memory tracker).
func paginatedList(
	ctx context.Context,
	c client.Reader,
	list client.ObjectList,
	visit func() error,
	opts ...client.ListOption,
) error {
	cont := ""
	for {
		pageOpts := append([]client.ListOption{}, opts...)
		pageOpts = append(pageOpts, client.Limit(paginatedListPageSize))
		if cont != "" {
			pageOpts = append(pageOpts, client.Continue(cont))
		}
		if err := c.List(ctx, list, pageOpts...); err != nil {
			return err
		}
		if err := visit(); err != nil {
			return err
		}
		next, err := meta.NewAccessor().Continue(list)
		if err != nil {
			return err
		}
		if next == "" {
			return nil
		}
		cont = next
	}
}
