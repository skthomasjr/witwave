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

// Credential-Secret watch + pod-template checksum (#1114). Makes
// credential rotation observable: any Secret referenced by an agent
// (either the operator-managed per-entry Secret or a user-provided
// ExistingSecret) triggers a reconcile of the owning agent, which
// recomputes a checksum stamped on the pod-template annotation. When
// the checksum actually changes the Deployment rolls, loading the new
// token; the reconciler also bumps witwaveagent_credential_rotations_total.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	witwavev1alpha1 "github.com/witwave-ai/witwave-operator/api/v1alpha1"
)

// credentialsChecksumAnnotation is the pod-template annotation key that
// carries the referenced-Secrets checksum. Pod-template annotation
// churn triggers a rolling restart, so a Secret value change picked up
// by the watch propagates to running pods without manual intervention.
const credentialsChecksumAnnotation = "witwave.ai/credentials-checksum"

// referencedCredentialSecretNames returns every Secret name the given
// agent depends on for credentials. This covers three reference paths:
//
//  1. resolveGitSyncCredentials / resolveBackendCredentials — the
//     operator-managed-or-passthrough resolver output.
//  2. EnvFromSource.SecretRef across each backend's EnvFrom list — a
//     user-provided Secret sourced whole-cloth into the backend env
//     (#1171). Missing these used to mean a rotation of a user-supplied
//     Secret never rolled the Deployment.
//  3. EnvVarSource.SecretKeyRef across each backend's Env list — a
//     user-provided Secret referenced one key at a time (#1171).
//
// Names are returned in stable sort order so the caller's checksum is
// deterministic across reconciles.
func referencedCredentialSecretNames(agent *witwavev1alpha1.WitwaveAgent) []string {
	names := map[string]struct{}{}
	for _, gs := range agent.Spec.GitSyncs {
		r := resolveGitSyncCredentials(agent.Name, gs)
		if r.SecretName != "" {
			names[r.SecretName] = struct{}{}
		}
		// GitSync EnvFrom SecretRef coverage (#1171). GitSyncSpec has
		// no inline Env field — credentials are injected via EnvFrom
		// or the resolver.
		for _, ef := range gs.EnvFrom {
			if ef.SecretRef != nil && ef.SecretRef.Name != "" {
				names[ef.SecretRef.Name] = struct{}{}
			}
		}
	}
	for _, b := range agent.Spec.Backends {
		r := resolveBackendCredentials(agent.Name, b)
		if r.SecretName != "" {
			names[r.SecretName] = struct{}{}
		}
		for _, ef := range b.EnvFrom {
			if ef.SecretRef != nil && ef.SecretRef.Name != "" {
				names[ef.SecretRef.Name] = struct{}{}
			}
		}
		for _, e := range b.Env {
			if e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil && e.ValueFrom.SecretKeyRef.Name != "" {
				names[e.ValueFrom.SecretKeyRef.Name] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(names))
	for n := range names {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// computeCredentialsChecksum hashes the ResourceVersion of each
// referenced Secret into a short checksum suitable for a pod-template
// annotation. Missing Secrets (IsNotFound) are encoded as the empty
// string so a create→delete→create sequence still changes the hash.
func (r *WitwaveAgentReconciler) computeCredentialsChecksum(ctx context.Context, agent *witwavev1alpha1.WitwaveAgent) (string, error) {
	names := referencedCredentialSecretNames(agent)
	if len(names) == 0 {
		return "", nil
	}
	h := sha256.New()
	for _, name := range names {
		sec := &corev1.Secret{}
		err := r.Get(ctx, types.NamespacedName{Namespace: agent.Namespace, Name: name}, sec)
		switch {
		case apierrors.IsNotFound(err):
			fmt.Fprintf(h, "%s=\n", name)
		case err != nil:
			return "", fmt.Errorf("get credential Secret %q: %w", name, err)
		default:
			fmt.Fprintf(h, "%s=%s\n", name, sec.ResourceVersion)
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}

// WitwaveAgentCredentialSecretRefsIndex is the field-indexer key that
// maps every WitwaveAgent to the Secret names its credentials
// reference (#1474). Registered in cmd/main.go alongside the other
// indexers so a Secret change maps O(k) to the k agents that
// actually reference it, instead of O(N) across every agent in the
// namespace (the previous mapper pattern). At 100+ agents + bulk
// rotation, the old full-List path produced a reconcile thundering
// herd; the index shape eliminates it.
const WitwaveAgentCredentialSecretRefsIndex = "spec.backends.credentials.secretRefs"

// WitwaveAgentCredentialSecretRefsExtractor returns every Secret name a
// WitwaveAgent references through its credential configuration. Used
// to populate the field indexer above; must stay in sync with
// referencedCredentialSecretNames (the runtime list). A drift here
// would not cause functional breakage (the legacy fallback in
// enqueueAgentsReferencingSecret still works) but it would silently
// undo the perf improvement.
func WitwaveAgentCredentialSecretRefsExtractor(obj client.Object) []string {
	a, ok := obj.(*witwavev1alpha1.WitwaveAgent)
	if !ok {
		return nil
	}
	return referencedCredentialSecretNames(a)
}

// enqueueAgentsReferencingSecret maps a Secret change to the set of
// WitwaveAgents that reference it (either via ExistingSecret or via the
// operator-managed per-entry name).
//
// Indexed path (#1474): uses `client.MatchingFields` against
// WitwaveAgentCredentialSecretRefsIndex so a Secret rotation on a
// cluster with 100+ agents only enqueues the agents that actually
// reference the rotated Secret — eliminating the reconcile stampede
// that the old full-List mapper produced at fleet scale.
//
// Legacy fallback: when the indexer hasn't been registered (unit
// tests that skip the manager bootstrap, or a caller that wired the
// reconciler without running cmd/main.go), the indexed List errors
// with "field label not supported" and we fall back to a full List +
// in-memory filter. Correctness is identical; performance is
// O(N) instead of O(k). Detection is by error string match because
// controller-runtime doesn't expose a typed sentinel for the case.
func (r *WitwaveAgentReconciler) enqueueAgentsReferencingSecret(ctx context.Context, obj client.Object) []reconcile.Request {
	sec, ok := obj.(*corev1.Secret)
	if !ok {
		return nil
	}
	log := logf.FromContext(ctx).WithValues(
		"secret", sec.Name,
		"namespace", sec.Namespace,
		"component", "credentials-watch",
	)

	// Indexed path.
	list := &witwavev1alpha1.WitwaveAgentList{}
	err := r.List(ctx, list,
		client.InNamespace(sec.Namespace),
		client.MatchingFields{WitwaveAgentCredentialSecretRefsIndex: sec.Name},
	)
	if err != nil && isIndexNotRegistered(err) {
		// Fallback: no indexer → full List + in-memory filter.
		return r.enqueueAgentsReferencingSecretLegacy(ctx, sec, log)
	}
	if err != nil {
		// #1170: don't silently drop the event. Log ERROR + retry
		// once; if the retry also fails, bump the counter and return
		// an empty set (upstream is free to re-fire).
		log.Error(err, "credentials watch: indexed List failed; retrying")
		WitwaveAgentCredentialWatchListErrorsTotal.WithLabelValues(sec.Namespace).Inc()
		retry := &witwavev1alpha1.WitwaveAgentList{}
		if rErr := r.List(ctx, retry,
			client.InNamespace(sec.Namespace),
			client.MatchingFields{WitwaveAgentCredentialSecretRefsIndex: sec.Name},
		); rErr != nil {
			log.Error(rErr, "credentials watch: retry indexed List failed; returning empty enqueue set")
			WitwaveAgentCredentialWatchListErrorsTotal.WithLabelValues(sec.Namespace).Inc()
			return nil
		}
		list = retry
	}

	out := make([]reconcile.Request, 0, len(list.Items))
	for i := range list.Items {
		a := &list.Items[i]
		out = append(out, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: a.Namespace,
				Name:      a.Name,
			},
		})
	}
	return out
}

// enqueueAgentsReferencingSecretLegacy is the pre-#1474 full-List +
// filter path, kept for unit tests and callers that haven't wired the
// field indexer. The in-memory filter stays authoritative; the index
// just narrows the input set.
func (r *WitwaveAgentReconciler) enqueueAgentsReferencingSecretLegacy(
	ctx context.Context,
	sec *corev1.Secret,
	log logr.Logger,
) []reconcile.Request {
	list := &witwavev1alpha1.WitwaveAgentList{}
	if err := r.List(ctx, list, client.InNamespace(sec.Namespace)); err != nil {
		log.Error(err, "credentials watch: failed to List WitwaveAgents for Secret rotation; retrying")
		WitwaveAgentCredentialWatchListErrorsTotal.WithLabelValues(sec.Namespace).Inc()
		retry := &witwavev1alpha1.WitwaveAgentList{}
		if rErr := r.List(ctx, retry, client.InNamespace(sec.Namespace)); rErr != nil {
			log.Error(rErr, "credentials watch: retry List failed; returning empty enqueue set")
			WitwaveAgentCredentialWatchListErrorsTotal.WithLabelValues(sec.Namespace).Inc()
			return nil
		}
		list = retry
	}
	var out []reconcile.Request
	for i := range list.Items {
		a := &list.Items[i]
		for _, n := range referencedCredentialSecretNames(a) {
			if n == sec.Name {
				out = append(out, reconcile.Request{
					NamespacedName: types.NamespacedName{
						Namespace: a.Namespace,
						Name:      a.Name,
					},
				})
				break
			}
		}
	}
	return out
}

// isIndexNotRegistered detects the error controller-runtime returns
// when a MatchingFields predicate references a field that wasn't
// installed via mgr.GetFieldIndexer().IndexField(). The sentinel
// shape is stable across recent controller-runtime versions:
// "Index with name field:... does not exist".
func isIndexNotRegistered(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "does not exist") && strings.Contains(s, "Index with name")
}
