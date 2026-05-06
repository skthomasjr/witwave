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
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	witwavev1alpha1 "github.com/witwave-ai/witwave-operator/api/v1alpha1"
)

// ssaCreateOnMissing intercepts Patch(client.Apply) calls and falls back
// to Create when the underlying fake client returns NotFound — the fake
// client's SSA support does not synthesise objects on first apply, but
// our reconciler relies on apply-creates-or-updates semantics. This
// helper makes the fake client behave like the real apiserver for the
// scope of these tests.
func ssaCreateOnMissing() interceptor.Funcs {
	return interceptor.Funcs{
		Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			err := c.Patch(ctx, obj, patch, opts...)
			if err != nil && apierrors.IsNotFound(err) {
				// Strip SSA-only fields the fake Create rejects.
				obj.SetResourceVersion("")
				return c.Create(ctx, obj)
			}
			return err
		},
	}
}

func TestEvaluateDashboardIngressAuth(t *testing.T) {
	cases := []struct {
		name  string
		agent *witwavev1alpha1.WitwaveAgent
		want  DashboardIngressAuthStatus
	}{
		{
			name:  "nil agent",
			agent: nil,
			want:  DashboardIngressAuthStatusDisabled,
		},
		{
			name:  "no dashboard",
			agent: &witwavev1alpha1.WitwaveAgent{},
			want:  DashboardIngressAuthStatusDisabled,
		},
		{
			name: "dashboard without ingress",
			agent: &witwavev1alpha1.WitwaveAgent{
				Spec: witwavev1alpha1.WitwaveAgentSpec{Dashboard: &witwavev1alpha1.DashboardSpec{Enabled: true}},
			},
			want: DashboardIngressAuthStatusDisabled,
		},
		{
			name: "ingress disabled",
			agent: &witwavev1alpha1.WitwaveAgent{
				Spec: witwavev1alpha1.WitwaveAgentSpec{
					Dashboard: &witwavev1alpha1.DashboardSpec{
						Enabled: true,
						Ingress: &witwavev1alpha1.DashboardIngressSpec{Enabled: false},
					},
				},
			},
			want: DashboardIngressAuthStatusDisabled,
		},
		{
			name: "ingress enabled without auth is fail-closed",
			agent: &witwavev1alpha1.WitwaveAgent{
				Spec: witwavev1alpha1.WitwaveAgentSpec{
					Dashboard: &witwavev1alpha1.DashboardSpec{
						Enabled: true,
						Ingress: &witwavev1alpha1.DashboardIngressSpec{Enabled: true},
					},
				},
			},
			want: DashboardIngressAuthStatusMissingAuth,
		},
		{
			name: "auth.mode=none is explicit opt-out",
			agent: &witwavev1alpha1.WitwaveAgent{
				Spec: witwavev1alpha1.WitwaveAgentSpec{
					Dashboard: &witwavev1alpha1.DashboardSpec{
						Enabled: true,
						Ingress: &witwavev1alpha1.DashboardIngressSpec{
							Enabled: true,
							Auth:    &witwavev1alpha1.DashboardAuthSpec{Mode: "none"},
						},
					},
				},
			},
			want: DashboardIngressAuthStatusUnauthenticated,
		},
		{
			name: "auth.mode=basic renders with basic-auth",
			agent: &witwavev1alpha1.WitwaveAgent{
				Spec: witwavev1alpha1.WitwaveAgentSpec{
					Dashboard: &witwavev1alpha1.DashboardSpec{
						Enabled: true,
						Ingress: &witwavev1alpha1.DashboardIngressSpec{
							Enabled: true,
							Auth:    &witwavev1alpha1.DashboardAuthSpec{Mode: "basic"},
						},
					},
				},
			},
			want: DashboardIngressAuthStatusBasic,
		},
		{
			name: "unknown mode is treated as missing-auth",
			agent: &witwavev1alpha1.WitwaveAgent{
				Spec: witwavev1alpha1.WitwaveAgentSpec{
					Dashboard: &witwavev1alpha1.DashboardSpec{
						Enabled: true,
						Ingress: &witwavev1alpha1.DashboardIngressSpec{
							Enabled: true,
							Auth:    &witwavev1alpha1.DashboardAuthSpec{Mode: "invalid"},
						},
					},
				},
			},
			want: DashboardIngressAuthStatusMissingAuth,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EvaluateDashboardIngressAuth(tc.agent); got != tc.want {
				t.Errorf("got %q want %q", got, tc.want)
			}
		})
	}
}

// helper builders for the new render-path tests

func diAgent(name string) *witwavev1alpha1.WitwaveAgent {
	return &witwavev1alpha1.WitwaveAgent{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: witwavev1alpha1.WitwaveAgentSpec{
			Dashboard: &witwavev1alpha1.DashboardSpec{Enabled: true, Port: 80},
		},
	}
}

