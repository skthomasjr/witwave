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

import (
	"context"
	"errors"
	"fmt"

	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	witwavev1alpha1 "github.com/witwave-ai/witwave-operator/api/v1alpha1"
)

// mcpToolDefaultMetricsPort is the chart default for `mcpTools.metricsPort`
// (charts/witwave/values.yaml). The operator's CRD does not yet surface
// this knob, so we mirror the chart constant when scoping the metrics
// rule on per-tool NetworkPolicies (#1743).
const mcpToolDefaultMetricsPort int32 = 9000

// reconcileNetworkPolicy renders per-agent and per-sibling NetworkPolicies
// when spec.networkPolicy.enabled=true (#971, #1743). Three pod-selector
// targets are emitted to mirror chart parity:
//
//   - the agent pod itself (selectorLabels(agent))
//   - each enabled MCP tool sibling (mcpToolSelector(agent, tool))
//   - the per-agent dashboard sibling, when spec.dashboard.enabled=true
//     (dashboardSelectorLabels(agent))
//
// Sibling NetworkPolicies are GC'd when their backing Deployment is
// disabled — the same pattern reconcileMCPTools/cleanupMCPTools uses for
// Deployments + Services.
func (r *WitwaveAgentReconciler) reconcileNetworkPolicy(ctx context.Context, agent *witwavev1alpha1.WitwaveAgent) error {
	np := agent.Spec.NetworkPolicy
	// Agent-disabled short-circuit (#1635): when the parent CR has
	// spec.enabled=false the teardown path invokes this reconciler so a
	// previously-applied NetworkPolicy gets cleaned up. Force the
	// delete-if-present branch regardless of np.Enabled — the apply path
	// would otherwise re-stamp a NetworkPolicy on a CR whose Deployment
	// has just been removed.
	agentDisabled := agent.Spec.Enabled != nil && !*agent.Spec.Enabled

	// Delete-if-present path (#1175): flipping spec.networkPolicy.enabled
	// from true to false (or removing the block entirely) used to leave
	// the previously-applied NetworkPolicy behind, so the agent stayed
	// effectively isolated until someone deleted the object by hand.
	// Mirror reconcileHPA: Get by name, IsControlledBy-check, Delete.
	if agentDisabled || np == nil || !np.Enabled {
		var errs []error
		// Agent NP
		if err := r.deleteOwnedNetworkPolicy(ctx, agent, agent.Name); err != nil {
			errs = append(errs, err)
		}
		// Sibling NPs (mcp + dashboard) — best-effort cleanup so that
		// a disable flip removes every previously-owned NetworkPolicy.
		for _, sib := range siblingNetworkPolicyNames(agent) {
			if err := r.deleteOwnedNetworkPolicy(ctx, agent, sib); err != nil {
				errs = append(errs, err)
			}
		}
		return errors.Join(errs...)
	}

	var errs []error

	// Agent NP — preserves the original behaviour.
	desired := buildNetworkPolicy(agent)
	if err := controllerutil.SetControllerReference(agent, desired, r.Scheme); err != nil {
		errs = append(errs, fmt.Errorf("set owner on NetworkPolicy: %w", err))
	} else if err := applySSA(ctx, r.Client, desired); err != nil {
		errs = append(errs, fmt.Errorf("apply agent NetworkPolicy: %w", err))
	}

	// Sibling MCP-tool NPs — only the enabled set is rendered; disabled
	// tools fall through to the cleanup pass below so flipping a tool
	// off tears the NetworkPolicy down.
	desiredSiblings := map[string]struct{}{}
	if agent.Spec.MCPTools != nil {
		toolCandidates := map[string]*witwavev1alpha1.MCPToolSpec{
			"kubernetes": agent.Spec.MCPTools.Kubernetes,
			"helm":       agent.Spec.MCPTools.Helm,
			"prometheus": agent.Spec.MCPTools.Prometheus,
		}
		for name, tool := range toolCandidates {
			if tool == nil || !tool.Enabled {
				continue
			}
			toolNP := buildMCPToolNetworkPolicy(agent, name)
			if err := controllerutil.SetControllerReference(agent, toolNP, r.Scheme); err != nil {
				errs = append(errs, fmt.Errorf("set owner on mcp-%s NetworkPolicy: %w", name, err))
				continue
			}
			if err := applySSA(ctx, r.Client, toolNP); err != nil {
				errs = append(errs, fmt.Errorf("apply mcp-%s NetworkPolicy: %w", name, err))
				continue
			}
			desiredSiblings[toolNP.Name] = struct{}{}
		}
	}

	// Sibling dashboard NP.
	if agent.Spec.Dashboard != nil && agent.Spec.Dashboard.Enabled {
		dashNP := buildDashboardNetworkPolicy(agent)
		if err := controllerutil.SetControllerReference(agent, dashNP, r.Scheme); err != nil {
			errs = append(errs, fmt.Errorf("set owner on dashboard NetworkPolicy: %w", err))
		} else if err := applySSA(ctx, r.Client, dashNP); err != nil {
			errs = append(errs, fmt.Errorf("apply dashboard NetworkPolicy: %w", err))
		} else {
			desiredSiblings[dashNP.Name] = struct{}{}
		}
	}

	// Cleanup pass for siblings: any previously-owned sibling NP that
	// is no longer in the desired set gets deleted.
	for _, sib := range siblingNetworkPolicyNames(agent) {
		if _, want := desiredSiblings[sib]; want {
			continue
		}
		if err := r.deleteOwnedNetworkPolicy(ctx, agent, sib); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// siblingNetworkPolicyNames returns the static list of sibling
// NetworkPolicy names a single WitwaveAgent could ever own. Used by the
// disable/cleanup path so we never iterate "every NetworkPolicy in the
// namespace" — bounded by the 3 known MCP tools + 1 dashboard.
func siblingNetworkPolicyNames(agent *witwavev1alpha1.WitwaveAgent) []string {
	return []string{
		mcpToolName(agent, "kubernetes"),
		mcpToolName(agent, "helm"),
		mcpToolName(agent, "prometheus"),
		agent.Name + "-dashboard",
	}
}

// deleteOwnedNetworkPolicy gates a delete on IsControlledBy so we never
// remove a NetworkPolicy authored by a non-operator manager.
func (r *WitwaveAgentReconciler) deleteOwnedNetworkPolicy(ctx context.Context, agent *witwavev1alpha1.WitwaveAgent, name string) error {
	existing := &networkingv1.NetworkPolicy{}
	key := client.ObjectKey{Namespace: agent.Namespace, Name: name}
	if err := r.Get(ctx, key, existing); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("get NetworkPolicy %q for cleanup: %w", name, err)
	}
	if !metav1.IsControlledBy(existing, agent) {
		return nil
	}
	if err := r.Delete(ctx, existing); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete NetworkPolicy %q: %w", name, err)
	}
	return nil
}

