/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package controller

import "testing"

// TestContainerMetricsPort exercises the chart-#687 / CRD-#836 resolution
// rule for the per-container Prometheus metrics listener port.
func TestContainerMetricsPort(t *testing.T) {
	cases := []struct {
		name     string
		override int32
		appPort  int32
		want     int32
	}{
		{"harness default app 8000", 0, 8000, 9000},
		{"backend default app 8010", 0, 8010, 9010},
		{"backend default app 8011", 0, 8011, 9011},
		{"legacy override wins", 9900, 8010, 9900},
		{"legacy override wins on harness", 9900, 8000, 9900},
		{"overflow caps at 65535", 0, 65000, 65535},
		{"absurd negative app falls back to 9000", 0, -1000, 9000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := containerMetricsPort(c.override, c.appPort)
			if got != c.want {
				t.Fatalf("containerMetricsPort(%d, %d) = %d, want %d",
					c.override, c.appPort, got, c.want)
			}
		})
	}
}
