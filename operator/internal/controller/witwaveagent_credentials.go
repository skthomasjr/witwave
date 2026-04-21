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

// Credentials resolver + Secret reconciliation (#witwave.resolveCredentials
// parity). Mirrors the chart's dev/prod helper:
//
//   existingSecret — pre-created Secret, operator never writes it
//   inline values  — operator reconciles a per-entry Secret
//                    owned by the WitwaveAgent, GC'd when the entry is removed
//   empty          — fall back to the entry's legacy EnvFrom list
//
// The admission webhook refuses inline values without
// AcknowledgeInsecureInline=true so inline credentials can't land in etcd
// or CR history without an explicit opt-in.

import (
	"context"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	witwavev1alpha1 "github.com/witwave-ai/witwave-operator/api/v1alpha1"
)

// componentCredentials labels operator-reconciled credential Secrets so
// the cleanup pass can find them without joining against the parent
// WitwaveAgent's spec. Distinct from every other operator-managed label so
// generic Secret sweeps never touch these.
const componentCredentials = "credentials"

// gitsyncCredentialsSecretName is the per-gitsync credential Secret
// name. Matches the chart's naming (#witwave.resolveCredentials) so operator-
// and chart-rendered deployments reference the same Secret names and a
// deployment converted from one path to the other doesn't churn.
func gitsyncCredentialsSecretName(agentName, entryName string) string {
	return fmt.Sprintf("%s-%s-gitsync-credentials", agentName, entryName)
}

// backendCredentialsSecretName is the per-backend credential Secret name.
func backendCredentialsSecretName(agentName, backendName string) string {
	return fmt.Sprintf("%s-%s-backend-credentials", agentName, backendName)
}

// gitsyncCredentialsResolved tells the render layer which Secret name to
// wire into envFrom and whether the operator owns that Secret. An empty
// Name means "no Secret — fall back to the legacy EnvFrom list".
type gitsyncCredentialsResolved struct {
	SecretName string
	Managed    bool // true when operator reconciles the Secret; false when ExistingSecret pass-through.
}

// resolveGitSyncCredentials implements the three-mode resolver for a
// single GitSync entry. See GitSyncCredentialsSpec for the mode matrix.
func resolveGitSyncCredentials(agentName string, gs witwavev1alpha1.GitSyncSpec) gitsyncCredentialsResolved {
	if gs.Credentials == nil {
		return gitsyncCredentialsResolved{}
	}
	c := gs.Credentials
	// ExistingSecret wins over inline values — mirrors the chart.
	if c.ExistingSecret != "" {
		return gitsyncCredentialsResolved{SecretName: c.ExistingSecret, Managed: false}
	}
	if c.Username != "" || c.Token != "" {
		return gitsyncCredentialsResolved{
			SecretName: gitsyncCredentialsSecretName(agentName, gs.Name),
			Managed:    true,
		}
	}
	return gitsyncCredentialsResolved{}
}

// backendCredentialsResolved is the resolver output for a backend entry.
type backendCredentialsResolved struct {
	SecretName string
	Managed    bool
}

// resolveBackendCredentials implements the three-mode resolver for a
// single BackendSpec. See BackendCredentialsSpec for the mode matrix.
func resolveBackendCredentials(agentName string, b witwavev1alpha1.BackendSpec) backendCredentialsResolved {
	if b.Credentials == nil {
		return backendCredentialsResolved{}
	}
	c := b.Credentials
	if c.ExistingSecret != "" {
		return backendCredentialsResolved{SecretName: c.ExistingSecret, Managed: false}
	}
	if len(c.Secrets) > 0 {
		return backendCredentialsResolved{
			SecretName: backendCredentialsSecretName(agentName, b.Name),
			Managed:    true,
		}
	}
	return backendCredentialsResolved{}
}

// credentialsEnvFromSource returns the EnvFromSource that references a
// resolved credentials Secret. Returns nil when the resolver reports
// no Secret (the caller should fall back to the entry's legacy EnvFrom).
func gitsyncCredentialsEnvFromSource(r gitsyncCredentialsResolved) *corev1.EnvFromSource {
	if r.SecretName == "" {
		return nil
	}
	return &corev1.EnvFromSource{
		SecretRef: &corev1.SecretEnvSource{
			LocalObjectReference: corev1.LocalObjectReference{Name: r.SecretName},
		},
	}
}

