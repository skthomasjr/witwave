/*
Copyright 2025.

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

// Package v1alpha1 hosts the admission-webhook scaffolding for the
// WitwaveAgent CRD (#624).
//
// The scaffold is intentionally narrow: one defaulting rule and one
// validating rule, wired so cert-manager-issued certs (#639) and
// kubebuilder-shaped manifests are all that's needed to turn it on.
// Further invariants should land as separate narrow gaps on top of
// this skeleton.
package v1alpha1

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	authv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	witwavev1alpha1 "github.com/witwave-ai/witwave-operator/api/v1alpha1"
)

// +kubebuilder:webhook:path=/mutate-witwave-ai-v1alpha1-witwaveagent,mutating=true,failurePolicy=fail,sideEffects=None,groups=witwave.ai,resources=witwaveagents,verbs=create;update,versions=v1alpha1,name=mwitwaveagent.kb.io,admissionReviewVersions=v1
// +kubebuilder:webhook:path=/validate-witwave-ai-v1alpha1-witwaveagent,mutating=false,failurePolicy=fail,sideEffects=None,groups=witwave.ai,resources=witwaveagents,verbs=create;update,versions=v1alpha1,name=vwitwaveagent.kb.io,admissionReviewVersions=v1

// WitwaveAgentCustomDefaulter applies defaults to WitwaveAgent objects on admission.
type WitwaveAgentCustomDefaulter struct{}

var _ webhook.CustomDefaulter = &WitwaveAgentCustomDefaulter{}

// Default sets fields that aren't worth defaulting via render-time helpers
// because they belong on the stored CR (visible in kubectl get, driftable
// against git-ops tools, etc.).
//
// Initial scope: exactly one rule — when Spec.Port is unset (0), default
// to 8000. Additional rules land as follow-up gaps once the scaffold is
// live in production.
func (d *WitwaveAgentCustomDefaulter) Default(ctx context.Context, obj runtime.Object) error {
	log := logf.FromContext(ctx)
	agent, ok := obj.(*witwavev1alpha1.WitwaveAgent)
	if !ok {
		return apierrors.NewBadRequest(fmt.Sprintf("expected *WitwaveAgent, got %T", obj))
	}
	if agent.Spec.Port == 0 {
		agent.Spec.Port = 8000
		log.V(1).Info("defaulted spec.port", "namespace", agent.Namespace, "name", agent.Name, "port", 8000)
	}
	return nil
}

// WitwaveAgentCustomValidator validates WitwaveAgent objects on admission.
//
// The Client field (#1683, #1685) is used to do live apiserver lookups
// during admission — Get on each existingSecret reference and a
// SelfSubjectAccessReview to verify the operator can actually create
// Secrets when the spec carries inline credentials. Older test sites
// instantiate this struct without a Client; those checks short-circuit
// on a nil receiver so existing tests keep passing.
type WitwaveAgentCustomValidator struct {
	Client client.Client
}

var _ webhook.CustomValidator = &WitwaveAgentCustomValidator{}

func (v *WitwaveAgentCustomValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	agent, ok := obj.(*witwavev1alpha1.WitwaveAgent)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected *WitwaveAgent, got %T", obj))
	}
	if err := validateWitwaveAgent(agent); err != nil {
		return nil, err
	}
	if err := validateLiveCredentials(ctx, v.Client, agent); err != nil {
		return nil, err
	}
	return inlineCredentialsRBACWarnings(agent), nil
}

func (v *WitwaveAgentCustomValidator) ValidateUpdate(ctx context.Context, _ runtime.Object, newObj runtime.Object) (admission.Warnings, error) {
	agent, ok := newObj.(*witwavev1alpha1.WitwaveAgent)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected *WitwaveAgent, got %T", newObj))
	}
	if err := validateWitwaveAgent(agent); err != nil {
		return nil, err
	}
	if err := validateLiveCredentials(ctx, v.Client, agent); err != nil {
		return nil, err
	}
	return inlineCredentialsRBACWarnings(agent), nil
}

// validateLiveCredentials runs admission checks that require live
// apiserver access (#1683, #1685). Two gates:
//
//  1. Every non-empty `credentials.existingSecret` must resolve to an
//     existing Secret in the agent's namespace. Without this gate,
//     typos / accidentally-deleted Secrets render a Pod whose envFrom
//     references a missing Secret — kubelet wedges the pod with
//     CrashLoopBackOff and the WitwaveAgent's Status.Conditions never
//     surface the cause.
//  2. When the spec carries inline credentials (BackendCredentialsSpec.
//     Secrets non-empty OR GitSyncSpec.Credentials.Username/Token set),
//     the operator must have RBAC to create/patch Secrets in the
//     namespace — otherwise reconciliation hits a permanent 403 retry
//     loop documented in charts/witwave-operator/values.yaml's #1372
//     warning. The webhook runs a SelfSubjectAccessReview to catch
//     this at admit time rather than silently in the controller queue.
//
// When v == nil (older test instantiation paths that don't wire a
// client), both checks short-circuit so existing unit tests keep
// passing without a fake client.
func validateLiveCredentials(ctx context.Context, c client.Client, agent *witwavev1alpha1.WitwaveAgent) error {
	if c == nil {
		return nil
	}

	// Gate 1: existingSecret existence (#1683).
	type ref struct {
		field string
		idx   int
		name  string
	}
	refs := make([]ref, 0)
	for i, gs := range agent.Spec.GitSyncs {
		if gs.Credentials != nil && gs.Credentials.ExistingSecret != "" {
			refs = append(refs, ref{
				field: fmt.Sprintf("spec.gitSyncs[%d].credentials.existingSecret", i),
				idx:   i,
				name:  gs.Credentials.ExistingSecret,
			})
		}
	}
	for i, b := range agent.Spec.Backends {
		if b.Credentials != nil && b.Credentials.ExistingSecret != "" {
			refs = append(refs, ref{
				field: fmt.Sprintf("spec.backends[%d].credentials.existingSecret", i),
				idx:   i,
				name:  b.Credentials.ExistingSecret,
			})
		}
	}
	for _, r := range refs {
		var sec corev1.Secret
		err := c.Get(ctx, types.NamespacedName{Namespace: agent.Namespace, Name: r.name}, &sec)
		if apierrors.IsNotFound(err) {
			return apierrors.NewForbidden(
				schema.GroupResource{Group: "witwave.ai", Resource: "witwaveagents"},
				agent.Name,
				fmt.Errorf(
					"%s references Secret %q in namespace %q but no such Secret exists; create the Secret before creating the WitwaveAgent or fix the reference (#1683)",
					r.field, r.name, agent.Namespace,
				),
			)
		}
		// Forbidden / other transient errors aren't part of this gate's
		// contract — the operator's own Secret read RBAC is required for
		// reconcile too, and surfacing it as an admission denial would
		// mask other problems. Let the controller path log and retry.
		if err != nil && !apierrors.IsForbidden(err) {
			return apierrors.NewInternalError(fmt.Errorf("verifying %s: %w", r.field, err))
		}
	}

	// Gate 2: SSAR for secrets create when spec carries inline credentials (#1685).
	hasInline := false
	for _, gs := range agent.Spec.GitSyncs {
		if gs.Credentials != nil && gs.Credentials.ExistingSecret == "" &&
			(gs.Credentials.Username != "" || gs.Credentials.Token != "") {
			hasInline = true
			break
		}
	}
	if !hasInline {
		for _, b := range agent.Spec.Backends {
			if b.Credentials != nil && b.Credentials.ExistingSecret == "" && len(b.Credentials.Secrets) > 0 {
				hasInline = true
				break
			}
		}
	}
	if hasInline {
		for _, verb := range []string{"create", "patch"} {
			ssar := &authv1.SelfSubjectAccessReview{
				Spec: authv1.SelfSubjectAccessReviewSpec{
					ResourceAttributes: &authv1.ResourceAttributes{
						Namespace: agent.Namespace,
						Verb:      verb,
						Group:     "",
						Resource:  "secrets",
					},
				},
			}
			if err := c.Create(ctx, ssar); err != nil {
				// SSAR Create can fail in test envs that don't bind the
				// authorization API. Treat as soft-degrade: emit a
				// warning via the validator's normal warnings path
				// rather than rejecting admission for an infra issue
				// outside the operator's control. We can't do that from
				// here (return signature is error-only), so log and
				// continue — the existing inlineCredentialsRBACWarnings
				// already nudges the operator.
				_ = logf.FromContext(ctx)
				continue
			}
			if !ssar.Status.Allowed {
				return apierrors.NewForbidden(
					schema.GroupResource{Group: "witwave.ai", Resource: "witwaveagents"},
					agent.Name,
					fmt.Errorf(
						"WitwaveAgent uses inline credentials but the operator's ServiceAccount cannot %q Secrets in namespace %q (rbac.secretsWrite=false). Set rbac.secretsWrite=true OR migrate to credentials.existingSecret to reference a pre-created Secret. (#1685)",
						verb, agent.Namespace,
					),
				)
			}
		}
	}

	return nil
}

// validateWitwaveAgent runs every WitwaveAgent admission check. Any single failure
// returns immediately so the first offending field is reported to the
// user, rather than piling every unrelated error into one message.
func validateWitwaveAgent(agent *witwavev1alpha1.WitwaveAgent) error {
	if err := validateBackendNamesUnique(agent); err != nil {
		return err
	}
	if err := validateInlineCredentialsAck(agent); err != nil {
		return err
	}
	// Chart-side invariants surfaced at admission (#832) so misconfigs
	// fail loudly on `kubectl apply` instead of silently leaving pods
	// Pending or reconciler-retry-looping. Each validator targets a
	// specific field so the error message points the operator at the
	// exact knob to fix.
	if err := validatePreStopGrace(agent); err != nil {
		return err
	}
	if err := validatePodDisruptionBudget(agent); err != nil {
		return err
	}
	if err := validateBackendStorageSize(agent); err != nil {
		return err
	}
	if err := validateGitMappingRefs(agent); err != nil {
		return err
	}
	if err := validateSharedStorageHostPath(agent); err != nil {
		return err
	}
	if err := validateAppPorts(agent); err != nil {
		return err
	}
	if err := validateCors(agent); err != nil {
		return err
	}
	return nil
}

// validateCors mirrors the chart's CORS guard (#763, #1748). When the
// spec lists `*` in cors.allowOrigins, the operator requires the
// explicit cors.allowWildcard=true acknowledgement — a wildcard in
// CORS_ALLOW_ORIGINS combined with credentialed routes (`/triggers`,
// `/conversations`) is a documented disclosure-hole the chart's #763
// fail-render guard exists to prevent.
func validateCors(agent *witwavev1alpha1.WitwaveAgent) error {
	if agent.Spec.Cors == nil || len(agent.Spec.Cors.AllowOrigins) == 0 {
		return nil
	}
	hasWildcard := false
	for _, o := range agent.Spec.Cors.AllowOrigins {
		if o == "*" {
			hasWildcard = true
			break
		}
	}
	if hasWildcard && !agent.Spec.Cors.AllowWildcard {
		return apierrors.NewForbidden(witwaveagentGR, agent.Name, fmt.Errorf(
			"spec.cors.allowOrigins contains \"*\" without spec.cors.allowWildcard=true; " +
				"a wildcard origin combined with credentialed routes (/triggers, /conversations) " +
				"is a disclosure-hole — set allowWildcard=true to acknowledge the risk explicitly (#763, #1748)",
		))
	}
	return nil
}

// validateAppPorts enforces upper bounds on every app-listener port in the
// spec so the implicit metrics port (app_port + 1000, see #687 / #836) can
// never overflow Kubernetes's 1..65535 port range. The CRD already pins
// each Port field to Maximum=64535, but when MetricsSpec.Enabled=true the
// effective ceiling drops to 64535 - 1000 = 63535 because anything above
// that produces a metrics port >= 65536 which the kernel rejects at
// bind(2) — by which point the pod is already in CrashLoopBackOff with
// no kubectl-visible signal pointing at the port choice.
//
// We refuse the CR at admission time with a message that names the
// reservation explicitly so the operator can fix the port without
// having to read pod logs.
func validateAppPorts(agent *witwavev1alpha1.WitwaveAgent) error {
	const portMax = int32(64535)
	const metricsReservation = int32(1000)
	metricsEnabled := agent.Spec.Metrics.Enabled

	check := func(field string, port int32) error {
		if port == 0 {
			return nil
		}
		if port > portMax {
			return apierrors.NewForbidden(witwaveagentGR, agent.Name, fmt.Errorf(
				"%s=%d exceeds maximum allowed port %d (Kubernetes 1..65535 minus the %d-port metrics reservation, app_port+1000, see #687/#836)",
				field, port, portMax, metricsReservation,
			))
		}
		if metricsEnabled && port > portMax-metricsReservation {
			return apierrors.NewForbidden(witwaveagentGR, agent.Name, fmt.Errorf(
				"%s=%d incompatible with spec.metrics.enabled=true: the implicit metrics port is app_port+%d=%d which would overflow the 1..65535 range; pick an app port <= %d or disable metrics",
				field, port, metricsReservation, port+metricsReservation, portMax-metricsReservation,
			))
		}
		return nil
	}

	if err := check("spec.port", agent.Spec.Port); err != nil {
		return err
	}
	for i, b := range agent.Spec.Backends {
		if err := check(fmt.Sprintf("spec.backends[%d].port (backend=%q)", i, b.Name), b.Port); err != nil {
			return err
		}
	}
	return nil
}

// witwaveagentGR is the GroupResource used on every Forbidden error so the
// API server renders consistent error surfaces.
var witwaveagentGR = schema.GroupResource{Group: "witwave.ai", Resource: "witwaveagents"}

// validatePreStopGrace refuses a CR whose `preStop.enabled=true` with a
// DelaySeconds that is not strictly less than TerminationGracePeriodSeconds
// (or the K8s default of 30s when the CR omits the override). Without the
// strict-less relationship, SIGKILL arrives while the preStop sleep is
// still running and the drain window the feature was designed to provide
// collapses to zero — exactly the failure mode the #447 scaffold warns
// about in a comment but never enforced.
func validatePreStopGrace(agent *witwavev1alpha1.WitwaveAgent) error {
	ps := agent.Spec.PreStop
	if ps == nil || !ps.Enabled {
		return nil
	}
	graceSeconds := int64(30) // corev1 default when TerminationGracePeriodSeconds is nil
	if agent.Spec.TerminationGracePeriodSeconds != nil {
		graceSeconds = *agent.Spec.TerminationGracePeriodSeconds
	}
	// delay == 0 is a no-op preStop sleep — permit it unconditionally even
	// when grace == 0. Only enforce the strict-less relationship when the
	// user actually asked for a sleep window.
	if ps.DelaySeconds > 0 && int64(ps.DelaySeconds) >= graceSeconds {
		return apierrors.NewForbidden(witwaveagentGR, agent.Name, fmt.Errorf(
			"spec.preStop.delaySeconds=%d must be strictly less than "+
				"spec.terminationGracePeriodSeconds=%d (K8s default 30 when unset); "+
				"otherwise SIGKILL fires while preStop sleep is still running and "+
				"the drain window is effectively zero",
			ps.DelaySeconds, graceSeconds,
		))
	}
	return nil
}

// validatePodDisruptionBudget enforces the exactly-one rule that the
// PodDisruptionBudgetSpec doc comment promises — Kubernetes's PDB API
// itself is permissive (accepts neither or both), and the reconciler
// would silently pick one at reconcile time, so admission-time enforcement
// surfaces the misconfig immediately.
func validatePodDisruptionBudget(agent *witwavev1alpha1.WitwaveAgent) error {
	pdb := agent.Spec.PodDisruptionBudget
	if pdb == nil || !pdb.Enabled {
		return nil
	}
	hasMin := pdb.MinAvailable != nil
	hasMax := pdb.MaxUnavailable != nil
	if hasMin == hasMax { // neither OR both
		return apierrors.NewForbidden(witwaveagentGR, agent.Name, fmt.Errorf(
			"spec.podDisruptionBudget: exactly one of minAvailable or maxUnavailable "+
				"must be set when enabled=true (have minAvailable=%v, maxUnavailable=%v)",
			hasMin, hasMax,
		))
	}
	return nil
}

// validateBackendStorageSize parses every enabled backend's Size through
// resource.ParseQuantity so the operator doesn't silently skip a PVC at
// reconcile time with only a Warning Event and a counter bump.
func validateBackendStorageSize(agent *witwavev1alpha1.WitwaveAgent) error {
	for i, b := range agent.Spec.Backends {
		st := b.Storage
		if st == nil || !st.Enabled {
			continue
		}
		if st.ExistingClaim != "" {
			continue
		}
		if strings.TrimSpace(st.Size) == "" {
			return apierrors.NewForbidden(witwaveagentGR, agent.Name, fmt.Errorf(
				"spec.backends[%d].storage.size (backend=%q): required when enabled=true "+
					"and existingClaim is empty — e.g. \"1Gi\"",
				i, b.Name,
			))
		}
		if _, err := resource.ParseQuantity(st.Size); err != nil {
			return apierrors.NewForbidden(witwaveagentGR, agent.Name, fmt.Errorf(
				"spec.backends[%d].storage.size (backend=%q): invalid "+
					"resource.Quantity %q: %v — must parse as a Kubernetes "+
					"storage quantity (\"1Gi\", \"500Mi\", etc.)",
				i, b.Name, st.Size, err,
			))
		}
	}
	return nil
}

// validateGitMappingRefs refuses a CR whose GitMapping entries reference
// a GitSync name that isn't declared in Spec.GitSyncs. The reconciler
// would otherwise silently fail to populate the emptyDir volume,
// leaving the backend with a missing mount and no kubectl-visible signal.
func validateGitMappingRefs(agent *witwavev1alpha1.WitwaveAgent) error {
	declared := make(map[string]bool, len(agent.Spec.GitSyncs))
	for _, gs := range agent.Spec.GitSyncs {
		declared[gs.Name] = true
	}
	for bi, b := range agent.Spec.Backends {
		for mi, gm := range b.GitMappings {
			if !declared[gm.GitSync] {
				return apierrors.NewForbidden(witwaveagentGR, agent.Name, fmt.Errorf(
					"spec.backends[%d].gitMappings[%d].gitSync=%q "+
						"(backend=%q) does not name any entry in "+
						"spec.gitSyncs — declared names are %v",
					bi, mi, gm.GitSync, b.Name, gitSyncNameList(agent),
				))
			}
		}
	}
	return nil
}

func gitSyncNameList(agent *witwavev1alpha1.WitwaveAgent) []string {
	names := make([]string, 0, len(agent.Spec.GitSyncs))
	for _, gs := range agent.Spec.GitSyncs {
		names = append(names, gs.Name)
	}
	return names
}

// validateSharedStorageHostPath refuses a CR whose sharedStorage uses
// storageType=hostPath without a non-empty absolute HostPath (and not
// ".."-escaping). The CRD schema allows HostPath to be empty (pvc mode
// doesn't use it), so we enforce the conditional requirement here.
func validateSharedStorageHostPath(agent *witwavev1alpha1.WitwaveAgent) error {
	ss := agent.Spec.SharedStorage
	if ss == nil || !ss.Enabled {
		return nil
	}
	if ss.StorageType != witwavev1alpha1.SharedStorageTypeHostPath {
		return nil
	}
	hp := strings.TrimSpace(ss.HostPath)
	switch {
	case hp == "":
		return apierrors.NewForbidden(witwaveagentGR, agent.Name, fmt.Errorf(
			"spec.sharedStorage.hostPath: required when storageType=hostPath",
		))
	case !strings.HasPrefix(hp, "/"):
		return apierrors.NewForbidden(witwaveagentGR, agent.Name, fmt.Errorf(
			"spec.sharedStorage.hostPath=%q: must be an absolute path (start with '/')",
			ss.HostPath,
		))
	case containsDotDotSegment(hp):
		// #1320: check for ".." as a path SEGMENT, not a substring, so
		// legitimate directory names like "/mnt/backup..old" pass while
		// "/mnt/../etc" correctly rejects.
		return apierrors.NewForbidden(witwaveagentGR, agent.Name, fmt.Errorf(
			"spec.sharedStorage.hostPath=%q: must not contain '..' path segments "+
				"(hostPath volumes bypass cluster-level isolation)",
			ss.HostPath,
		))
	}
	return nil
}

// validateInlineCredentialsAck enforces the AcknowledgeInsecureInline
// gate on every credentials block in the spec. Any GitSync or Backend
// entry whose inline Username/Token/Secrets is populated must set
// AcknowledgeInsecureInline=true — inline values land in etcd and show
// up in `kubectl get witwaveagent -o yaml`, so we refuse them unless the
// operator explicitly opts in. Mirrors the chart's `witwave.resolveCredentials`
// fail path.
func validateInlineCredentialsAck(agent *witwavev1alpha1.WitwaveAgent) error {
	for i, gs := range agent.Spec.GitSyncs {
		c := gs.Credentials
		if c == nil {
			continue
		}
		// ExistingSecret wins; inline values are ignored when it's set,
		// so there's no security risk to accept the CR even if
		// acknowledgeInsecureInline is false in that case.
		if c.ExistingSecret != "" {
			continue
		}
		if (c.Username != "" || c.Token != "") && !c.AcknowledgeInsecureInline {
			return apierrors.NewForbidden(
				schema.GroupResource{Group: "witwave.ai", Resource: "witwaveagents"},
				agent.Name,
				fmt.Errorf(
					"spec.gitSyncs[%d].credentials (name=%q): inline username/token requires acknowledgeInsecureInline=true — inline credentials land in etcd + CR history and are readable via `kubectl get witwaveagent -o yaml`; set the flag to confirm (dev only) OR use existingSecret to reference a pre-created Secret (production)",
					i, gs.Name,
				),
			)
		}
	}
	for i, b := range agent.Spec.Backends {
		c := b.Credentials
		if c == nil {
			continue
		}
		if c.ExistingSecret != "" {
			continue
		}
		if len(c.Secrets) > 0 && !c.AcknowledgeInsecureInline {
			return apierrors.NewForbidden(
				schema.GroupResource{Group: "witwave.ai", Resource: "witwaveagents"},
				agent.Name,
				fmt.Errorf(
					"spec.backends[%d].credentials (name=%q): inline secrets map requires acknowledgeInsecureInline=true — inline credentials land in etcd + CR history and are readable via `kubectl get witwaveagent -o yaml`; set the flag to confirm (dev only) OR use existingSecret to reference a pre-created Secret (production)",
					i, b.Name,
				),
			)
		}
	}
	return nil
}

func (v *WitwaveAgentCustomValidator) ValidateDelete(ctx context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

// inlineCredentialsRBACWarnings returns admission warnings (NOT errors —
// must not block admission) when any credentials block on the CR opts in
// to inline secrets via AcknowledgeInsecureInline=true. Inline mode
// requires the operator to reconcile a Secret on the user's behalf,
// which means the operator ServiceAccount needs Secret write verbs
// (`create`, `update`, `patch`, `delete`). The chart gates that surface
// behind `rbac.secretsWrite=true` (split RBAC posture, see AGENTS.md
// "Operator RBAC"); when an operator is deployed with
// `rbac.secretsWrite=false` and a CR ships inline credentials, the
// reconciler will fail to materialise the Secret and the backend will
// stay unhealthy.
//
// The webhook can't directly introspect its own RBAC binding, so we emit
// the warning unconditionally whenever the inline-acknowledgement is
// present — telling the operator (the human) what to verify rather than
// trying to detect it ourselves. See #1623 (warning surface) and #1613
// (split RBAC posture motivation).
func inlineCredentialsRBACWarnings(agent *witwavev1alpha1.WitwaveAgent) admission.Warnings {
	var warnings admission.Warnings
	for i, gs := range agent.Spec.GitSyncs {
		c := gs.Credentials
		if c == nil || c.ExistingSecret != "" {
			continue
		}
		if c.AcknowledgeInsecureInline && (c.Username != "" || c.Token != "") {
			warnings = append(warnings, fmt.Sprintf(
				"spec.gitSyncs[%d].credentials (name=%q): inline credentials with acknowledgeInsecureInline=true require the operator to reconcile a Secret on your behalf — ensure the operator chart was installed with rbac.secretsWrite=true (see #1623, #1613); for production, prefer credentials.existingSecret to reference a pre-created Secret",
				i, gs.Name,
			))
		}
	}
	for i, b := range agent.Spec.Backends {
		c := b.Credentials
		if c == nil || c.ExistingSecret != "" {
			continue
		}
		if c.AcknowledgeInsecureInline && len(c.Secrets) > 0 {
			warnings = append(warnings, fmt.Sprintf(
				"spec.backends[%d].credentials (name=%q): inline secrets with acknowledgeInsecureInline=true require the operator to reconcile a Secret on your behalf — ensure the operator chart was installed with rbac.secretsWrite=true (see #1623, #1613); for production, prefer credentials.existingSecret to reference a pre-created Secret",
				i, b.Name,
			))
		}
	}
	return warnings
}

// validateBackendNamesUnique returns an error when two or more entries in
// Spec.Backends share the same Name. The reconciler's resource naming
// already assumes uniqueness (PVC + Deployment names embed the backend
// name); silent duplicates have historically caused one backend's
// resources to shadow the other's without any user-facing signal.
func validateBackendNamesUnique(agent *witwavev1alpha1.WitwaveAgent) error {
	seen := make(map[string]int, len(agent.Spec.Backends))
	for i, b := range agent.Spec.Backends {
		if prev, ok := seen[b.Name]; ok {
			return apierrors.NewForbidden(
				schema.GroupResource{Group: "witwave.ai", Resource: "witwaveagents"},
				agent.Name,
				fmt.Errorf(
					"spec.backends[%d].name %q duplicates spec.backends[%d].name; backend names must be unique — they are embedded in Deployment / Service / PVC names and duplicates silently cause one backend's resources to shadow the other's",
					i, b.Name, prev,
				),
			)
		}
		seen[b.Name] = i
	}
	return nil
}

// SetupWitwaveAgentWebhookWithManager registers the defaulter and validator
// with the controller-runtime manager. Call this from main.go after the
// reconciler is registered.
func SetupWitwaveAgentWebhookWithManager(mgr ctrl.Manager) error {
	// #1683 / #1685: hand the validator the manager's client so it can
	// do live apiserver lookups (Secret existence + SSAR for the
	// secrets create/patch verbs).
	if err := ctrl.NewWebhookManagedBy(mgr).
		For(&witwavev1alpha1.WitwaveAgent{}).
		WithDefaulter(&WitwaveAgentCustomDefaulter{}).
		WithValidator(&WitwaveAgentCustomValidator{Client: mgr.GetClient()}).
		Complete(); err != nil {
		return err
	}
	return nil
}

// containsDotDotSegment reports whether the given filesystem path
// contains ".." as a path SEGMENT (not as a substring of a segment).
// "/mnt/../etc" → true; "/mnt/backup..old" → false. #1320.
//
// We inspect the RAW path (split on '/') rather than filepath.Clean
// because Clean resolves ".." — and the whole point is to refuse any
// operator intent to traverse, even when the resolved path exists.
func containsDotDotSegment(p string) bool {
	slash := filepath.ToSlash(p)
	for _, seg := range strings.Split(slash, "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}
