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

// Per-agent "internal" Secret reconciliation.
//
// Holds operator-minted, pod-internal env vars that have no business being
// user-provided — runtime coordination secrets the controller must own end
// to end. Today: HOOK_EVENTS_AUTH_TOKEN, which gates claude→harness POSTs
// on the internal /internal/events/hook-decision endpoint. Future internal
// env vars (event-bus tokens, mTLS material, etc.) land in the same Secret
// rather than each getting its own.
//
// Lifecycle:
//   - Created on first reconcile when absent — token generated via
//     crypto/rand (32 bytes, base64 url-safe encoded; ~43 chars).
//   - Preserved across subsequent reconciles — only Update when keys are
//     missing or empty. The token is NOT rotated automatically; rotation
//     is operator-initiated by deleting the Secret and waiting for the
//     next reconcile to re-mint it.
//   - Cascade-deleted with the agent via OwnerReference; no separate
//     cleanup pass needed.
//
// Distinct from the credential Secret reconciler (componentCredentials)
// because the lifecycle differs: credential Secrets are spec-driven and
// rebuilt on every reconcile; this one is auto-managed and idempotent.
// Uses componentInternal label so the credential reconciler's cleanup
// sweep doesn't see it.

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	witwavev1alpha1 "github.com/witwave-ai/witwave-operator/api/v1alpha1"
)

// componentInternal labels operator-reconciled internal-state Secrets so
// the credential-Secret cleanup sweep (which scopes to componentCredentials)
// never sees them. One Secret per WitwaveAgent.
const componentInternal = "internal"

// internalSecretAuthTokenKey is the env-var name the harness reads
// (`HOOK_EVENTS_AUTH_TOKEN`) and the backends accept as one of three
// resolution sources (per backends/claude/executor.py:_resolve_harness_events_auth_token).
// Backends prefer the dedicated `HARNESS_EVENTS_AUTH_TOKEN`; we set BOTH
// keys in the Secret so either resolution path lands the token without
// the legacy-fallback warning fire.
const (
	internalSecretAuthTokenKey        = "HOOK_EVENTS_AUTH_TOKEN"
	internalSecretBackendAuthTokenKey = "HARNESS_EVENTS_AUTH_TOKEN"
)

// internalTokenByteLen is the entropy budget for the auto-minted token.
// 32 bytes = 256 bits, base64-encoded to ~43 chars. More than enough for
// HMAC-grade pod-internal auth.
const internalTokenByteLen = 32

// internalSecretName returns the per-agent internal Secret name. Single
// Secret per agent — every internal env var the operator mints lives
// under this one ObjectMeta.Name.
func internalSecretName(agentName string) string {
	return agentName + "-internal"
}

// generateInternalAuthToken returns a fresh random token suitable for
// HOOK_EVENTS_AUTH_TOKEN. Uses crypto/rand so a low-entropy host (CI
// containers, Docker Desktop) doesn't degrade silently to math/rand.
// Returned as URL-safe base64 (no padding) so it's safe in HTTP headers
// and shell quoting.
func generateInternalAuthToken() (string, error) {
	buf := make([]byte, internalTokenByteLen)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("read random bytes for internal token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// internalSecretEnvFromSource returns the EnvFromSource that wires the
// per-agent internal Secret as envFrom on a container. Always non-nil —
// every WitwaveAgent has an internal Secret reconciled.
func internalSecretEnvFromSource(agentName string) corev1.EnvFromSource {
	return corev1.EnvFromSource{
		SecretRef: &corev1.SecretEnvSource{
			LocalObjectReference: corev1.LocalObjectReference{Name: internalSecretName(agentName)},
		},
	}
}

// reconcileInternalSecret ensures the per-agent internal Secret exists
// and carries non-empty values for every required key. Idempotent on the
// happy path — the token is preserved across reconciles (no silent
// rotation). Missing or empty keys trigger a regen-and-update so a
// half-populated Secret heals on the next reconcile.
func (r *WitwaveAgentReconciler) reconcileInternalSecret(ctx context.Context, agent *witwavev1alpha1.WitwaveAgent) (err error) {
	ctx, _, finish := startStepSpan(ctx, "witwaveagent.reconcileInternalSecret")
	defer finish(&err)

	name := internalSecretName(agent.Name)
	key := client.ObjectKey{Namespace: agent.Namespace, Name: name}

	existing := &corev1.Secret{}
	getErr := r.Get(ctx, key, existing)
	switch {
	case apierrors.IsNotFound(getErr):
		token, err := generateInternalAuthToken()
		if err != nil {
			return err
		}
		sec := buildInternalSecret(agent, token)
		if err := controllerutil.SetControllerReference(agent, sec, r.Scheme); err != nil {
			return fmt.Errorf("set owner on Secret %s: %w", name, err)
		}
		if err := r.Create(ctx, sec); err != nil {
			return fmt.Errorf("create internal Secret %s: %w", name, err)
		}
		return nil
	case getErr != nil:
		return fmt.Errorf("get internal Secret %s: %w", name, getErr)
	}

	// Existing Secret — refuse to mutate one we don't own. A user-managed
	// Secret colliding by name is left alone and the workload may
	// 401 forever; the user gets to fix or delete it.
	if !metav1.IsControlledBy(existing, agent) {
		return nil
	}

	// Heal missing keys without rotating the existing token.
	updates, needsUpdate, err := healInternalSecretKeys(existing.Data)
	if err != nil {
		return err
	}
	if !needsUpdate {
		return nil
	}
	if existing.Data == nil {
		existing.Data = map[string][]byte{}
	}
	for k, v := range updates {
		existing.Data[k] = v
	}
	if existing.Labels == nil {
		existing.Labels = map[string]string{}
	}
	for k, v := range internalSecretLabels(agent) {
		existing.Labels[k] = v
	}
	if err := r.Update(ctx, existing); err != nil {
		return fmt.Errorf("update internal Secret %s: %w", name, err)
	}
	return nil
}

// buildInternalSecret renders the desired state for a freshly-created
// internal Secret. Both auth-token keys carry the same value — backends
// resolve via HARNESS_EVENTS_AUTH_TOKEN first, harness via
// HOOK_EVENTS_AUTH_TOKEN; we satisfy both without forcing the legacy
// fallback path on the backend side.
func buildInternalSecret(agent *witwavev1alpha1.WitwaveAgent, token string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      internalSecretName(agent.Name),
			Namespace: agent.Namespace,
			Labels:    internalSecretLabels(agent),
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			internalSecretAuthTokenKey:        []byte(token),
			internalSecretBackendAuthTokenKey: []byte(token),
		},
	}
}

