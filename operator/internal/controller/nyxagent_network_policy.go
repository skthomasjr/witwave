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
	"fmt"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	nyxv1alpha1 "github.com/nyx-ai/nyx-operator/api/v1alpha1"
)

// reconcileNetworkPolicy renders a per-agent NetworkPolicy when
// spec.networkPolicy.enabled=true (#971). Scaffold scope: the policy
// targets only the agent pod (selector=selectorLabels(agent)); MCP-tool
// NetworkPolicies (the chart's `allowNyxAgents` knob) are follow-up.
func (r *NyxAgentReconciler) reconcileNetworkPolicy(ctx context.Context, agent *nyxv1alpha1.NyxAgent) error {
	np := agent.Spec.NetworkPolicy
	if np == nil || !np.Enabled {
		return nil
	}
	desired := buildNetworkPolicy(agent)
	if err := controllerutil.SetControllerReference(agent, desired, r.Scheme); err != nil {
		return fmt.Errorf("set owner on NetworkPolicy: %w", err)
	}
	return applySSA(ctx, r.Client, desired)
}

// buildNetworkPolicy renders the v1 NetworkPolicy the operator owns for
// the agent pod. Extracted so unit tests can assert peer-list shape
// without a fake client.
func buildNetworkPolicy(agent *nyxv1alpha1.NyxAgent) *networkingv1.NetworkPolicy {
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
		ObjectMeta: metav1.ObjectMeta{
			Name:      agent.Name,
			Namespace: agent.Namespace,
			Labels:    agentLabels(agent),
		},
		Spec: spec,
	}
}

// buildNetworkPolicyIngressRules translates the spec's peer-opt-in knobs
// into concrete NetworkPolicyIngressRule entries. Each rule is scoped to
// the narrowest possible port set so turning one peer off does not
// accidentally broaden another's reach.
func buildNetworkPolicyIngressRules(agent *nyxv1alpha1.NyxAgent, appPort, metricsPort int32) []networkingv1.NetworkPolicyIngressRule {
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

	// Same-namespace peer: reaches every port on the agent pod. Uses an
	// empty PodSelector which matches all pods in the namespace this
	// NetworkPolicy lives in (that's how v1 NetworkPolicy peers scope by
	// default — no namespaceSelector needed for same-namespace).
	if ing.AllowSameNamespace {
		rules = append(rules, networkingv1.NetworkPolicyIngressRule{
			From: []networkingv1.NetworkPolicyPeer{{
				PodSelector: &metav1.LabelSelector{},
			}},
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
