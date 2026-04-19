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

// Package controller — domain Prometheus metrics for the NyxAgent
// reconciler (#471). controller-runtime already exports the standard
// reconcile / workqueue / client-go counters on the manager's metrics
// endpoint; these are added on top to surface NyxAgent-specific signals
// (phase transitions, PVC build failures, dashboard adoption) so dashboards
// don't have to infer them from generic counters.
package controller

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	// nyxagentPhaseTransitionsTotal counts every observed transition between
	// status.phase values (Pending, Ready, Degraded, Error). The empty→Pending
	// bootstrap transition is intentionally omitted (matches the Event
	// emitted in recordPhaseTransitionEvent).
	nyxagentPhaseTransitionsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "nyxagent_phase_transitions_total",
			Help: "Total NyxAgent status.phase transitions, labelled by source and target phase.",
		},
		[]string{"from", "to"},
	)

	// nyxagentPVCBuildErrorsTotal counts backend PVC entries that the
	// reconciler refused to apply because their spec was unparseable
	// (e.g. invalid storage.size). One increment per skipped backend per
	// reconcile pass — the operator continues with the rest of the
	// agent's resources, so this metric tracks visibility of silent skips.
	nyxagentPVCBuildErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "nyxagent_pvc_build_errors_total",
			Help: "Total backend PVC build failures (e.g. invalid storage.size), labelled by backend name.",
		},
		[]string{"backend"},
	)

	// nyxagentDashboardEnabled reports whether each NyxAgent has the
	// dashboard feature opted in. Following the kube_state_metrics
	// convention (gauge per CR, sum-aggregable in PromQL) instead of a
	// 2-bucket {enabled=true|false} counter, which doesn't compose well
	// with dashboards.
	nyxagentDashboardEnabled = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "nyxagent_dashboard_enabled",
			Help: "1 when this NyxAgent has spec.dashboard.enabled=true, 0 otherwise. Sum across instances for cluster total.",
		},
		[]string{"namespace", "name"},
	)

	// nyxagentTeardownStepErrorsTotal counts individual resource-kind
	// delete failures inside teardownDisabledAgent (#754). Rather than
	// short-circuiting on the first kind that errors, the teardown
	// accumulates all failures via errors.Join; each increment here
	// records one (kind, reason) pair so a stuck CR's root cause is
	// visible without grepping reconcile logs.  ``reason`` is one of
	// {"get","list","delete","probe"} — coarse enough to avoid label
	// cardinality blowup, specific enough to distinguish a failing
	// apiserver probe from a delete that was rejected outright.
	nyxagentTeardownStepErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "nyxagent_teardown_step_errors_total",
			Help: "Total teardown step errors on the spec.enabled=false / delete path, labelled by resource kind and reason.",
		},
		[]string{"kind", "reason"},
	)

	// nyxpromptBindingOutcomesTotal counts individual (NyxPrompt, NyxAgent)
	// binding outcomes per reconcile pass (#837). ``outcome`` is one of:
	//   - "ready"          — ConfigMap built + applied, binding Ready=true
	//   - "agent_missing"  — target NyxAgent not found; will retry on enqueue
	//   - "build_error"    — buildNyxPromptConfigMap failed
	//   - "owner_error"    — controllerutil.SetControllerReference failed
	//   - "apply_error"    — applyNyxPromptConfigMap failed
	//
	// Cardinality safety (#1070): previously this counter carried an
	// "agent" label taken directly from spec.agentRefs[].name. Since
	// those names include referenced-but-nonexistent agents, a malformed
	// CR with thousands of unique bogus refs could explode the operator
	// metrics endpoint. The agent label has been dropped — operators
	// who need per-agent attribution should consult the
	// NyxPrompt.Status.Bindings list directly.
	nyxpromptBindingOutcomesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "nyxprompt_binding_outcomes_total",
			Help: "Total NyxPrompt→NyxAgent binding attempts, labelled by outcome.",
		},
		[]string{"outcome"},
	)

	// nyxpromptReadyCount mirrors NyxPrompt.Status.ReadyCount per CR so
	// dashboards can alert on "prompt has been partial for > N minutes"
	// without scraping the CR subresource (#837).
	nyxpromptReadyCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "nyxprompt_ready_count",
			Help: "Number of NyxAgent refs on this NyxPrompt whose Binding.Ready=true.",
		},
		[]string{"namespace", "name"},
	)

	// nyxpromptDesiredCount reports len(spec.agentRefs); paired with
	// nyxpromptReadyCount to compute "fully bound" via
	// sum(ready)/sum(desired) in PromQL.
	nyxpromptDesiredCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "nyxprompt_desired_count",
			Help: "Number of NyxAgent refs declared in the NyxPrompt spec.",
		},
		[]string{"namespace", "name"},
	)

	// nyxpromptStatusPatchConflictsTotal distinguishes benign 409s on
	// NyxPrompt status subresource writes from real apiserver failures
	// (#950). Previously conflicts were joined into the generic
	// reconcileErrs chain so alert rules could not separate contention
	// from a genuine apiserver outage.
	// Declared as a CounterVec with (namespace, name) labels so dashboards
	// can attribute sustained contention to a specific NyxPrompt — this
	// matches README.md (#1015). Previously declared as a label-less
	// Counter, which silently dropped the labels documented in the metrics
	// table and quietly broke every PromQL filter built against them.
	nyxpromptStatusPatchConflictsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "nyxprompt_status_patch_conflicts_total",
			Help: "Total 409 conflicts encountered on NyxPrompt status subresource writes; retried inline by patchStatusWithConflictRetry.",
		},
		[]string{"namespace", "name"},
	)

	// NyxPromptWebhookIndexFallbackTotal counts every time the NyxPrompt
	// admission webhook's scoped-by-index heartbeat-singleton check had
	// to fall through to the O(N) full-namespace scan because the field
	// indexer was unavailable (#1069). Unit-test call sites that skip
	// manager bootstrap legitimately trip this; a sudden rate spike in
	// production is an operational signal that the indexer has been
	// dropped from the manager start-up path.
	NyxPromptWebhookIndexFallbackTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "nyxprompt_webhook_index_fallback_total",
			Help: "Total NyxPrompt admission-webhook heartbeat-singleton checks that fell back to the full-namespace scan because the field index was missing.",
		},
	)

	// NyxAgentLeader reports which operator replica currently holds the
	// leader-election lease (#1115). Set to 1 on the pod that has been
	// elected leader; the other replicas never set the gauge so their
	// value stays absent (not 0) and PromQL ``sum(nyxagent_leader) == 0``
	// cleanly alerts "no leader for > N seconds". controller-runtime
	// already emits leader_election_master_status via client-go, but it
	// doesn't carry the pod label operators need to attribute handoffs
	// during rollouts.
	NyxAgentLeader = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "nyxagent_leader",
			Help: "1 when this operator pod currently holds the NyxAgent leader-election lease, absent otherwise.",
		},
		[]string{"pod"},
	)

	// NyxAgentCredentialRotationsTotal counts observed changes to the
	// credential-Secret checksum stamped on each agent's pod template
	// (#1114). One increment per (namespace, name) per detected rotation:
	// the Secret watch enqueues the owning agent, the reconciler
	// recomputes the checksum against the referenced Secrets' current
	// ResourceVersion, and — when the checksum actually differs — the
	// annotation update triggers a rolling restart so the new token
	// loads. Operators can alert on sustained rotation rates (indicating
	// a flapping credential source) or on a suspicious absence of
	// rotations during a scheduled refresh window.
	NyxAgentCredentialRotationsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "nyxagent_credential_rotations_total",
			Help: "Total NyxAgent credential-Secret checksum rotations observed by the reconciler, labelled by namespace and name.",
		},
		[]string{"namespace", "name"},
	)
)

func init() {
	metrics.Registry.MustRegister(
		nyxagentPhaseTransitionsTotal,
		nyxagentPVCBuildErrorsTotal,
		nyxagentDashboardEnabled,
		nyxagentTeardownStepErrorsTotal,
		nyxpromptBindingOutcomesTotal,
		nyxpromptReadyCount,
		nyxpromptDesiredCount,
		nyxpromptStatusPatchConflictsTotal,
		NyxPromptWebhookIndexFallbackTotal,
		NyxAgentLeader,
		NyxAgentCredentialRotationsTotal,
	)
}
