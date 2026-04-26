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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	witwavev1alpha1 "github.com/witwave-ai/witwave-operator/api/v1alpha1"
)

// TestDashboardReconcileDisabledNoPanic covers #1660: when
// spec.dashboard.enabled is false (or .Dashboard is nil),
// buildDashboardDeployment returns nil. The apply branch must skip the
// SetControllerReference call on the nil Deployment instead of panicking,
// mirroring the existing nil-guards on desiredCM and desiredSvc.
//
// We exercise reconcileDashboardInternal directly with a fake client so
// the test asserts the panic guard without depending on envtest assets.
func TestDashboardReconcileDisabledNoPanic(t *testing.T) {
	sch := runtime.NewScheme()
	if err := scheme.AddToScheme(sch); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	if err := witwavev1alpha1.AddToScheme(sch); err != nil {
		t.Fatalf("add witwave scheme: %v", err)
	}

	cases := []struct {
		name  string
		agent *witwavev1alpha1.WitwaveAgent
	}{
		{
			name: "dashboard nil",
			agent: &witwavev1alpha1.WitwaveAgent{
				ObjectMeta: metav1.ObjectMeta{Name: "no-dash", Namespace: "default"},
			},
		},
		{
			name: "dashboard enabled=false",
			agent: &witwavev1alpha1.WitwaveAgent{
				ObjectMeta: metav1.ObjectMeta{Name: "off-dash", Namespace: "default"},
				Spec: witwavev1alpha1.WitwaveAgentSpec{
					Dashboard: &witwavev1alpha1.DashboardSpec{Enabled: false},
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := fake.NewClientBuilder().
				WithScheme(sch).
				WithObjects(tc.agent).
				Build()
			r := &WitwaveAgentReconciler{
				Client:    c,
				APIReader: c,
				Scheme:    sch,
				Recorder:  record.NewFakeRecorder(8),
			}

			defer func() {
				if rec := recover(); rec != nil {
					t.Fatalf("reconcileDashboard panicked with disabled dashboard: %v", rec)
				}
			}()
			if err := r.reconcileDashboard(context.Background(), tc.agent); err != nil {
				t.Fatalf("reconcileDashboard returned error: %v", err)
			}
		})
	}
}
