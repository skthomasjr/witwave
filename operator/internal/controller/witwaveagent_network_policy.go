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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	witwavev1alpha1 "github.com/witwave-ai/witwave-operator/api/v1alpha1"
)

// reconcileNetworkPolicy renders a per-agent NetworkPolicy when
// spec.networkPolicy.enabled=true (#971). Scaffold scope: the policy
// targets only the agent pod (selector=selectorLabels(agent)); MCP-tool
// NetworkPolicies (the chart's `allowWitwaveAgents` knob) are follow-up.
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
		existing := &networkingv1.NetworkPolicy{}
		key := client.ObjectKey{Namespace: agent.Namespace, Name: agent.Name}
		if err := r.Get(ctx, key, existing); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("get NetworkPolicy for cleanup: %w", err)
		}
		if !metav1.IsControlledBy(existing, agent) {
			// Never touch a NetworkPolicy we didn't create; operators
			// sometimes hand-author one with the agent's name.
			return nil
		}
		if err := r.Delete(ctx, existing); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete disabled NetworkPolicy: %w", err)
		}
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
