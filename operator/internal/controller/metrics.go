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

// Package controller — domain Prometheus metrics for the WitwaveAgent
// reconciler (#471). controller-runtime already exports the standard
// reconcile / workqueue / client-go counters on the manager's metrics
// endpoint; these are added on top to surface WitwaveAgent-specific signals
// (phase transitions, PVC build failures, dashboard adoption) so dashboards
// don't have to infer them from generic counters.
package controller

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	// witwaveagentPhaseTransitionsTotal counts every observed transition between
	// status.phase values (Pending, Ready, Degraded, Error). The empty→Pending
	// bootstrap transition is intentionally omitted (matches the Event
	// emitted in recordPhaseTransitionEvent).
	witwaveagentPhaseTransitionsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "witwaveagent_phase_transitions_total",
			Help: "Total WitwaveAgent status.phase transitions, labelled by source and target phase.",
		},
		[]string{"from", "to"},
	)

	// witwaveagentPVCBuildErrorsTotal counts backend PVC entries that the
	// reconciler refused to apply because their spec was unparseable
	// (e.g. invalid storage.size). One increment per skipped backend per
	// reconcile pass — the operator continues with the rest of the
	// agent's resources, so this metric tracks visibility of silent skips.
	witwaveagentPVCBuildErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "witwaveagent_pvc_build_errors_total",
			Help: "Total backend PVC build failures (e.g. invalid storage.size), labelled by backend name.",
		},
		[]string{"backend"},
	)

	// witwaveagentDashboardEnabled reports whether each WitwaveAgent has the
	// dashboard feature opted in. Following the kube_state_metrics
	// convention (gauge per CR, sum-aggregable in PromQL) instead of a
	// 2-bucket {enabled=true|false} counter, which doesn't compose well
	// with dashboards.
	witwaveagentDashboardEnabled = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "witwaveagent_dashboard_enabled",
			Help: "1 when this WitwaveAgent has spec.dashboard.enabled=true, 0 otherwise. Sum across instances for cluster total.",
		},
		[]string{"namespace", "name"},
	)

	// witwaveagentTeardownStepErrorsTotal counts individual resource-kind
	// delete failures inside teardownDisabledAgent (#754). Rather than
	// short-circuiting on the first kind that errors, the teardown
	// accumulates all failures via errors.Join; each increment here
	// records one (kind, reason) pair so a stuck CR's root cause is
	// visible without grepping reconcile logs.  ``reason`` is one of
	// {"get","list","delete","probe"} — coarse enough to avoid label
	// cardinality blowup, specific enough to distinguish a failing
	// apiserver probe from a delete that was rejected outright.
	witwaveagentTeardownStepErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "witwaveagent_teardown_step_errors_total",
			Help: "Total teardown step errors on the spec.enabled=false / delete path, labelled by resource kind and reason.",
		},
		[]string{"kind", "reason"},
	)

	// witwavepromptBindingOutcomesTotal counts individual (WitwavePrompt, WitwaveAgent)
	// binding outcomes per reconcile pass (#837). ``outcome`` is one of:
	//   - "ready"          — ConfigMap built + applied, binding Ready=true
	//   - "agent_missing"  — target WitwaveAgent not found; will retry on enqueue
	//   - "build_error"    — buildWitwavePromptConfigMap failed
	//   - "owner_error"    — controllerutil.SetControllerReference failed
	//   - "apply_error"    — applyWitwavePromptConfigMap failed
	//
	// Cardinality safety (#1070): previously this counter carried an
	// "agent" label taken directly from spec.agentRefs[].name. Since
	// those names include referenced-but-nonexistent agents, a malformed
	// CR with thousands of unique bogus refs could explode the operator
	// metrics endpoint. The agent label has been dropped — operators
	// who need per-agent attribution should consult the
	// WitwavePrompt.Status.Bindings list directly.
	witwavepromptBindingOutcomesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "witwaveprompt_binding_outcomes_total",
			Help: "Total WitwavePrompt→WitwaveAgent binding attempts, labelled by outcome.",
		},
		[]string{"outcome"},
	)

	// witwavepromptReadyAgents mirrors WitwavePrompt.Status.ReadyCount per CR so
	// dashboards can alert on "prompt has been partial for > N minutes"
	// without scraping the CR subresource (#837). Renamed from
	// witwaveprompt_ready_count to witwaveprompt_ready_agents (#1299): the gauge
	// reports a count of agents in the ready state, and the _count suffix
	// collides with Prometheus convention for counter-derived series.
	witwavepromptReadyAgents = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "witwaveprompt_ready_agents",
			Help: "Number of WitwaveAgent refs on this WitwavePrompt whose Binding.Ready=true.",
		},
		[]string{"namespace", "name"},
	)

	// witwavepromptDesiredAgents reports len(spec.agentRefs); paired with
	// witwavepromptReadyAgents to compute "fully bound" via
	// sum(ready)/sum(desired) in PromQL. Renamed from
	// witwaveprompt_desired_count (#1299) for the same reason as
	// witwaveprompt_ready_agents above.
	witwavepromptDesiredAgents = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "witwaveprompt_desired_agents",
			Help: "Number of WitwaveAgent refs declared in the WitwavePrompt spec.",
		},
		[]string{"namespace", "name"},
	)

	// witwavepromptStatusPatchConflictsTotal distinguishes benign 409s on
	// WitwavePrompt status subresource writes from real apiserver failures
	// (#950). Previously conflicts were joined into the generic
	// reconcileErrs chain so alert rules could not separate contention
	// from a genuine apiserver outage.
	// Declared as a CounterVec with (namespace, name) labels so dashboards
	// can attribute sustained contention to a specific WitwavePrompt — this
	// matches README.md (#1015). Previously declared as a label-less
	// Counter, which silently dropped the labels documented in the metrics
	// table and quietly broke every PromQL filter built against them.
	witwavepromptStatusPatchConflictsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "witwaveprompt_status_patch_conflicts_total",
			Help: "Total 409 conflicts encountered on WitwavePrompt status subresource writes; retried inline by patchStatusWithConflictRetry.",
		},
		[]string{"namespace", "name"},
	)

	// WitwavePromptWebhookIndexFallbackTotal counts every time the WitwavePrompt
	// admission webhook's scoped-by-index heartbeat-singleton check had
	// to fall through to the O(N) full-namespace scan because the field
	// indexer was unavailable (#1069). Unit-test call sites that skip
	// manager bootstrap legitimately trip this; a sudden rate spike in
	// production is an operational signal that the indexer has been
	// dropped from the manager start-up path.
	WitwavePromptWebhookIndexFallbackTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "witwaveprompt_webhook_index_fallback_total",
			Help: "Total WitwavePrompt admission-webhook heartbeat-singleton checks that fell back to the full-namespace scan because the field index was missing.",
		},
	)

	// WitwaveAgentLeader reports which operator replica currently holds the
	// leader-election lease (#1115). Set to 1 on the pod that has been
	// elected leader; the other replicas never set the gauge so their
	// value stays absent (not 0) and PromQL ``sum(witwaveagent_leader) == 0``
	// cleanly alerts "no leader for > N seconds". controller-runtime
	// already emits leader_election_master_status via client-go, but it
	// doesn't carry the pod label operators need to attribute handoffs
	// during rollouts.
	WitwaveAgentLeader = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "witwaveagent_leader",
			Help: "1 when this operator pod currently holds the WitwaveAgent leader-election lease, absent otherwise.",
		},
		[]string{"pod"},
	)

	// witwaveagentManifestOwnerRefSkippedNoUIDTotal counts team-manifest
	// OwnerReference entries dropped because the member WitwaveAgent had an
	// empty UID when buildManifestOwnerRefs ran (#1016). APIReader is
	// expected to return fully-persisted objects, so a non-zero rate
	// signals a narrow race where a newly-created WitwaveAgent contributes
	// to the manifest body but not to the CM's OwnerReferences — which
	// can confuse GC timing if the race persists across reconciles.
	// Labelled by the agent namespace so operators can attribute spikes
	// to a specific tenant without exploding cardinality on Name.
	witwaveagentManifestOwnerRefSkippedNoUIDTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "witwaveagent_manifest_owner_ref_skipped_no_uid_total",
			Help: "Total team-manifest OwnerReference entries skipped because the member WitwaveAgent had no UID yet; expected to be 0 in steady state.",
		},
		[]string{"namespace"},
	)

	// WitwaveAgentCredentialRotationsTotal counts observed changes to the
	// credential-Secret checksum stamped on each agent's pod template
	// (#1114). One increment per (namespace, name) per detected rotation:
	// the Secret watch enqueues the owning agent, the reconciler
	// recomputes the checksum against the referenced Secrets' current
	// ResourceVersion, and — when the checksum actually differs — the
	// annotation update triggers a rolling restart so the new token
	// loads. Operators can alert on sustained rotation rates (indicating
	// a flapping credential source) or on a suspicious absence of
	// rotations during a scheduled refresh window.
	WitwaveAgentCredentialRotationsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "witwaveagent_credential_rotations_total",
			Help: "Total WitwaveAgent credential-Secret checksum rotations observed by the reconciler, labelled by namespace and name.",
		},
		[]string{"namespace", "name"},
	)

	// WitwaveAgentCredentialWatchListErrorsTotal counts List failures in the
	// Secret-watch mapper (#1170). When the primary namespace-scoped List
	// fails, the mapper falls back to a best-effort retry so one missed
	// list doesn't permanently desync the reconciler from a rotated
	// Secret. A non-zero rate here points at APIServer pressure or a
	// per-namespace RBAC regression rather than a controller bug.
	WitwaveAgentCredentialWatchListErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "witwaveagent_credential_watch_list_errors_total",
			Help: "Total List failures in the credentials Secret-watch mapper; the mapper retries once before falling back to an empty enqueue set.",
		},
		[]string{"namespace"},
	)

	// workspaceReconcileTotal counts every Workspace reconcile pass by
	// outcome. ``outcome`` is one of:
	//   - "success"        — the reconcile completed cleanly
	//   - "error"          — at least one sub-step returned an error
	//   - "delete_blocked" — the deletion path refused to clear the
	//     finalizer because Status.BoundAgents is non-empty
	//   - "deleted"        — the Workspace was successfully deleted
	workspaceReconcileTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "witwaveworkspace_reconcile_total",
			Help: "Total Workspace reconcile passes, labelled by outcome.",
		},
		[]string{"outcome"},
	)

	// workspaceVolumesProvisioned reports the count of Spec.Volumes
	// entries the operator has provisioned per Workspace. Mirrors
	// kube_state_metrics' gauge-per-CR convention so dashboards can
	// sum across instances for cluster-wide totals.
	workspaceVolumesProvisioned = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "witwaveworkspace_volumes_provisioned",
			Help: "Number of PVCs provisioned for this Workspace's Spec.Volumes entries.",
		},
		[]string{"namespace", "name"},
	)

	// workspaceBoundAgents reports the cardinality of
	// Status.BoundAgents per Workspace. Operators can alert on
	// "Workspace has been bound for > N minutes without an agent" or
	// "agent count regressed" without scraping the CR subresource.
	workspaceBoundAgents = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "witwaveworkspace_bound_agents",
			Help: "Number of WitwaveAgents currently referencing this Workspace via Spec.WorkspaceRefs.",
		},
		[]string{"namespace", "name"},
	)

	// WitwaveAgentLeaderElectionRenewFailuresTotal counts renewal-deadline
	// misses by the operator's leader-election machinery (#1475). A non-zero
	// rate on a cluster with 3+ operator replicas indicates a slow
	// apiserver (control-plane upgrade, networking hiccup, etcd pressure)
	// false-positive-demoting the active leader — the newly-promoted
	// leader then re-reconciles the world, producing a stampede. The
	// metric is populated by a lease-renewal error hook wired into the
	// manager's leader-election config; a jump in the counter paired
	// with a WitwaveAgent reconcile-rate spike is the canonical signal.
	WitwaveAgentLeaderElectionRenewFailuresTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "witwaveagent_leader_election_renew_failures_total",
			Help: "Total leader-election renew-deadline misses (#1475). Each miss demotes the active leader; a second replica promotes on the next RetryPeriod. Non-zero rate correlates with reconcile stampedes — consider widening --leader-election-lease-duration on slow-apiserver clusters.",
		},
	)
)

func init() {
	metrics.Registry.MustRegister(
		witwaveagentPhaseTransitionsTotal,
		witwaveagentPVCBuildErrorsTotal,
		witwaveagentDashboardEnabled,
		witwaveagentTeardownStepErrorsTotal,
		witwavepromptBindingOutcomesTotal,
		witwavepromptReadyAgents,
		witwavepromptDesiredAgents,
		witwavepromptStatusPatchConflictsTotal,
		WitwavePromptWebhookIndexFallbackTotal,
		WitwaveAgentLeader,
		WitwaveAgentCredentialRotationsTotal,
		WitwaveAgentCredentialWatchListErrorsTotal,
		witwaveagentManifestOwnerRefSkippedNoUIDTotal,
		WitwaveAgentLeaderElectionRenewFailuresTotal,
		workspaceReconcileTotal,
		workspaceVolumesProvisioned,
		workspaceBoundAgents,
	)
}