// buildNetworkPolicy renders the v1 NetworkPolicy the operator owns for
// the agent pod. Extracted so unit tests can assert peer-list shape
// without a fake client.
func buildNetworkPolicy(agent *witwavev1alpha1.WitwaveAgent) *networkingv1.NetworkPolicy {
	np := agent.Spec.NetworkPolicy
	port := agent.Spec.Port
	if port == 0 {
		port = 8000
	}
	metricsPort := containerMetricsPort(agent.Spec.MetricsPort, port)

	policyTypes := []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}
	egressOpen := true
	if np.EgressOpen != nil {
		egressOpen = *np.EgressOpen
	}
	if !egressOpen {
		policyTypes = append(policyTypes, networkingv1.PolicyTypeEgress)
	}

	ingressRules := buildNetworkPolicyIngressRules(agent, port, metricsPort)

	spec := networkingv1.NetworkPolicySpec{
		PodSelector: metav1.LabelSelector{MatchLabels: selectorLabels(agent)},
		PolicyTypes: policyTypes,
		Ingress:     ingressRules,
	}
	if !egressOpen {
		spec.Egress = nil // explicit deny-all-egress (empty list)
	}

	return &networkingv1.NetworkPolicy{
		// #1565: stamp TypeMeta explicitly so applySSA no longer
		// depends on scheme lookup to infer the GVK. A missing GVK
		// path through SSA surfaces as a confusing generic Patch
		// error; the stamp here is a belt-and-suspenders fix.
		TypeMeta: metav1.TypeMeta{
			APIVersion: "networking.k8s.io/v1",
			Kind:       "NetworkPolicy",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      agent.Name,
			Namespace: agent.Namespace,
			Labels:    agentLabels(agent),
		},
		Spec: spec,
	}
}