// internalSecretLabels combines the agent's standard label set with the
// componentInternal marker so the credential reconciler's cleanup sweep
// (label-selector componentCredentials) doesn't see this Secret.
func internalSecretLabels(agent *witwavev1alpha1.WitwaveAgent) map[string]string {
	labels := agentLabels(agent)
	labels[labelComponent] = componentInternal
	return labels
}

// healInternalSecretKeys checks an existing Secret's Data for missing or
// empty values across the required key set. Returns (updates, needsUpdate)
// where updates is a map of just the keys that need writing — preserves
// any existing non-empty value untouched. When ALL keys are missing/empty
// it generates a single fresh token and assigns it to every required key
// so the backend + harness see the same value.
func healInternalSecretKeys(data map[string][]byte) (map[string][]byte, bool, error) {
	required := []string{internalSecretAuthTokenKey, internalSecretBackendAuthTokenKey}
	updates := map[string][]byte{}

	// Find any non-empty existing value to use as the canonical token.
	// This way one of the keys carrying a value is preserved as-is and
	// missing siblings get the same value (vs minting an unrelated
	// new token for the half-populated keys).
	var canonical []byte
	for _, k := range required {
		if v, ok := data[k]; ok && len(v) > 0 {
			canonical = v
			break
		}
	}
	if canonical == nil {
		token, err := generateInternalAuthToken()
		if err != nil {
			return nil, false, err
		}
		canonical = []byte(token)
	}

	for _, k := range required {
		if v, ok := data[k]; !ok || len(v) == 0 {
			updates[k] = canonical
		}
	}
	return updates, len(updates) > 0, nil
}

// harnessEnvFromWithInternal returns the EnvFrom list for the harness
// container with the per-agent internal Secret prepended to whatever the
// CR's Spec.EnvFrom carries. Prepending puts the operator's keys first
// so the user's later envFrom wins on key collision (consistent with
// gitSyncEnvFromWithCredentials' precedence posture). A user explicitly
// setting HOOK_EVENTS_AUTH_TOKEN via spec.envFrom overrides the
// operator-managed value.
func harnessEnvFromWithInternal(agent *witwavev1alpha1.WitwaveAgent) []corev1.EnvFromSource {
	out := make([]corev1.EnvFromSource, 0, len(agent.Spec.EnvFrom)+1)
	out = append(out, internalSecretEnvFromSource(agent.Name))
	out = append(out, agent.Spec.EnvFrom...)
	return out
}

// backendEnvFromWithInternal wraps backendEnvFromWithCredentials by
// prepending the per-agent internal Secret. Same precedence rule as the
// harness helper: user-provided envFrom wins on key collision.
func backendEnvFromWithInternal(agent *witwavev1alpha1.WitwaveAgent, b witwavev1alpha1.BackendSpec) []corev1.EnvFromSource {
	credEnv := backendEnvFromWithCredentials(agent, b)
	out := make([]corev1.EnvFromSource, 0, len(credEnv)+1)
	out = append(out, internalSecretEnvFromSource(agent.Name))
	out = append(out, credEnv...)
	return out
}
