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

// witwaveagent_prometheusrule.go reconciles a per-WitwaveAgent
// monitoring.coreos.com/v1 PrometheusRule (#1746). The shipped alerts
// mirror charts/witwave/templates/prometheusrule.yaml with the chart's
// default thresholds, so an operator-only install gets the same
// pageable surface a chart install does. Per-alert toggles are
// intentionally NOT modelled — Phase 11 narrowed the scope to a single
// `Spec.PrometheusRule.Enabled` flag; operators who want a different
// posture should hand-author their own PrometheusRule and leave this
// flag off.

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	witwavev1alpha1 "github.com/witwave-ai/witwave-operator/api/v1alpha1"
)

// prometheusRuleGVK is the monitoring.coreos.com/v1 PrometheusRule
// GroupVersionKind used by the Prometheus Operator (#1746). Stamped
// here, the unstructured client doesn't depend on prometheus-operator
// Go types so the operator builds without that module.
var prometheusRuleGVK = schema.GroupVersionKind{
	Group:   "monitoring.coreos.com",
	Version: "v1",
	Kind:    "PrometheusRule",
}

// prometheusRuleEnabled reports whether the agent opted in to
// PrometheusRule reconciliation. Reconciliation also requires the CRD
// presence probe to succeed.
func prometheusRuleEnabled(agent *witwavev1alpha1.WitwaveAgent) bool {
	return agent.Spec.PrometheusRule != nil && agent.Spec.PrometheusRule.Enabled
}

// prometheusRuleCRDPresent probes whether the cluster has the
// monitoring.coreos.com/v1 PrometheusRule REST mapping registered. Same
// pattern as serviceMonitorCRDPresent / podMonitorCRDPresent.
func (r *WitwaveAgentReconciler) prometheusRuleCRDPresent(ctx context.Context) (bool, error) {
	_ = ctx
	const cacheKey = "monitoring.coreos.com/v1/PrometheusRule"
	if e, ok := getCachedCRDProbe(cacheKey); ok {
		return e.present, nil
	}
	mapper := r.Client.RESTMapper()
	if mapper == nil {
		return false, nil
	}
	_, err := mapper.RESTMapping(prometheusRuleGVK.GroupKind(), prometheusRuleGVK.Version)
	if err != nil {
		if meta.IsNoMatchError(err) {
			setCachedCRDProbe(cacheKey, false)
			return false, nil
		}
		return false, err
	}
	setCachedCRDProbe(cacheKey, true)
	return true, nil
}

// reconcilePrometheusRule creates / updates / deletes a per-agent
// PrometheusRule (#1746). Same gating shape as ServiceMonitor /
// PodMonitor reconcilers: opt-in via spec, CRD must be installed, owned
// objects only.
func (r *WitwaveAgentReconciler) reconcilePrometheusRule(ctx context.Context, agent *witwavev1alpha1.WitwaveAgent) error {
	log := logf.FromContext(ctx)
	const cacheKey = "monitoring.coreos.com/v1/PrometheusRule"

	present, err := r.prometheusRuleCRDPresent(ctx)
	if err != nil {
		return fmt.Errorf("probe PrometheusRule CRD: %w", err)
	}
	if !present {
		if prometheusRuleEnabled(agent) {
			log.V(1).Info("PrometheusRule CRD not installed — skipping PrometheusRule reconcile",
				"group", prometheusRuleGVK.Group, "version", prometheusRuleGVK.Version)
		}
		return nil
	}

	key := client.ObjectKey{Namespace: agent.Namespace, Name: agent.Name + "-witwave"}
	wantCreate := prometheusRuleEnabled(agent)

	if !wantCreate {
		existing := &unstructured.Unstructured{}
		existing.SetGroupVersionKind(prometheusRuleGVK)
		if err := r.Get(ctx, key, existing); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			if _, skip := handleDownstreamNoMatch(cacheKey, err); skip {
				return nil
			}
			return fmt.Errorf("get PrometheusRule for delete: %w", err)
		}
		if !metav1.IsControlledBy(existing, agent) {
			return nil
		}
		if err := r.Delete(ctx, existing); err != nil && !apierrors.IsNotFound(err) {
			if _, skip := handleDownstreamNoMatch(cacheKey, err); skip {
				return nil
			}
			return fmt.Errorf("delete PrometheusRule: %w", err)
		}
		return nil
	}

	desired := buildPrometheusRule(agent)
	if desired == nil {
		return nil
	}
	if err := controllerutil.SetControllerReference(agent, desired, r.Scheme); err != nil {
		return fmt.Errorf("set owner on PrometheusRule: %w", err)
	}

	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(prometheusRuleGVK)
	err = r.Get(ctx, key, existing)
	switch {
	case apierrors.IsNotFound(err):
		if err := r.Create(ctx, desired); err != nil {
			if _, skip := handleDownstreamNoMatch(cacheKey, err); skip {
				return nil
			}
			return fmt.Errorf("create PrometheusRule: %w", err)
		}
		return nil
	case err != nil:
		if _, skip := handleDownstreamNoMatch(cacheKey, err); skip {
			return nil
		}
		return fmt.Errorf("get PrometheusRule: %w", err)
	}

	if !metav1.IsControlledBy(existing, agent) {
		return nil
	}

	existing.Object["spec"] = desired.Object["spec"]
	existing.SetLabels(desired.GetLabels())
	existing.SetGroupVersionKind(prometheusRuleGVK)
	if err := r.Update(ctx, existing); err != nil {
		if _, skip := handleDownstreamNoMatch(cacheKey, err); skip {
			return nil
		}
		return fmt.Errorf("update PrometheusRule: %w", err)
	}
	return nil
}

