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

	witwavev1alpha1 "github.com/witwave-ai/witwave-operator/api/v1alpha1"
)

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
