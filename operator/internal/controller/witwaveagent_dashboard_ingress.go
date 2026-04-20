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

// dashboard_ingress.go implements the operator counterpart to the chart's
// `dashboard.ingress` block (#831). The fail-closed posture is modelled
// end-to-end:
//
//   1. Schema layer (DashboardAuthSpec.Mode enum): "basic" | "none". Any
//      other value is rejected by the apiserver before it reaches the
//      controller.
//
//   2. authReconcile flag wiring (scaffold; documented below):
//        - Ingress == nil OR Ingress.Enabled == false  -> no-op
//        - Ingress.Auth == nil OR Ingress.Auth.Mode == "" -> SKIP render,
//          emit a Warning event "DashboardIngressAuthRequired" so the
//          user sees why the Ingress didn't materialise. This is the
//          direct analogue of the chart's fail-render (#528).
//        - Ingress.Auth.Mode == "none" -> render unauthenticated Ingress,
//          emit a Warning event "DashboardIngressUnauthenticated" on
//          every reconcile so the opt-out stays visible in kubectl
//          describe / dashboards.
//        - Ingress.Auth.Mode == "basic" -> (scaffold) look up the
//          basic-auth Secret named by BasicAuthSecretName; render the
//          Ingress with the nginx.ingress.kubernetes.io/auth-type +
//          auth-secret annotations. Full cross-controller wiring (Contour,
//          Traefik, HAProxy) is follow-up work.
//
// Only the schema + fail-closed gating are shipped in this scaffold. The
// full Ingress + Secret renderer is a follow-up so the CRD surface can
// settle before the controller grows a per-ingress-class adapter matrix.

import (
	"context"
	"encoding/json"
	"fmt"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	witwavev1alpha1 "github.com/witwave-ai/witwave-operator/api/v1alpha1"
)

// dashboardIngressLastEventStateAnnotation tracks the last
// DashboardIngressAuthStatus for which the reconciler emitted an Event
// (#1180). Only transitions emit new Events, so steady-state
// `auth.mode=none` no longer floods the namespace's Event stream every
// reconcile pass.
const dashboardIngressLastEventStateAnnotation = "witwave.ai/dashboard-ingress-last-event-state"

// DashboardIngressAuthStatus describes the fail-closed decision the
// reconciler made this pass. Exported so downstream status-writers (and
// tests) can assert on the distinct states without re-parsing log output.
type DashboardIngressAuthStatus string

const (
	DashboardIngressAuthStatusDisabled        DashboardIngressAuthStatus = "disabled"
	DashboardIngressAuthStatusMissingAuth     DashboardIngressAuthStatus = "missing-auth"
	DashboardIngressAuthStatusUnauthenticated DashboardIngressAuthStatus = "unauthenticated"
	DashboardIngressAuthStatusBasic           DashboardIngressAuthStatus = "basic"
)

// EvaluateDashboardIngressAuth returns the fail-closed decision for a
// WitwaveAgent's dashboard ingress configuration. Extracted as a pure helper
// so unit tests can cover every branch without standing up a fake client.
func EvaluateDashboardIngressAuth(agent *witwavev1alpha1.WitwaveAgent) DashboardIngressAuthStatus {
	if agent == nil || agent.Spec.Dashboard == nil {
		return DashboardIngressAuthStatusDisabled
	}
	ing := agent.Spec.Dashboard.Ingress
	if ing == nil || !ing.Enabled {
		return DashboardIngressAuthStatusDisabled
	}
	if ing.Auth == nil || ing.Auth.Mode == "" {
		return DashboardIngressAuthStatusMissingAuth
	}
	switch ing.Auth.Mode {
	case "none":
		return DashboardIngressAuthStatusUnauthenticated
	case "basic":
		return DashboardIngressAuthStatusBasic
	}
	// Any unknown Mode should have been rejected by the CRD enum
	// validation — treat as fail-closed to be safe.
	return DashboardIngressAuthStatusMissingAuth
}