// buildPrometheusRule renders an unstructured PrometheusRule whose
// spec.groups carries the chart's default alert set verbatim.
//
// Sourced from charts/witwave/templates/prometheusrule.yaml +
// charts/witwave/values.yaml `prometheusRule.alerts.*`. The PromQL is
// byte-for-byte identical so the CI diff check
// (operator/scripts/check-prometheusrule-parity.sh) can compare the
// two surfaces.
func buildPrometheusRule(agent *witwavev1alpha1.WitwaveAgent) *unstructured.Unstructured {
	if !prometheusRuleEnabled(agent) {
		return nil
	}
	pr := agent.Spec.PrometheusRule

	labels := map[string]string{
		labelPartOf:    partOf,
		labelManagedBy: managedBy,
	}
	for k, v := range pr.AdditionalLabels {
		labels[k] = v
	}

	groups := []map[string]interface{}{
		{
			"name": "witwave.backends",
			"rules": []map[string]interface{}{
				{
					"alert": "WitwaveBackendDown",
					// for: omitted (#1263) — absent_over_time already
					// requires continuous absence; pairing with for: doubled
					// detection latency.
					"expr": "absent_over_time(backend_info{app_kubernetes_io_part_of=\"witwave\"}[5m])\nor\n(sum by (agent, backend, pod) (up{app_kubernetes_io_part_of=\"witwave\"}) == 0)",
					"labels": map[string]interface{}{
						"severity": "critical",
						"part_of":  "witwave",
					},
					"annotations": map[string]interface{}{
						"summary":     "witwave backend {{ $labels.backend }} on agent {{ $labels.agent }} is not being scraped",
						"description": "Backend pod has not reported metrics for 5m. Check pod health (crash-loop, image pull, metrics listener wedge).",
						"runbook_url": "https://github.com/witwave-ai/witwave/blob/main/docs/runbooks.md#witwavebackenddown",
					},
				},
				{
					"alert": "WitwaveHookDenialSpike",
					"expr":  "sum by (agent, backend) (rate(backend_hooks_denials_total[5m])) > 0.5",
					"for":   "10m",
					"labels": map[string]interface{}{
						"severity": "warning",
						"part_of":  "witwave",
					},
					"annotations": map[string]interface{}{
						"summary":     "witwave hook-denial spike on {{ $labels.agent }} / {{ $labels.backend }}",
						"description": "backend_hooks_denials_total 5m rate exceeds 0.5/s. Indicates hook policy tripping on repeated disallowed-tool attempts — investigate whether the agent is misconfigured or under jailbreak pressure.",
						"runbook_url": "https://github.com/witwave-ai/witwave/blob/main/docs/runbooks.md#witwavehookdenialspike",
					},
				},
			},
		},
		{
			"name": "witwave.mcp",
			"rules": []map[string]interface{}{
				{
					"alert": "WitwaveMcpAuthFailure",
					"expr":  "sum by (agent, backend, reason) (rate(backend_mcp_command_rejected_total[5m])) > 0.1",
					"for":   "5m",
					"labels": map[string]interface{}{
						"severity": "warning",
						"part_of":  "witwave",
					},
					"annotations": map[string]interface{}{
						"summary":     "MCP command rejected rate elevated on {{ $labels.agent }} / {{ $labels.backend }} (reason={{ $labels.reason }})",
						"description": "backend_mcp_command_rejected_total 5m rate > 0.1/s. Typically means the backend is attempting stdio MCP commands that fall outside MCP_ALLOWED_COMMANDS / MCP_ALLOWED_COMMAND_PREFIXES / MCP_ALLOWED_CWD_PREFIXES, or the MCP tool bearer-token auth is rejecting calls.",
						"runbook_url": "https://github.com/witwave-ai/witwave/blob/main/docs/runbooks.md#witwavemcpauthfailure",
					},
				},
			},
		},
		{
			"name": "witwave.webhooks",
			"rules": []map[string]interface{}{
				{
					"alert": "WitwaveWebhookTimeout",
					"expr":  "sum by (subscription) (rate(harness_webhooks_delivery_total{result=~\"timeout.*\"}[5m])) > 0.05",
					"for":   "10m",
					"labels": map[string]interface{}{
						"severity": "warning",
						"part_of":  "witwave",
					},
					"annotations": map[string]interface{}{
						"summary":     "witwave webhook deliveries timing out on {{ $labels.subscription }}",
						"description": "harness_webhooks_delivery_total{result=~\"timeout.*\"} 5m rate > 0.05/s. Webhook sink is slow or unreachable; downstream automation is missing events.",
						"runbook_url": "https://github.com/witwave-ai/witwave/blob/main/docs/runbooks.md#witwavewebhooktimeout",
					},
				},
			},
		},
		{
			"name": "witwave.storage",
			"rules": []map[string]interface{}{
				{
					"alert": "WitwaveLockWaitSaturation",
					"expr":  "histogram_quantile(0.99,\n  sum by (agent, backend, le) (rate(backend_sqlite_task_store_lock_wait_seconds_bucket[5m]))\n) > 1",
					"for":   "10m",
					"labels": map[string]interface{}{
						"severity": "warning",
						"part_of":  "witwave",
					},
					"annotations": map[string]interface{}{
						"summary":     "witwave task-store lock-wait p99 elevated on {{ $labels.agent }} / {{ $labels.backend }}",
						"description": "backend_sqlite_task_store_lock_wait_seconds p99 > 1s. SQLite task store is contending on locks; inflight tasks are queuing behind each other.",
						"runbook_url": "https://github.com/witwave-ai/witwave/blob/main/docs/runbooks.md#witwavelockwaitsaturation",
					},
				},
				{
					"alert": "WitwavePVCFillWarning",
					"expr":  "kubelet_volume_stats_used_bytes{persistentvolumeclaim=~\".*witwave.*\"}\n/ kubelet_volume_stats_capacity_bytes{persistentvolumeclaim=~\".*witwave.*\"}\n> 0.7",
					"for":   "10m",
					"labels": map[string]interface{}{
						"severity": "warning",
						"part_of":  "witwave",
					},
					"annotations": map[string]interface{}{
						"summary":     "witwave PVC {{ $labels.persistentvolumeclaim }} is 70%+ full",
						"description": "Persistent volume {{ $labels.persistentvolumeclaim }} is above 70% capacity. Conversation JSONL typically dominates; check log rotation settings or resize the PVC.",
						"runbook_url": "https://github.com/witwave-ai/witwave/blob/main/docs/runbooks.md#witwavepvcfillwarning--witwavepvcfillcritical",
					},
				},
				{
					"alert": "WitwavePVCFillCritical",
					"expr":  "kubelet_volume_stats_used_bytes{persistentvolumeclaim=~\".*witwave.*\"}\n/ kubelet_volume_stats_capacity_bytes{persistentvolumeclaim=~\".*witwave.*\"}\n> 0.9",
					"for":   "5m",
					"labels": map[string]interface{}{
						"severity": "critical",
						"part_of":  "witwave",
					},
					"annotations": map[string]interface{}{
						"summary":     "witwave PVC {{ $labels.persistentvolumeclaim }} nearly full (90%+)",
						"description": "Persistent volume {{ $labels.persistentvolumeclaim }} is above 90% capacity. Pod eviction risk; act immediately to free space or resize.",
						"runbook_url": "https://github.com/witwave-ai/witwave/blob/main/docs/runbooks.md#witwavepvcfillwarning--witwavepvcfillcritical",
					},
				},
			},
		},
		{
			"name": "witwave.retry_bytes",
			"rules": []map[string]interface{}{
				{
					"alert": "WitwaveWebhookRetryBytesHalfFull",
					"expr":  "(\n  harness_webhooks_retry_bytes_in_flight_total\n  /\n  clamp_min(\n    max(harness_webhooks_retry_bytes_budget_bytes{scope=\"global\"}), 1\n  )\n) > 0.5",
					"for":   "5m",
					"labels": map[string]interface{}{
						"severity": "warning",
						"part_of":  "witwave",
					},
					"annotations": map[string]interface{}{
						"summary":     "witwave webhook retry-byte budget above 50% utilisation",
						"description": "harness_webhooks_retry_bytes_in_flight_total is above 50% of WEBHOOK_RETRY_BYTES_BUDGET. Sink is slow; next step is drops via result=\"shed_retry_bytes\". Identify the offending subscription via per-sub gauge.",
						"runbook_url": "https://github.com/witwave-ai/witwave/blob/main/docs/runbooks.md#witwavewebhookretrybyteshalffull",
					},
				},
			},
		},
		{
			"name": "witwave.a2a",
			"rules": []map[string]interface{}{
				{
					"alert": "WitwaveA2ALatencyHigh",
					"expr":  "histogram_quantile(0.99,\n  sum by (backend, le) (rate(harness_a2a_backend_request_duration_seconds_bucket[5m]))\n) > 30",
					"for":   "5m",
					"labels": map[string]interface{}{
						"severity": "warning",
						"part_of":  "witwave",
					},
					"annotations": map[string]interface{}{
						"summary":     "witwave A2A relay p99 latency elevated to backend {{ $labels.backend }}",
						"description": "harness_a2a_backend_request_duration_seconds p99 > 30s. Likely pool exhaustion, wedged backend, or expected tool-heavy workload — check backend logs before tuning.",
						"runbook_url": "https://github.com/witwave-ai/witwave/blob/main/docs/runbooks.md#witwavea2alatencyhigh",
					},
				},
			},
		},
		{
			"name": "witwave.events",
			"rules": []map[string]interface{}{
				{
					"alert": "WitwaveEventValidationErrors",
					"expr":  "sum by (type) (rate(harness_event_stream_validation_errors_total[5m])) > 0",
					"for":   "5m",
					"labels": map[string]interface{}{
						"severity": "warning",
						"part_of":  "witwave",
					},
					"annotations": map[string]interface{}{
						"summary":     "witwave event schema validation failures for type={{ $labels.type }}",
						"description": "harness_event_stream_validation_errors_total 5m rate > 0/s. Emitter and schema have drifted; downstream subscribers never see the affected events.",
						"runbook_url": "https://github.com/witwave-ai/witwave/blob/main/docs/runbooks.md#witwaveeventvalidationerrors",
					},
				},
			},
		},
	}

	groupsAny := make([]interface{}, len(groups))
	for i, g := range groups {
		groupsAny[i] = g
	}

	out := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": prometheusRuleGVK.GroupVersion().String(),
		"kind":       prometheusRuleGVK.Kind,
		"metadata": map[string]interface{}{
			"name":      agent.Name + "-witwave",
			"namespace": agent.Namespace,
			"labels":    toUnstructuredStringMap(labels),
		},
		"spec": map[string]interface{}{
			"groups": groupsAny,
		},
	}}
	out.SetGroupVersionKind(prometheusRuleGVK)
	return out
}

// toUnstructuredStringMap converts a typed string map into the
// map[string]interface{} unstructured serialisation expects.
func toUnstructuredStringMap(in map[string]string) map[string]interface{} {
	out := make(map[string]interface{}, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
