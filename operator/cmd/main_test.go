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

package main

import (
	"strings"
	"testing"
	"time"
)

// TestValidateLeaderElectionFlags exercises the timing-relationship
// validator extracted in #1657. The validator enforces the strict
// inequality leaseDuration > renewDeadline > retryPeriod and rejects
// non-positive durations.
func TestValidateLeaderElectionFlags(t *testing.T) {
	cases := []struct {
		name        string
		lease       time.Duration
		renew       time.Duration
		retry       time.Duration
		wantErr     bool
		errContains string
	}{
		{
			name:    "controller-runtime defaults are valid",
			lease:   15 * time.Second,
			renew:   10 * time.Second,
			retry:   2 * time.Second,
			wantErr: false,
		},
		{
			name:    "widened slow-apiserver values are valid",
			lease:   60 * time.Second,
			renew:   45 * time.Second,
			retry:   5 * time.Second,
			wantErr: false,
		},
		{
			name:        "lease equal to renew is rejected",
			lease:       10 * time.Second,
			renew:       10 * time.Second,
			retry:       2 * time.Second,
			wantErr:     true,
			errContains: "lease-duration",
		},
		{
			name:        "lease less than renew is rejected",
			lease:       5 * time.Second,
			renew:       10 * time.Second,
			retry:       2 * time.Second,
			wantErr:     true,
			errContains: "lease-duration",
		},
		{
			name:        "renew equal to retry is rejected",
			lease:       15 * time.Second,
			renew:       2 * time.Second,
			retry:       2 * time.Second,
			wantErr:     true,
			errContains: "renew-deadline",
		},
		{
			name:        "renew less than retry is rejected",
			lease:       15 * time.Second,
			renew:       1 * time.Second,
			retry:       2 * time.Second,
			wantErr:     true,
			errContains: "renew-deadline",
		},
		{
			name:        "zero lease is rejected",
			lease:       0,
			renew:       10 * time.Second,
			retry:       2 * time.Second,
			wantErr:     true,
			errContains: "lease-duration",
		},
		{
			name:        "zero renew is rejected",
			lease:       15 * time.Second,
			renew:       0,
			retry:       2 * time.Second,
			wantErr:     true,
			errContains: "renew-deadline",
		},
		{
			name:        "zero retry is rejected",
			lease:       15 * time.Second,
			renew:       10 * time.Second,
			retry:       0,
			wantErr:     true,
			errContains: "retry-period",
		},
		{
			name:        "negative lease is rejected",
			lease:       -1 * time.Second,
			renew:       10 * time.Second,
			retry:       2 * time.Second,
			wantErr:     true,
			errContains: "lease-duration",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateLeaderElectionFlags(tc.lease, tc.renew, tc.retry)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for lease=%s renew=%s retry=%s, got nil",
						tc.lease, tc.renew, tc.retry)
				}
				if tc.errContains != "" && !strings.Contains(err.Error(), tc.errContains) {
					t.Fatalf("expected error containing %q, got %q", tc.errContains, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("expected no error for lease=%s renew=%s retry=%s, got %v",
					tc.lease, tc.renew, tc.retry, err)
			}
		})
	}
}