// reconcileDashboardIngress is the scaffold entrypoint wired into
// WitwaveAgentReconciler.Reconcile. The actual Ingress + Secret render is a
// follow-up; this implementation emits the fail-closed signal so the
// feature is observable from day one.
func (r *WitwaveAgentReconciler) reconcileDashboardIngress(ctx context.Context, agent *witwavev1alpha1.WitwaveAgent) error {
	log := logf.FromContext(ctx).WithValues("agent", agent.Name, "namespace", agent.Namespace)
	state := EvaluateDashboardIngressAuth(agent)
	switch state {
	case DashboardIngressAuthStatusDisabled:
		// No-op. Dashboard ingress was not requested or the Enabled
		// toggle is off. Clear any prior annotation so a future
		// Enabled=true → auth.mode=none flip emits an Event again.
		return r.updateDashboardIngressLastEventState(ctx, agent, string(state))
	case DashboardIngressAuthStatusMissingAuth:
		// Fail-closed mirror of chart #528. Log on every reconcile,
		// but only emit an Event on a state transition (#1180) so a
		// steady-state missing-auth configuration doesn't flood the
		// namespace Event stream.
		log.Info("skipping dashboard Ingress render: spec.dashboard.ingress.auth.mode must be set (fail-closed)",
			"component", "dashboard-ingress")
		if r.shouldEmitDashboardIngressEvent(agent, state) && r.Recorder != nil {
			r.Recorder.Event(agent, "Warning", "DashboardIngressAuthRequired",
				"Dashboard ingress skipped: set spec.dashboard.ingress.auth.mode to 'basic' or 'none' to proceed (fail-closed, see #528/#831)")
		}
		return r.updateDashboardIngressLastEventState(ctx, agent, string(state))
	case DashboardIngressAuthStatusUnauthenticated:
		log.Info("rendering dashboard Ingress WITHOUT auth (spec.dashboard.ingress.auth.mode=none)",
			"component", "dashboard-ingress")
		if r.shouldEmitDashboardIngressEvent(agent, state) && r.Recorder != nil {
			r.Recorder.Event(agent, "Warning", "DashboardIngressUnauthenticated",
				"Dashboard ingress rendered without auth (auth.mode=none) — conversation history is reachable via the Ingress host")
		}
		// Full Ingress render is follow-up scaffolding.
		return r.updateDashboardIngressLastEventState(ctx, agent, string(state))
	case DashboardIngressAuthStatusBasic:
		log.Info("rendering dashboard Ingress with basic-auth (scaffold: full render is follow-up)",
			"component", "dashboard-ingress")
		return r.updateDashboardIngressLastEventState(ctx, agent, string(state))
	}
	return nil
}

// shouldEmitDashboardIngressEvent reports whether the current
// DashboardIngressAuthStatus differs from the last value we stamped on
// the agent's annotations. Callers gate their `Recorder.Event` calls
// on this so reconciler-churn doesn't spam duplicate Events.
func (r *WitwaveAgentReconciler) shouldEmitDashboardIngressEvent(agent *witwavev1alpha1.WitwaveAgent, state DashboardIngressAuthStatus) bool {
	prev := ""
	if agent.Annotations != nil {
		prev = agent.Annotations[dashboardIngressLastEventStateAnnotation]
	}
	return prev != string(state)
}

// updateDashboardIngressLastEventState writes the current state value
// into the annotation via an SSA-safe JSON-merge patch (#1180). The
// patch is scoped to metadata.annotations so it never clobbers
// concurrent writes to unrelated fields. Idempotent: if the annotation
// already holds `value` the patch is a no-op skip.
func (r *WitwaveAgentReconciler) updateDashboardIngressLastEventState(ctx context.Context, agent *witwavev1alpha1.WitwaveAgent, value string) error {
	if agent.Annotations != nil && agent.Annotations[dashboardIngressLastEventStateAnnotation] == value {
		return nil
	}
	patch := map[string]interface{}{
		"metadata": map[string]interface{}{
			"annotations": map[string]string{
				dashboardIngressLastEventStateAnnotation: value,
			},
		},
	}
	raw, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("marshal dashboard-ingress-last-event-state patch: %w", err)
	}
	if err := r.Patch(ctx, agent, client.RawPatch(types.MergePatchType, raw)); err != nil {
		return fmt.Errorf("patch dashboard-ingress-last-event-state annotation: %w", err)
	}
	// Mirror the write back into the in-memory copy so downstream
	// reconcile steps see the same annotation value we just landed.
	if agent.Annotations == nil {
		agent.Annotations = map[string]string{}
	}
	agent.Annotations[dashboardIngressLastEventStateAnnotation] = value
	return nil
}
