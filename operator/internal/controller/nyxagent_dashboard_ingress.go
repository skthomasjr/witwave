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

	logf "sigs.k8s.io/controller-runtime/pkg/log"

	nyxv1alpha1 "github.com/nyx-ai/nyx-operator/api/v1alpha1"
)

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
// NyxAgent's dashboard ingress configuration. Extracted as a pure helper
// so unit tests can cover every branch without standing up a fake client.
func EvaluateDashboardIngressAuth(agent *nyxv1alpha1.NyxAgent) DashboardIngressAuthStatus {
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
// NyxAgentReconciler.Reconcile. The actual Ingress + Secret render is a
// follow-up; this implementation emits the fail-closed signal so the
// feature is observable from day one.
func (r *NyxAgentReconciler) reconcileDashboardIngress(ctx context.Context, agent *nyxv1alpha1.NyxAgent) error {
	log := logf.FromContext(ctx).WithValues("agent", agent.Name, "namespace", agent.Namespace)
	switch EvaluateDashboardIngressAuth(agent) {
	case DashboardIngressAuthStatusDisabled:
		// No-op. Dashboard ingress was not requested or the Enabled
		// toggle is off.
		return nil
	case DashboardIngressAuthStatusMissingAuth:
		// Fail-closed mirror of chart #528. Log + event so the user
		// sees *why* the Ingress didn't materialise.
		log.Info("skipping dashboard Ingress render: spec.dashboard.ingress.auth.mode must be set (fail-closed)",
			"component", "dashboard-ingress")
		if r.Recorder != nil {
			r.Recorder.Event(agent, "Warning", "DashboardIngressAuthRequired",
				"Dashboard ingress skipped: set spec.dashboard.ingress.auth.mode to 'basic' or 'none' to proceed (fail-closed, see #528/#831)")
		}
		return nil
	case DashboardIngressAuthStatusUnauthenticated:
		log.Info("rendering dashboard Ingress WITHOUT auth (spec.dashboard.ingress.auth.mode=none)",
			"component", "dashboard-ingress")
		if r.Recorder != nil {
			r.Recorder.Event(agent, "Warning", "DashboardIngressUnauthenticated",
				"Dashboard ingress rendered without auth (auth.mode=none) — conversation history is reachable via the Ingress host")
		}
		// Full Ingress render is follow-up scaffolding.
		return nil
	case DashboardIngressAuthStatusBasic:
		log.Info("rendering dashboard Ingress with basic-auth (scaffold: full render is follow-up)",
			"component", "dashboard-ingress")
		return nil
	}
	return nil
}