// buildMCPToolNetworkPolicy renders the v1 NetworkPolicy that scopes the
// per-tool MCP sibling pod (#1743). Mirrors charts/witwave/templates/networkpolicy.yaml's
// `mcpTools` block: same-namespace + additionalFrom + metricsFrom rules
// from the spec, plus the "allowWitwaveAgents" rule that lets agents and
// other witwave pods reach the FastMCP listener on TCP/8000.
func buildMCPToolNetworkPolicy(agent *witwavev1alpha1.WitwaveAgent, tool string) *networkingv1.NetworkPolicy {
	np := agent.Spec.NetworkPolicy
	policyTypes := []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}
	egressOpen := true
	if np != nil && np.EgressOpen != nil {
		egressOpen = *np.EgressOpen
	}
	if !egressOpen {
		policyTypes = append(policyTypes, networkingv1.PolicyTypeEgress)
	}

	ingSpec := (*witwavev1alpha1.NetworkPolicyIngressSpec)(nil)
	if np != nil {
		ingSpec = np.Ingress
	}

	var rules []networkingv1.NetworkPolicyIngressRule

	// allowSameNamespace -> scoped to the FastMCP listener (8000).
	if ingSpec != nil && ingSpec.AllowSameNamespace {
		rules = append(rules, networkingv1.NetworkPolicyIngressRule{
			From: []networkingv1.NetworkPolicyPeer{{
				PodSelector: &metav1.LabelSelector{},
			}},
			Ports: []networkingv1.NetworkPolicyPort{{Port: intstrPtr(int(mcpToolPort))}},
		})
	}
	// metricsFrom -> scoped to the MCP-tool metrics port (default 9000).
	if ingSpec != nil && len(ingSpec.MetricsFrom) > 0 {
		rules = append(rules, networkingv1.NetworkPolicyIngressRule{
			From: append([]networkingv1.NetworkPolicyPeer(nil), ingSpec.MetricsFrom...),
			Ports: []networkingv1.NetworkPolicyPort{{
				Port: intstrPtr(int(mcpToolDefaultMetricsPort)),
			}},
		})
	}
	// additionalFrom -> every port.
	if ingSpec != nil && len(ingSpec.AdditionalFrom) > 0 {
		rules = append(rules, networkingv1.NetworkPolicyIngressRule{
			From: append([]networkingv1.NetworkPolicyPeer(nil), ingSpec.AdditionalFrom...),
		})
	}
	// allowWitwaveAgents (#1074): default-on. Match on
	// app.kubernetes.io/part-of=witwave so every chart-or-operator pod can
	// reach the FastMCP listener on :8000 without broadening to "every
	// pod in the namespace".
	rules = append(rules, networkingv1.NetworkPolicyIngressRule{
		From: []networkingv1.NetworkPolicyPeer{{
			NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{
				"kubernetes.io/metadata.name": agent.Namespace,
			}},
			PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{
				labelPartOf: partOf,
			}},
		}},
		Ports: []networkingv1.NetworkPolicyPort{{Port: intstrPtr(int(mcpToolPort))}},
	})

	spec := networkingv1.NetworkPolicySpec{
		PodSelector: metav1.LabelSelector{MatchLabels: mcpToolSelector(agent, tool)},
		PolicyTypes: policyTypes,
		Ingress:     rules,
	}

	return &networkingv1.NetworkPolicy{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "networking.k8s.io/v1",
			Kind:       "NetworkPolicy",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      mcpToolName(agent, tool),
			Namespace: agent.Namespace,
			Labels:    mcpToolLabels(agent, tool),
		},
		Spec: spec,
	}
}