func backendCredentialsEnvFromSource(r backendCredentialsResolved) *corev1.EnvFromSource {
	if r.SecretName == "" {
		return nil
	}
	return &corev1.EnvFromSource{
		SecretRef: &corev1.SecretEnvSource{
			LocalObjectReference: corev1.LocalObjectReference{Name: r.SecretName},
		},
	}
}

// buildGitSyncCredentialsSecret renders the operator-managed Secret for
// an inline gitSync credentials entry. Maps Username → GITSYNC_USERNAME
// and Token → GITSYNC_PASSWORD to match the chart. Returns nil when the
// entry's Credentials resolves to anything other than "Managed" (e.g.
// ExistingSecret passthrough or no credentials at all).
func buildGitSyncCredentialsSecret(agent *witwavev1alpha1.WitwaveAgent, gs witwavev1alpha1.GitSyncSpec) *corev1.Secret {
	r := resolveGitSyncCredentials(agent.Name, gs)
	if !r.Managed {
		return nil
	}
	labels := agentLabels(agent)
	labels[labelComponent] = componentCredentials
	data := map[string][]byte{}
	if gs.Credentials.Username != "" {
		data["GITSYNC_USERNAME"] = []byte(gs.Credentials.Username)
	}
	if gs.Credentials.Token != "" {
		data["GITSYNC_PASSWORD"] = []byte(gs.Credentials.Token)
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.SecretName,
			Namespace: agent.Namespace,
			Labels:    labels,
		},
		Type: corev1.SecretTypeOpaque,
		Data: data,
	}
}

// buildBackendCredentialsSecret renders the operator-managed Secret for
// an inline backend credentials entry. The Secrets map is passed through
// verbatim so each backend can set its own env-var shape
// (CLAUDE_CODE_OAUTH_TOKEN, OPENAI_API_KEY, etc.). Deterministic ordering
// isn't strictly required for Secret.Data (Kubernetes stores it as a
// map), but keys are sorted in case future diff logic compares bytes.
func buildBackendCredentialsSecret(agent *witwavev1alpha1.WitwaveAgent, b witwavev1alpha1.BackendSpec) *corev1.Secret {
	r := resolveBackendCredentials(agent.Name, b)
	if !r.Managed {
		return nil
	}
	labels := agentLabels(agent)
	labels[labelComponent] = componentCredentials

	keys := make([]string, 0, len(b.Credentials.Secrets))
	for k := range b.Credentials.Secrets {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	data := make(map[string][]byte, len(keys))
	for _, k := range keys {
		data[k] = []byte(b.Credentials.Secrets[k])
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.SecretName,
			Namespace: agent.Namespace,
			Labels:    labels,
		},
		Type: corev1.SecretTypeOpaque,
		Data: data,
	}
}

// gitSyncEnvFromWithCredentials returns the EnvFrom list to wire into a
// git-sync init/sidecar container. When Credentials resolves to a Secret
// (operator-managed OR ExistingSecret), that Secret is prepended so
// gitsync sees GITSYNC_USERNAME / GITSYNC_PASSWORD ahead of any keys the
// legacy EnvFrom list sets — the chart's helper does the same thing. The
// legacy list is preserved verbatim so operators migrating from envFrom-
// only to credentials mid-flight don't lose SSH-key envs or other
// secondary sources.
func gitSyncEnvFromWithCredentials(agent *witwavev1alpha1.WitwaveAgent, gs witwavev1alpha1.GitSyncSpec) []corev1.EnvFromSource {
	src := gitsyncCredentialsEnvFromSource(resolveGitSyncCredentials(agent.Name, gs))
	if src == nil {
		return gs.EnvFrom
	}
	out := make([]corev1.EnvFromSource, 0, len(gs.EnvFrom)+1)
	out = append(out, *src)
	out = append(out, gs.EnvFrom...)
	return out
}

// backendEnvFromWithCredentials returns the EnvFrom list for a backend
// container with the resolved credentials Secret (if any) prepended. See
// gitSyncEnvFromWithCredentials for the rationale.
func backendEnvFromWithCredentials(agent *witwavev1alpha1.WitwaveAgent, b witwavev1alpha1.BackendSpec) []corev1.EnvFromSource {
	src := backendCredentialsEnvFromSource(resolveBackendCredentials(agent.Name, b))
	if src == nil {
		return b.EnvFrom
	}
	out := make([]corev1.EnvFromSource, 0, len(b.EnvFrom)+1)
	out = append(out, *src)
	out = append(out, b.EnvFrom...)
	return out
}

