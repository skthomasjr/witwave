/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package controller

import (
	"context"
	"testing"

	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

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

// ----- sibling NP tests (#1743) -----

func TestBuildMCPToolNetworkPolicy_SelectorAndAllowWitwaveAgents(t *testing.T) {
	a := npAgent("iris")
	a.Spec.NetworkPolicy = &witwavev1alpha1.NetworkPolicySpec{Enabled: true}
	np := buildMCPToolNetworkPolicy(a, "kubernetes")
	if np.Name != "iris-mcp-kubernetes" {
		t.Errorf("name: got %q want iris-mcp-kubernetes", np.Name)
	}
	if got := np.Spec.PodSelector.MatchLabels[labelName]; got != "iris-mcp-kubernetes" {
		t.Errorf("podSelector: got %q want iris-mcp-kubernetes", got)
	}
	// allowWitwaveAgents should always render — it's the chart's default-on
	// rule that lets agents reach the FastMCP listener at :8000.
	if len(np.Spec.Ingress) != 1 {
		t.Fatalf("expected exactly the allowWitwaveAgents rule, got %d", len(np.Spec.Ingress))
	}
	rule := np.Spec.Ingress[0]
	if len(rule.From) != 1 {
		t.Fatalf("expected 1 peer in allowWitwaveAgents, got %d", len(rule.From))
	}
	peer := rule.From[0]
	if peer.PodSelector == nil || peer.PodSelector.MatchLabels[labelPartOf] != partOf {
		t.Errorf("expected part-of=witwave podSelector, got %+v", peer.PodSelector)
	}
	if peer.NamespaceSelector == nil ||
		peer.NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"] != "default" {
		t.Errorf("expected namespaceSelector pinned to default, got %+v", peer.NamespaceSelector)
	}
	if len(rule.Ports) != 1 || rule.Ports[0].Port.IntValue() != 8000 {
		t.Errorf("expected scoped to TCP/8000, got %+v", rule.Ports)
	}
}

func TestBuildMCPToolNetworkPolicy_AllowSameNamespaceScopedTo8000(t *testing.T) {
	a := npAgent("iris")
	a.Spec.NetworkPolicy = &witwavev1alpha1.NetworkPolicySpec{
		Enabled: true,
		Ingress: &witwavev1alpha1.NetworkPolicyIngressSpec{AllowSameNamespace: true},
	}
	np := buildMCPToolNetworkPolicy(a, "helm")
	// Expect: 1 same-namespace rule + 1 allowWitwaveAgents rule = 2.
	if len(np.Spec.Ingress) != 2 {
		t.Fatalf("expected 2 rules (sameNs + allowWitwaveAgents), got %d", len(np.Spec.Ingress))
	}
	sn := np.Spec.Ingress[0]
	if len(sn.Ports) != 1 || sn.Ports[0].Port.IntValue() != 8000 {
		t.Errorf("allowSameNamespace must be scoped to TCP/8000, got %+v", sn.Ports)
	}
}

func TestBuildMCPToolNetworkPolicy_MetricsFromScopedToMetricsPort(t *testing.T) {
	a := npAgent("iris")
	a.Spec.NetworkPolicy = &witwavev1alpha1.NetworkPolicySpec{
		Enabled: true,
		Ingress: &witwavev1alpha1.NetworkPolicyIngressSpec{
			MetricsFrom: []networkingv1.NetworkPolicyPeer{{
				NamespaceSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"kubernetes.io/metadata.name": "monitoring"},
				},
			}},
		},
	}
	np := buildMCPToolNetworkPolicy(a, "kubernetes")
	// Find the metricsFrom rule: it's the one whose port is 9000.
	found := false
	for _, rule := range np.Spec.Ingress {
		if len(rule.Ports) == 1 && rule.Ports[0].Port.IntValue() == int(mcpToolDefaultMetricsPort) {
			found = true
			if len(rule.From) != 1 {
				t.Errorf("metricsFrom: expected 1 peer, got %d", len(rule.From))
			}
		}
	}
	if !found {
		t.Errorf("expected a metricsFrom rule scoped to %d, got %+v", mcpToolDefaultMetricsPort, np.Spec.Ingress)
	}
}

