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
func paginatedList(
	ctx context.Context,
	c client.Client,
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