func TestBuildDashboardIngress_DisabledReturnsNil(t *testing.T) {
	cases := map[string]*witwavev1alpha1.WitwaveAgent{
		"nil agent":     nil,
		"nil dashboard": {ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "ns"}},
		"dashboard disabled": {
			ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "ns"},
			Spec: witwavev1alpha1.WitwaveAgentSpec{
				Dashboard: &witwavev1alpha1.DashboardSpec{Enabled: false},
			},
		},
		"ingress disabled": func() *witwavev1alpha1.WitwaveAgent {
			a := diAgent("x")
			a.Spec.Dashboard.Ingress = &witwavev1alpha1.DashboardIngressSpec{Enabled: false}
			return a
		}(),
		"missing auth.mode": func() *witwavev1alpha1.WitwaveAgent {
			a := diAgent("x")
			a.Spec.Dashboard.Ingress = &witwavev1alpha1.DashboardIngressSpec{Enabled: true}
			return a
		}(),
		"basic without secret name": func() *witwavev1alpha1.WitwaveAgent {
			a := diAgent("x")
			a.Spec.Dashboard.Ingress = &witwavev1alpha1.DashboardIngressSpec{
				Enabled: true,
				Auth:    &witwavev1alpha1.DashboardAuthSpec{Mode: "basic"},
			}
			return a
		}(),
	}
	for name, agent := range cases {
		t.Run(name, func(t *testing.T) {
			if got := buildDashboardIngress(agent); got != nil {
				t.Fatalf("expected nil Ingress, got %+v", got)
			}
		})
	}
}

func TestBuildDashboardIngress_NoneRendersWithoutAuthAnnotations(t *testing.T) {
	a := diAgent("iris")
	cls := "nginx"
	a.Spec.Dashboard.Ingress = &witwavev1alpha1.DashboardIngressSpec{
		Enabled:   true,
		ClassName: &cls,
		Host:      "iris.example.com",
		Auth:      &witwavev1alpha1.DashboardAuthSpec{Mode: "none"},
	}
	ing := buildDashboardIngress(a)
	if ing == nil {
		t.Fatal("expected Ingress, got nil")
	}
	if ing.Name != "iris-dashboard" {
		t.Errorf("name: got %q want iris-dashboard", ing.Name)
	}
	if ing.Namespace != "default" {
		t.Errorf("namespace: got %q want default", ing.Namespace)
	}
	if ing.Spec.IngressClassName == nil || *ing.Spec.IngressClassName != "nginx" {
		t.Errorf("ingressClassName: %+v want nginx", ing.Spec.IngressClassName)
	}
	if len(ing.Spec.Rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(ing.Spec.Rules))
	}
	rule := ing.Spec.Rules[0]
	if rule.Host != "iris.example.com" {
		t.Errorf("host: got %q want iris.example.com", rule.Host)
	}
	if rule.HTTP == nil || len(rule.HTTP.Paths) != 1 {
		t.Fatalf("expected 1 path, got %+v", rule.HTTP)
	}
	p := rule.HTTP.Paths[0]
	if p.Path != "/" {
		t.Errorf("path: got %q want /", p.Path)
	}
	if p.PathType == nil || *p.PathType != networkingv1.PathTypePrefix {
		t.Errorf("pathType: %+v want Prefix", p.PathType)
	}
	if p.Backend.Service == nil || p.Backend.Service.Name != "iris-dashboard" || p.Backend.Service.Port.Number != 80 {
		t.Errorf("backend: %+v want service iris-dashboard:80", p.Backend.Service)
	}
	for k := range ing.Annotations {
		if k == "nginx.ingress.kubernetes.io/auth-type" || k == "nginx.ingress.kubernetes.io/auth-secret" {
			t.Errorf("auth.mode=none must not stamp auth annotation %q", k)
		}
	}
}

func TestBuildDashboardIngress_BasicStampsAuthAnnotations(t *testing.T) {
	a := diAgent("iris")
	a.Spec.Dashboard.Ingress = &witwavev1alpha1.DashboardIngressSpec{
		Enabled: true,
		Host:    "iris.example.com",
		Auth: &witwavev1alpha1.DashboardAuthSpec{
			Mode:                "basic",
			BasicAuthSecretName: "my-htpasswd",
		},
	}
	ing := buildDashboardIngress(a)
	if ing == nil {
		t.Fatal("expected Ingress, got nil")
	}
	if got := ing.Annotations["nginx.ingress.kubernetes.io/auth-type"]; got != "basic" {
		t.Errorf("auth-type: got %q want basic", got)
	}
	if got := ing.Annotations["nginx.ingress.kubernetes.io/auth-secret"]; got != "my-htpasswd" {
		t.Errorf("auth-secret: got %q want my-htpasswd", got)
	}
	if got := ing.Annotations["nginx.ingress.kubernetes.io/auth-realm"]; got != "witwave dashboard" {
		t.Errorf("auth-realm: got %q want witwave dashboard", got)
	}
}

