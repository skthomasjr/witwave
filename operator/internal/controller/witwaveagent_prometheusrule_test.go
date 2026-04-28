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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	witwavev1alpha1 "github.com/witwave-ai/witwave-operator/api/v1alpha1"
)

func prAgent(name string) *witwavev1alpha1.WitwaveAgent {
	return &witwavev1alpha1.WitwaveAgent{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
	}
}

func TestPrometheusRuleEnabled(t *testing.T) {
	cases := []struct {
		name    string
		agent   *witwavev1alpha1.WitwaveAgent
		want    bool
	}{
		{name: "no spec", agent: prAgent("iris"), want: false},
		{
			name: "enabled false",
			agent: func() *witwavev1alpha1.WitwaveAgent {
				a := prAgent("iris")
				a.Spec.PrometheusRule = &witwavev1alpha1.PrometheusRuleSpec{Enabled: false}
				return a
			}(),
			want: false,
		},
		{
			name: "enabled true",
			agent: func() *witwavev1alpha1.WitwaveAgent {
				a := prAgent("iris")
				a.Spec.PrometheusRule = &witwavev1alpha1.PrometheusRuleSpec{Enabled: true}
				return a
			}(),
			want: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := prometheusRuleEnabled(tc.agent); got != tc.want {
				t.Errorf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestBuildPrometheusRule_DisabledReturnsNil(t *testing.T) {
	a := prAgent("iris")
	if got := buildPrometheusRule(a); got != nil {
		t.Errorf("expected nil PrometheusRule when not enabled, got %+v", got)
	}
}

func TestBuildPrometheusRule_NameAndLabels(t *testing.T) {
	a := prAgent("iris")
	a.Spec.PrometheusRule = &witwavev1alpha1.PrometheusRuleSpec{
		Enabled:          true,
		AdditionalLabels: map[string]string{"release": "kube-prometheus-stack"},
	}
	pr := buildPrometheusRule(a)
	if pr == nil {
		t.Fatal("expected PrometheusRule, got nil")
	}
	if pr.GetName() != "iris-witwave" {
		t.Errorf("name: got %q want iris-witwave", pr.GetName())
	}
	if pr.GetNamespace() != "default" {
		t.Errorf("namespace: got %q want default", pr.GetNamespace())
	}
	labels := pr.GetLabels()
	if labels[labelPartOf] != partOf {
		t.Errorf("part-of label missing: %v", labels)
	}
	if labels["release"] != "kube-prometheus-stack" {
		t.Errorf("additionalLabels not merged: %v", labels)
	}
	if pr.GroupVersionKind() != prometheusRuleGVK {
		t.Errorf("GVK: got %v want %v", pr.GroupVersionKind(), prometheusRuleGVK)
	}
}

func TestBuildPrometheusRule_HasExpectedAlerts(t *testing.T) {
	a := prAgent("iris")
	a.Spec.PrometheusRule = &witwavev1alpha1.PrometheusRuleSpec{Enabled: true}
	pr := buildPrometheusRule(a)
	if pr == nil {
		t.Fatal("expected PrometheusRule")
	}
	spec, ok := pr.Object["spec"].(map[string]interface{})
	if !ok {
		t.Fatalf("spec missing or wrong shape: %+v", pr.Object["spec"])
	}
	groups, ok := spec["groups"].([]interface{})
	if !ok {
		t.Fatalf("spec.groups missing: %+v", spec)
	}
	want := map[string]bool{
		"WitwaveBackendDown":               false,
		"WitwaveHookDenialSpike":           false,
		"WitwaveMcpAuthFailure":            false,
		"WitwaveWebhookTimeout":            false,
		"WitwaveLockWaitSaturation":        false,
		"WitwavePVCFillWarning":            false,
		"WitwavePVCFillCritical":           false,
		"WitwaveWebhookRetryBytesHalfFull": false,
		"WitwaveA2ALatencyHigh":            false,
		"WitwaveEventValidationErrors":     false,
	}
	for _, gAny := range groups {
		g, ok := gAny.(map[string]interface{})
		if !ok {
			continue
		}
		rules, _ := g["rules"].([]map[string]interface{})
		for _, rule := range rules {
			alert, _ := rule["alert"].(string)
			if _, expected := want[alert]; expected {
				want[alert] = true
			}
		}
	}
	for alert, found := range want {
		if !found {
			t.Errorf("expected alert %q to be rendered", alert)
		}
	}
}