// reconcileCredentialsSecrets applies every operator-managed credential
// Secret the WitwaveAgent currently calls for AND garbage-collects any
// Secrets this reconciler previously created for entries no longer
// present in spec (#witwave.resolveCredentials parity, operator edition).
//
// Cleanup pass uses the distinct componentCredentials label so it never
// touches Secrets reconciled by other controllers or user-created
// Secrets that happen to share the name pattern. IsControlledBy is also
// dual-checked before any delete.
func (r *WitwaveAgentReconciler) reconcileCredentialsSecrets(ctx context.Context, agent *witwavev1alpha1.WitwaveAgent) (err error) {
	ctx, _, finish := startStepSpan(ctx, "witwaveagent.reconcileCredentialsSecrets")
	defer finish(&err)

	desired := map[string]*corev1.Secret{}
	for _, gs := range agent.Spec.GitSyncs {
		if sec := buildGitSyncCredentialsSecret(agent, gs); sec != nil {
			desired[sec.Name] = sec
		}
	}
	for _, b := range agent.Spec.Backends {
		if !backendEnabled(b) {
			continue
		}
		if sec := buildBackendCredentialsSecret(agent, b); sec != nil {
			desired[sec.Name] = sec
		}
	}

	for _, sec := range desired {
		if err := controllerutil.SetControllerReference(agent, sec, r.Scheme); err != nil {
			return fmt.Errorf("set owner on Secret %s: %w", sec.Name, err)
		}
		existing := &corev1.Secret{}
		getErr := r.Get(ctx, client.ObjectKeyFromObject(sec), existing)
		switch {
		case apierrors.IsNotFound(getErr):
			if err := r.Create(ctx, sec); err != nil {
				return fmt.Errorf("create Secret %s: %w", sec.Name, err)
			}
			continue
		case getErr != nil:
			return fmt.Errorf("get Secret %s: %w", sec.Name, getErr)
		}
		// Refuse to mutate Secrets we don't own. A user-managed Secret
		// that collides by name is left alone; the workload referencing
		// it keeps working.
		if !metav1.IsControlledBy(existing, agent) {
			continue
		}
		if err := controllerutil.SetControllerReference(agent, existing, r.Scheme); err != nil {
			return fmt.Errorf("set owner on existing Secret %s: %w", sec.Name, err)
		}
		// #1562: merge Data rather than overwriting so a user who
		// kubectl-patched an extra key onto a managed credential Secret
		// (common pattern for adding a sidecar env / pull-through token)
		// doesn't lose it on every reconcile. The operator's keys are
		// authoritative — desired values overwrite — but foreign keys
		// pass through unchanged. Matches the labels/annotations merge
		// posture in mergeOwnedStringMap.
		if existing.Data == nil {
			existing.Data = map[string][]byte{}
		}
		for k, v := range sec.Data {
			existing.Data[k] = v
		}
		existing.Labels = mergeOwnedStringMap(existing.Labels, sec.Labels, witwaveAgentOwnedLabelKeys)
		existing.Type = sec.Type
		if err := r.Update(ctx, existing); err != nil {
			return fmt.Errorf("update Secret %s: %w", sec.Name, err)
		}
	}

	// Cleanup: list Secrets in this namespace carrying our credentials
	// component label and owned by THIS WitwaveAgent; delete any not in the
	// desired set. Mirrors reconcileConfigMaps / applyBackendPVCs.
	existing := &corev1.SecretList{}
	if err := r.List(ctx, existing,
		client.InNamespace(agent.Namespace),
		client.MatchingLabels{
			labelName:      agent.Name,
			labelComponent: componentCredentials,
			labelManagedBy: managedBy,
		},
	); err != nil {
		return fmt.Errorf("list credential Secrets for cleanup: %w", err)
	}
	for i := range existing.Items {
		sec := &existing.Items[i]
		if _, wanted := desired[sec.Name]; wanted {
			continue
		}
		if !metav1.IsControlledBy(sec, agent) {
			continue
		}
		if err := r.Delete(ctx, sec); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete stale Secret %s: %w", sec.Name, err)
		}
	}
	return nil
}
