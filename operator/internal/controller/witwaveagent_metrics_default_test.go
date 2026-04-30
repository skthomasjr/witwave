/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Coverage for metricsEnabled() — the read-side helper that gives the
// MetricsSpec.Enabled *bool its default-true semantics. The contract
// matters because the hook-events transport now depends on metrics
// being on (per #1781): a nil dereference returning false would 404
// every event POST silently.
package controller

import (
	"testing"

	witwavev1alpha1 "github.com/witwave-ai/witwave-operator/api/v1alpha1"
)

func TestMetricsEnabledNilDefaultsTrue(t *testing.T) {
	agent := &witwavev1alpha1.WitwaveAgent{
		Spec: witwavev1alpha1.WitwaveAgentSpec{
			Metrics: witwavev1alpha1.MetricsSpec{Enabled: nil},
		},
	}
	if !metricsEnabled(agent) {
		t.Errorf("metricsEnabled with nil Enabled = false; want true (default)")
	}
}

func TestMetricsEnabledExplicitTrue(t *testing.T) {
	on := true
	agent := &witwavev1alpha1.WitwaveAgent{
		Spec: witwavev1alpha1.WitwaveAgentSpec{
			Metrics: witwavev1alpha1.MetricsSpec{Enabled: &on},
		},
	}
	if !metricsEnabled(agent) {
		t.Errorf("metricsEnabled with explicit true = false; want true")
	}
}

func TestMetricsEnabledExplicitFalse(t *testing.T) {
	off := false
	agent := &witwavev1alpha1.WitwaveAgent{
		Spec: witwavev1alpha1.WitwaveAgentSpec{
			Metrics: witwavev1alpha1.MetricsSpec{Enabled: &off},
		},
	}
	if metricsEnabled(agent) {
		t.Errorf("metricsEnabled with explicit false = true; want false")
	}
}

func TestMetricsEnabledValueRendersTrueByDefault(t *testing.T) {
	agent := &witwavev1alpha1.WitwaveAgent{}
	if got := metricsEnabledValue(agent); got != "true" {
		t.Errorf("metricsEnabledValue with default spec = %q; want %q", got, "true")
	}
}