func TestBuildDashboardIngress_TLSPath(t *testing.T) {
	a := diAgent("iris")
	a.Spec.Dashboard.Ingress = &witwavev1alpha1.DashboardIngressSpec{
		Enabled: true,
		Host:    "iris.example.com",
		Auth:    &witwavev1alpha1.DashboardAuthSpec{Mode: "none"},
		TLS:     &witwavev1alpha1.DashboardIngressTLSSpec{SecretName: "iris-tls"},
	}
	ing := buildDashboardIngress(a)
	if ing == nil {
		t.Fatal("expected Ingress, got nil")
	}
	if len(ing.Spec.TLS) != 1 {
		t.Fatalf("expected 1 TLS entry, got %d", len(ing.Spec.TLS))
	}
	if ing.Spec.TLS[0].SecretName != "iris-tls" {
		t.Errorf("tls.secretName: got %q want iris-tls", ing.Spec.TLS[0].SecretName)
	}
	if len(ing.Spec.TLS[0].Hosts) != 1 || ing.Spec.TLS[0].Hosts[0] != "iris.example.com" {
		t.Errorf("tls.hosts: got %v want [iris.example.com]", ing.Spec.TLS[0].Hosts)
	}
}

// reconciler-level tests using a fake client

func newDashboardIngressTestReconciler(t *testing.T, agent *witwavev1alpha1.WitwaveAgent) (*WitwaveAgentReconciler, client.Client, *record.FakeRecorder) {
	t.Helper()
	sch := runtime.NewScheme()
	if err := scheme.AddToScheme(sch); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	if err := witwavev1alpha1.AddToScheme(sch); err != nil {
		t.Fatalf("add witwave scheme: %v", err)
	}
	c := fake.NewClientBuilder().
		WithScheme(sch).
		WithObjects(agent).
		WithInterceptorFuncs(ssaCreateOnMissing()).
		Build()
	rec := record.NewFakeRecorder(16)
	r := &WitwaveAgentReconciler{
		Client:    c,
		APIReader: c,
		Scheme:    sch,
		Recorder:  rec,
	}
	return r, c, rec
}

func TestReconcileDashboardIngress_DisabledNoIngressCreated(t *testing.T) {
	a := &witwavev1alpha1.WitwaveAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "iris", Namespace: "default"},
	}
	r, c, _ := newDashboardIngressTestReconciler(t, a)
	if err := r.reconcileDashboardIngress(context.Background(), a); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got := &networkingv1.Ingress{}
	err := c.Get(context.Background(),
		client.ObjectKey{Namespace: "default", Name: "iris-dashboard"}, got)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expected NotFound, got %v (obj=%+v)", err, got)
	}
}

func TestReconcileDashboardIngress_MissingAuthSkipsAndEvent(t *testing.T) {
	a := &witwavev1alpha1.WitwaveAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "iris", Namespace: "default"},
		Spec: witwavev1alpha1.WitwaveAgentSpec{
			Dashboard: &witwavev1alpha1.DashboardSpec{
				Enabled: true,
				Ingress: &witwavev1alpha1.DashboardIngressSpec{Enabled: true},
			},
		},
	}
	r, c, rec := newDashboardIngressTestReconciler(t, a)
	if err := r.reconcileDashboardIngress(context.Background(), a); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got := &networkingv1.Ingress{}
	if err := c.Get(context.Background(),
		client.ObjectKey{Namespace: "default", Name: "iris-dashboard"}, got); !apierrors.IsNotFound(err) {
		t.Fatalf("expected no Ingress when auth is missing, got %v", err)
	}
	select {
	case ev := <-rec.Events:
		if ev == "" {
			t.Errorf("expected DashboardIngressAuthRequired event, got empty string")
		}
	default:
		t.Errorf("expected an event for missing-auth state")
	}
}

