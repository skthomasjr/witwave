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
// token; the reconciler also bumps nyxagent_credential_rotations_total.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	nyxv1alpha1 "github.com/nyx-ai/nyx-operator/api/v1alpha1"
)

// credentialsChecksumAnnotation is the pod-template annotation key that
// carries the referenced-Secrets checksum. Pod-template annotation
// churn triggers a rolling restart, so a Secret value change picked up
// by the watch propagates to running pods without manual intervention.
const credentialsChecksumAnnotation = "nyx.ai/credentials-checksum"

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
func referencedCredentialSecretNames(agent *nyxv1alpha1.NyxAgent) []string {
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
func (r *NyxAgentReconciler) computeCredentialsChecksum(ctx context.Context, agent *nyxv1alpha1.NyxAgent) (string, error) {
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

// enqueueAgentsReferencingSecret maps a Secret change to the set of
// NyxAgents that reference it (either via ExistingSecret or via the
// operator-managed per-entry name). The mapper lists all NyxAgents in
// the namespace and checks each — acceptable because the watch fires
// only on Secret updates, which are low-frequency relative to reconcile
// churn.
func (r *NyxAgentReconciler) enqueueAgentsReferencingSecret(ctx context.Context, obj client.Object) []reconcile.Request {
	sec, ok := obj.(*corev1.Secret)
	if !ok {
		return nil
	}
	log := logf.FromContext(ctx).WithValues(
		"secret", sec.Name,
		"namespace", sec.Namespace,
		"component", "credentials-watch",
	)
	list := &nyxv1alpha1.NyxAgentList{}
	if err := r.List(ctx, list, client.InNamespace(sec.Namespace)); err != nil {
		// #1170: swallowing this error previously meant a rotated
		// Secret never reached any NyxAgent on that pass — the watch
		// would effectively drop the event. Log at ERROR so operators
		// see the miss, bump a counter for alerting, and fall back to
		// a best-effort second List. If the retry also fails we still
		// return an empty set (upstream is free to issue another
		// event), but we do not silently eat the observability.
		log.Error(err, "credentials watch: failed to List NyxAgents for Secret rotation; retrying")
		NyxAgentCredentialWatchListErrorsTotal.WithLabelValues(sec.Namespace).Inc()
		retry := &nyxv1alpha1.NyxAgentList{}
		if rErr := r.List(ctx, retry, client.InNamespace(sec.Namespace)); rErr != nil {
			log.Error(rErr, "credentials watch: retry List failed; returning empty enqueue set")
			NyxAgentCredentialWatchListErrorsTotal.WithLabelValues(sec.Namespace).Inc()
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