func TestBuildDashboardNetworkPolicy_PodSelectorAndPortDefault(t *testing.T) {
	a := npAgent("iris")
	a.Spec.Dashboard = &witwavev1alpha1.DashboardSpec{Enabled: true}
	a.Spec.NetworkPolicy = &witwavev1alpha1.NetworkPolicySpec{
		Enabled: true,
		Ingress: &witwavev1alpha1.NetworkPolicyIngressSpec{AllowSameNamespace: true},
	}
	np := buildDashboardNetworkPolicy(a)
	if np.Name != "iris-dashboard" {
		t.Errorf("name: got %q want iris-dashboard", np.Name)
	}
	if got := np.Spec.PodSelector.MatchLabels[labelName]; got != "iris-dashboard" {
		t.Errorf("podSelector: got %q want iris-dashboard", got)
	}
	if len(np.Spec.Ingress) != 1 {
		t.Fatalf("expected 1 rule (allowSameNamespace), got %d", len(np.Spec.Ingress))
	}
	if len(np.Spec.Ingress[0].Ports) != 1 || np.Spec.Ingress[0].Ports[0].Port.IntValue() != 80 {
		t.Errorf("dashboard port default 80 not honoured: %+v", np.Spec.Ingress[0].Ports)
	}
}

func TestBuildDashboardNetworkPolicy_NoMetricsFromRule(t *testing.T) {
	// dashboard NP must not emit a metricsFrom rule even when the spec
	// configures metricsFrom — the chart's dashboard block deliberately
	// skips it because the dashboard pod has no separate metrics port.
	a := npAgent("iris")
	a.Spec.Dashboard = &witwavev1alpha1.DashboardSpec{Enabled: true}
	a.Spec.NetworkPolicy = &witwavev1alpha1.NetworkPolicySpec{
		Enabled: true,
		Ingress: &witwavev1alpha1.NetworkPolicyIngressSpec{
			MetricsFrom: []networkingv1.NetworkPolicyPeer{{
				NamespaceSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"kubernetes.io/metadata.name": "monitoring"},
				},
			}},
		},
	}
	np := buildDashboardNetworkPolicy(a)
	for _, rule := range np.Spec.Ingress {
		// no rule should be scoped to a metrics-style port.
		for _, p := range rule.Ports {
			if p.Port != nil && p.Port.IntValue() == int(mcpToolDefaultMetricsPort) {
				t.Errorf("dashboard NP must not stamp metricsFrom rule")
			}
		}
	}
}

// reconciler-level cleanup test (#1743): when an MCP tool is disabled
// after being enabled, its previously-owned NetworkPolicy must be
// deleted along with the Deployment + Service.

func newNPTestReconciler(t *testing.T, agent *witwavev1alpha1.WitwaveAgent, prep ...client.Object) (*WitwaveAgentReconciler, client.Client) {
	t.Helper()
	sch := runtime.NewScheme()
	if err := scheme.AddToScheme(sch); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	if err := witwavev1alpha1.AddToScheme(sch); err != nil {
		t.Fatalf("add witwave scheme: %v", err)
	}
	objs := append([]client.Object{agent}, prep...)
	c := fake.NewClientBuilder().
		WithScheme(sch).
		WithObjects(objs...).
		WithInterceptorFuncs(ssaCreateOnMissing()).
		Build()
	r := &WitwaveAgentReconciler{
		Client:    c,
		APIReader: c,
		Scheme:    sch,
		Recorder:  record.NewFakeRecorder(8),
	}
	return r, c
}

