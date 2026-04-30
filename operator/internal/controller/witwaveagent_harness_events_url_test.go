/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Coverage for the HARNESS_EVENTS_URL port-selection helper.
//
// The bug that motivated this test was a silent 404 storm on every
// hook-decision POST: the operator stamped HARNESS_EVENTS_URL at the
// app listener while the harness moved /internal/events/* routes to
// the metrics listener under #924. The unit-test surface protects the
// invariant: when metrics are enabled, the URL must point at the
// metrics port; when disabled, it must point at the app port.
package controller

import "testing"

func TestHarnessEventsURLMetricsOnPicksMetricsPort(t *testing.T) {
	got := harnessEventsURL(8000, 9000, true)
	want := "http://localhost:9000"
	if got != want {
		t.Errorf("metrics on: got %q, want %q", got, want)
	}
}

func TestHarnessEventsURLMetricsOffPicksAppPort(t *testing.T) {
	got := harnessEventsURL(8000, 9000, false)
	want := "http://localhost:8000"
	if got != want {
		t.Errorf("metrics off: got %q, want %q", got, want)
	}
}

func TestHarnessEventsURLPortsCanCollide(t *testing.T) {
	// Custom CRDs may set MetricsPort = HarnessPort. The helper must
	// still produce a valid URL — the metrics listener is what
	// matters, not whether it shares a port with the app one.
	got := harnessEventsURL(8000, 8000, true)
	want := "http://localhost:8000"
	if got != want {
		t.Errorf("colliding ports with metrics on: got %q, want %q", got, want)
	}
}

func TestHarnessEventsURLNonStandardPorts(t *testing.T) {
	// Operator users can override either port via spec. Helper should
	// honour whichever value lands in scope, not hard-code 8000/9000.
	if got := harnessEventsURL(7777, 9999, true); got != "http://localhost:9999" {
		t.Errorf("metrics on with custom ports: got %q", got)
	}
	if got := harnessEventsURL(7777, 9999, false); got != "http://localhost:7777" {
		t.Errorf("metrics off with custom ports: got %q", got)
	}
}