func TestReconcileDashboardIngress_NoneRendersIngress(t *testing.T) {
	a := &witwavev1alpha1.WitwaveAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "iris", Namespace: "default", UID: "uid-iris"},
		Spec: witwavev1alpha1.WitwaveAgentSpec{
			Dashboard: &witwavev1alpha1.DashboardSpec{
				Enabled: true,
				Ingress: &witwavev1alpha1.DashboardIngressSpec{
					Enabled: true,
					Host:    "iris.example.com",
					Auth:    &witwavev1alpha1.DashboardAuthSpec{Mode: "none"},
				},
			},
		},
	}
	r, c, _ := newDashboardIngressTestReconciler(t, a)
	if err := r.reconcileDashboardIngress(context.Background(), a); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got := &networkingv1.Ingress{}
	if err := c.Get(context.Background(),
		client.ObjectKey{Namespace: "default", Name: "iris-dashboard"}, got); err != nil {
		t.Fatalf("expected Ingress, got %v", err)
	}
	if len(got.Spec.Rules) != 1 || got.Spec.Rules[0].Host != "iris.example.com" {
		t.Errorf("unexpected rules: %+v", got.Spec.Rules)
	}
	if got.Annotations["nginx.ingress.kubernetes.io/auth-type"] != "" {
		t.Errorf("auth.mode=none must not stamp auth-type annotation")
	}
}

func TestReconcileDashboardIngress_BasicWithSecretRendersWithAnnotations(t *testing.T) {
	a := &witwavev1alpha1.WitwaveAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "iris", Namespace: "default", UID: "uid-iris"},
		Spec: witwavev1alpha1.WitwaveAgentSpec{
			Dashboard: &witwavev1alpha1.DashboardSpec{
				Enabled: true,
				Ingress: &witwavev1alpha1.DashboardIngressSpec{
					Enabled: true,
					Host:    "iris.example.com",
					Auth: &witwavev1alpha1.DashboardAuthSpec{
						Mode:                "basic",
						BasicAuthSecretName: "iris-htpasswd",
					},
				},
			},
		},
	}
	r, c, _ := newDashboardIngressTestReconciler(t, a)
	if err := r.reconcileDashboardIngress(context.Background(), a); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got := &networkingv1.Ingress{}
	if err := c.Get(context.Background(),
		client.ObjectKey{Namespace: "default", Name: "iris-dashboard"}, got); err != nil {
		t.Fatalf("expected Ingress, got %v", err)
	}
	if got.Annotations["nginx.ingress.kubernetes.io/auth-type"] != "basic" {
		t.Errorf("auth-type annotation missing/wrong: %v", got.Annotations)
	}
	if got.Annotations["nginx.ingress.kubernetes.io/auth-secret"] != "iris-htpasswd" {
		t.Errorf("auth-secret annotation missing/wrong: %v", got.Annotations)
	}
}

func TestReconcileDashboardIngress_BasicWithoutSecretFailsClosed(t *testing.T) {
	a := &witwavev1alpha1.WitwaveAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "iris", Namespace: "default"},
		Spec: witwavev1alpha1.WitwaveAgentSpec{
			Dashboard: &witwavev1alpha1.DashboardSpec{
				Enabled: true,
				Ingress: &witwavev1alpha1.DashboardIngressSpec{
					Enabled: true,
					Host:    "iris.example.com",
					Auth:    &witwavev1alpha1.DashboardAuthSpec{Mode: "basic"},
				},
			},
		},
	}
	r, c, rec := newDashboardIngressTestReconciler(t, a)
	if err := r.reconcileDashboardIngress(context.Background(), a); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got := &networkingv1.Ingress{}
	if err := c.Get(context.Background(),
		client.ObjectKey{Namespace: "default", Name: "iris-dashboard"}, got); !apierrors.IsNotFound(err) {
		t.Fatalf("expected fail-closed (no Ingress), got %v", err)
	}
	select {
	case <-rec.Events:
		// ok
	default:
		t.Errorf("expected DashboardIngressBasicAuthSecretRequired event")
	}
}

func TestReconcileDashboardIngress_DisableFlipDeletesPreviouslyOwned(t *testing.T) {
	// Pre-seed an owned Ingress, then reconcile a disabled-spec agent and
	// confirm it is deleted.
	a := &witwavev1alpha1.WitwaveAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "iris", Namespace: "default", UID: "uid-iris"},
	}
	sch := runtime.NewScheme()
	if err := scheme.AddToScheme(sch); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	if err := witwavev1alpha1.AddToScheme(sch); err != nil {
		t.Fatalf("add witwave scheme: %v", err)
	}
	tru := true
	owned := &networkingv1.Ingress{
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
	c := fake.NewClientBuilder().
		WithScheme(sch).
		WithObjects(a, owned).
		Build()
	r := &WitwaveAgentReconciler{
		Client:    c,
		APIReader: c,
		Scheme:    sch,
		Recorder:  record.NewFakeRecorder(8),
	}
	if err := r.reconcileDashboardIngress(context.Background(), a); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got := &networkingv1.Ingress{}
	if err := c.Get(context.Background(),
		client.ObjectKey{Namespace: "default", Name: "iris-dashboard"}, got); !apierrors.IsNotFound(err) {
		t.Fatalf("expected previously-owned Ingress to be deleted, got %v", err)
	}
}