func TestReconcileNetworkPolicy_RendersSiblingNPs(t *testing.T) {
	a := &witwavev1alpha1.WitwaveAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "iris", Namespace: "default", UID: "uid-iris"},
		Spec: witwavev1alpha1.WitwaveAgentSpec{
			NetworkPolicy: &witwavev1alpha1.NetworkPolicySpec{Enabled: true},
			Dashboard:     &witwavev1alpha1.DashboardSpec{Enabled: true},
			MCPTools: &witwavev1alpha1.MCPToolsSpec{
				Kubernetes: &witwavev1alpha1.MCPToolSpec{Enabled: true},
			},
		},
	}
	r, c := newNPTestReconciler(t, a)
	if err := r.reconcileNetworkPolicy(context.Background(), a); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	// Agent NP, dashboard NP, and mcp-kubernetes NP should all exist.
	for _, name := range []string{"iris", "iris-dashboard", "iris-mcp-kubernetes"} {
		got := &networkingv1.NetworkPolicy{}
		if err := c.Get(context.Background(),
			client.ObjectKey{Namespace: "default", Name: name}, got); err != nil {
			t.Errorf("expected NetworkPolicy %q, got %v", name, err)
		}
	}
	// helm + prometheus tools were not enabled — those NPs must NOT exist.
	for _, name := range []string{"iris-mcp-helm", "iris-mcp-prometheus"} {
		got := &networkingv1.NetworkPolicy{}
		if err := c.Get(context.Background(),
			client.ObjectKey{Namespace: "default", Name: name}, got); !apierrors.IsNotFound(err) {
			t.Errorf("expected no NP for %q, got %v", name, err)
		}
	}
}

func TestReconcileNetworkPolicy_CleansUpDisabledSibling(t *testing.T) {
	a := &witwavev1alpha1.WitwaveAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "iris", Namespace: "default", UID: "uid-iris"},
		Spec: witwavev1alpha1.WitwaveAgentSpec{
			NetworkPolicy: &witwavev1alpha1.NetworkPolicySpec{Enabled: true},
			// Dashboard disabled now, but a previously-owned
			// dashboard NP exists below.
		},
	}
	tru := true
	owned := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "iris-dashboard",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: witwavev1alpha1.GroupVersion.String(),
				Kind:       "WitwaveAgent",
				Name:       "iris",
				UID:        "uid-iris",
				Controller: &tru,
			}},
		},
	}
	r, c := newNPTestReconciler(t, a, owned)
	if err := r.reconcileNetworkPolicy(context.Background(), a); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got := &networkingv1.NetworkPolicy{}
	if err := c.Get(context.Background(),
		client.ObjectKey{Namespace: "default", Name: "iris-dashboard"}, got); !apierrors.IsNotFound(err) {
		t.Fatalf("expected disabled-sibling NP to be cleaned up, got %v", err)
	}
}

func TestReconcileNetworkPolicy_DisableFlipDeletesAllSiblings(t *testing.T) {
	// spec.networkPolicy.enabled=false (or removed) must clean up the
	// agent NP and every sibling NP previously owned by the agent.
	a := &witwavev1alpha1.WitwaveAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "iris", Namespace: "default", UID: "uid-iris"},
		// no NetworkPolicy block -> disable path
	}
	tru := true
	pre := []client.Object{
		&networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{
			Name: "iris", Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: witwavev1alpha1.GroupVersion.String(),
				Kind:       "WitwaveAgent", Name: "iris", UID: "uid-iris", Controller: &tru,
			}},
		}},
		&networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{
			Name: "iris-mcp-kubernetes", Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: witwavev1alpha1.GroupVersion.String(),
				Kind:       "WitwaveAgent", Name: "iris", UID: "uid-iris", Controller: &tru,
			}},
		}},
		&networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{
			Name: "iris-dashboard", Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: witwavev1alpha1.GroupVersion.String(),
				Kind:       "WitwaveAgent", Name: "iris", UID: "uid-iris", Controller: &tru,
			}},
		}},
	}
	r, c := newNPTestReconciler(t, a, pre...)
	if err := r.reconcileNetworkPolicy(context.Background(), a); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	for _, name := range []string{"iris", "iris-mcp-kubernetes", "iris-dashboard"} {
		got := &networkingv1.NetworkPolicy{}
		if err := c.Get(context.Background(),
			client.ObjectKey{Namespace: "default", Name: name}, got); !apierrors.IsNotFound(err) {
			t.Errorf("expected %q to be deleted on disable flip, got %v", name, err)
		}
	}
}