// buildDashboardNetworkPolicy renders the v1 NetworkPolicy that scopes
// the per-agent dashboard pod (#1743). Mirrors the chart's `dashboard`
// NP block: allowSameNamespace + additionalFrom rules apply, allowDashboard
// is intentionally a no-op (dashboard talking to itself), and there is no
// metricsFrom rule because the dashboard does not expose a separate
// metrics port.
func buildDashboardNetworkPolicy(agent *witwavev1alpha1.WitwaveAgent) *networkingv1.NetworkPolicy {
	np := agent.Spec.NetworkPolicy
	policyTypes := []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}
	egressOpen := true
	if np != nil && np.EgressOpen != nil {
		egressOpen = *np.EgressOpen
	}
	if !egressOpen {
		policyTypes = append(policyTypes, networkingv1.PolicyTypeEgress)
	}

	dashPort := int32(80)
	if agent.Spec.Dashboard != nil && agent.Spec.Dashboard.Port != 0 {
		dashPort = agent.Spec.Dashboard.Port
	}

	ingSpec := (*witwavev1alpha1.NetworkPolicyIngressSpec)(nil)
	if np != nil {
		ingSpec = np.Ingress
	}

	var rules []networkingv1.NetworkPolicyIngressRule

	if ingSpec != nil && ingSpec.AllowSameNamespace {
		rules = append(rules, networkingv1.NetworkPolicyIngressRule{
			From: []networkingv1.NetworkPolicyPeer{{
				PodSelector: &metav1.LabelSelector{},
			}},
			Ports: []networkingv1.NetworkPolicyPort{{Port: intstrPtr(int(dashPort))}},
		})
	}
	if ingSpec != nil && len(ingSpec.AdditionalFrom) > 0 {
		rules = append(rules, networkingv1.NetworkPolicyIngressRule{
			From: append([]networkingv1.NetworkPolicyPeer(nil), ingSpec.AdditionalFrom...),
		})
	}

	spec := networkingv1.NetworkPolicySpec{
		PodSelector: metav1.LabelSelector{MatchLabels: dashboardSelectorLabels(agent)},
		PolicyTypes: policyTypes,
		Ingress:     rules,
	}

	return &networkingv1.NetworkPolicy{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "networking.k8s.io/v1",
			Kind:       "NetworkPolicy",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      agent.Name + "-dashboard",
			Namespace: agent.Namespace,
			Labels:    dashboardLabels(agent),
		},
		Spec: spec,
	}
}

// buildNetworkPolicyIngressRules translates the spec's peer-opt-in knobs
// into concrete NetworkPolicyIngressRule entries. Each rule is scoped to
// the narrowest possible port set so turning one peer off does not
// accidentally broaden another's reach.
func buildNetworkPolicyIngressRules(agent *witwavev1alpha1.WitwaveAgent, appPort, metricsPort int32) []networkingv1.NetworkPolicyIngressRule {
	ing := agent.Spec.NetworkPolicy.Ingress
	if ing == nil {
		// Enabled=true with Ingress=nil is a deliberate deny-all
		// ingress policy, mirroring the chart's fail-closed default.
		return nil
	}

	var rules []networkingv1.NetworkPolicyIngressRule

	// Dashboard peer: reaches the app port.
	allowDashboard := true
	if ing.AllowDashboard != nil {
		allowDashboard = *ing.AllowDashboard
	}
	if allowDashboard {
		rules = append(rules, networkingv1.NetworkPolicyIngressRule{
			From: []networkingv1.NetworkPolicyPeer{{
				PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{
					labelComponent: "dashboard",
					labelPartOf:    partOf,
				}},
			}},
			Ports: []networkingv1.NetworkPolicyPort{{
				Port: intstrPtr(int(appPort)),
			}},
		})
	}

	// Same-namespace peer: reaches the narrow set of ports the agent
	// actually exposes (app + metrics) instead of every port on the pod
	// (#1174). Previously an empty Ports list meant "all ports", which
	// defeated the point of defining a per-port policy at all —
	// sidecars co-tenanting the pod became reachable from any neighbour
	// in the namespace. Callers who genuinely need a wider same-
	// namespace reach should set AdditionalFrom instead; that path
	// keeps the explicit "all ports" escape hatch and is documented
	// accordingly.
	if ing.AllowSameNamespace {
		rules = append(rules, networkingv1.NetworkPolicyIngressRule{
			From: []networkingv1.NetworkPolicyPeer{{
				PodSelector: &metav1.LabelSelector{},
			}},
			Ports: []networkingv1.NetworkPolicyPort{
				{Port: intstrPtr(int(appPort))},
				{Port: intstrPtr(int(metricsPort))},
			},
		})
	}

	// Metrics peer: reaches metrics port only.
	if len(ing.MetricsFrom) > 0 {
		rules = append(rules, networkingv1.NetworkPolicyIngressRule{
			From: append([]networkingv1.NetworkPolicyPeer(nil), ing.MetricsFrom...),
			Ports: []networkingv1.NetworkPolicyPort{{
				Port: intstrPtr(int(metricsPort)),
			}},
		})
	}

	// Additional peers: reach every port (analogue of `additionalFrom`
	// in the chart). Use sparingly.
	if len(ing.AdditionalFrom) > 0 {
		rules = append(rules, networkingv1.NetworkPolicyIngressRule{
			From: append([]networkingv1.NetworkPolicyPeer(nil), ing.AdditionalFrom...),
		})
	}

	return rules
}

func intstrPtr(i int) *intstr.IntOrString {
	v := intstr.FromInt(i)
	return &v
}
