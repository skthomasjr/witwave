/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package controller

import (
	"testing"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	witwavev1alpha1 "github.com/witwave-ai/witwave-operator/api/v1alpha1"
)

func npAgent(name string) *witwavev1alpha1.WitwaveAgent {
	return &witwavev1alpha1.WitwaveAgent{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: witwavev1alpha1.WitwaveAgentSpec{
			Port: 8000,
		},
	}
}

func TestBuildNetworkPolicy_EnabledEmptyIngressDeniesAll(t *testing.T) {
	a := npAgent("iris")
	a.Spec.NetworkPolicy = &witwavev1alpha1.NetworkPolicySpec{
		Enabled: true,
		// Ingress: nil -> deny-all ingress
	}
	np := buildNetworkPolicy(a)
	if np == nil {
		t.Fatal("expected NetworkPolicy, got nil")
	}
	if len(np.Spec.Ingress) != 0 {
		t.Fatalf("expected deny-all ingress (empty rule list), got %d rules", len(np.Spec.Ingress))
	}
	// PolicyTypes must include Ingress even when empty (that's how
	// v1 NetworkPolicy expresses deny-all-ingress).
	hasIngress := false
	for _, pt := range np.Spec.PolicyTypes {
		if pt == networkingv1.PolicyTypeIngress {
			hasIngress = true
		}
	}
	if !hasIngress {
		t.Fatal("PolicyTypes missing PolicyTypeIngress")
	}
}

func TestBuildNetworkPolicy_DashboardPeerAllowed(t *testing.T) {
	a := npAgent("iris")
	allow := true
	a.Spec.NetworkPolicy = &witwavev1alpha1.NetworkPolicySpec{
		Enabled: true,
		Ingress: &witwavev1alpha1.NetworkPolicyIngressSpec{
			AllowDashboard: &allow,
		},
	}
	np := buildNetworkPolicy(a)
	if len(np.Spec.Ingress) != 1 {
		t.Fatalf("expected 1 ingress rule, got %d", len(np.Spec.Ingress))
	}
	rule := np.Spec.Ingress[0]
	if len(rule.From) != 1 {
		t.Fatalf("expected 1 peer, got %d", len(rule.From))
	}
	if rule.From[0].PodSelector == nil {
		t.Fatal("expected PodSelector for dashboard peer")
	}
	if rule.From[0].PodSelector.MatchLabels[labelComponent] != "dashboard" {
		t.Errorf("peer component: got %q want dashboard", rule.From[0].PodSelector.MatchLabels[labelComponent])
	}
}

func TestBuildNetworkPolicy_MetricsFromScopedToMetricsPort(t *testing.T) {
	a := npAgent("iris")
	disable := false
	a.Spec.NetworkPolicy = &witwavev1alpha1.NetworkPolicySpec{
		Enabled: true,
		Ingress: &witwavev1alpha1.NetworkPolicyIngressSpec{
			AllowDashboard: &disable,
			MetricsFrom: []networkingv1.NetworkPolicyPeer{{
				NamespaceSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"kubernetes.io/metadata.name": "monitoring"},
				},
			}},
		},
	}
	np := buildNetworkPolicy(a)
	if len(np.Spec.Ingress) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(np.Spec.Ingress))
	}
	rule := np.Spec.Ingress[0]
	if len(rule.Ports) != 1 {
		t.Fatalf("expected 1 port constraint, got %d", len(rule.Ports))
	}
	if rule.Ports[0].Port == nil {
		t.Fatal("expected port set")
	}
	// containerMetricsPort(0, 8000) -> 9000 (app_port + 1000); assert we
	// scoped to the metrics listener, not the app port.
	if rule.Ports[0].Port.IntValue() == 8000 {
		t.Error("metrics rule should not be scoped to the app port")
	}
}

func TestBuildNetworkPolicy_EgressOpenByDefault(t *testing.T) {
	a := npAgent("iris")
	a.Spec.NetworkPolicy = &witwavev1alpha1.NetworkPolicySpec{Enabled: true}
	np := buildNetworkPolicy(a)
	for _, pt := range np.Spec.PolicyTypes {
		if pt == networkingv1.PolicyTypeEgress {
			t.Fatal("egress should not be restricted by default (EgressOpen=true)")
		}
	}
}

func TestBuildNetworkPolicy_EgressClosedOptIn(t *testing.T) {
	a := npAgent("iris")
	closed := false
	a.Spec.NetworkPolicy = &witwavev1alpha1.NetworkPolicySpec{
		Enabled:    true,
		EgressOpen: &closed,
	}
	np := buildNetworkPolicy(a)
	hasEgress := false
	for _, pt := range np.Spec.PolicyTypes {
		if pt == networkingv1.PolicyTypeEgress {
			hasEgress = true
		}
	}
	if !hasEgress {
		t.Fatal("expected PolicyTypeEgress when EgressOpen=false")
	}
	if len(np.Spec.Egress) != 0 {
		t.Fatalf("expected deny-all egress (empty list), got %d rules", len(np.Spec.Egress))
	}
}
