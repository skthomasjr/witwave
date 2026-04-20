/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package controller

import (
	"errors"
	"strings"
	"testing"
	"time"

	witwavev1alpha1 "github.com/witwave-ai/witwave-operator/api/v1alpha1"
)

// TestAppendReconcileHistory_GrowsThenRotates asserts the ring's two
// invariants for #1112:
//
//  1. It grows up to witwavev1alpha1.ReconcileHistoryMax entries without
//     dropping any.
//  2. After the cap is reached, further appends push the oldest entry out
//     (FIFO rotation), preserving insertion order and keeping the cap.
func TestAppendReconcileHistory_GrowsThenRotates(t *testing.T) {
	t.Parallel()

	const max = witwavev1alpha1.ReconcileHistoryMax
	var ring []witwavev1alpha1.ReconcileHistoryEntry
	base := time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)

	// Fill up to cap — every append should add one entry.
	for i := 0; i < max; i++ {
		start := base.Add(time.Duration(i) * time.Second)
		ring = appendReconcileHistory(ring, start, time.Millisecond*time.Duration(i+1), nil)
		if got, want := len(ring), i+1; got != want {
			t.Fatalf("after %d appends: len=%d want=%d", i+1, got, want)
		}
		last := ring[len(ring)-1]
		if last.Phase != witwavev1alpha1.ReconcileHistoryPhaseSuccess {
			t.Fatalf("append %d: phase=%q want=%q", i, last.Phase, witwavev1alpha1.ReconcileHistoryPhaseSuccess)
		}
		if last.Reason != "Reconciled" {
			t.Fatalf("append %d: reason=%q want=Reconciled", i, last.Reason)
		}
		if !last.Time.Time.Equal(start) {
			t.Fatalf("append %d: time=%v want=%v", i, last.Time.Time, start)
		}
	}

	// Snapshot the first entry BEFORE we rotate so we can assert it's
	// gone after the next append. Use Time for identity — Reasons match
	// across Success entries.
	firstBeforeRotate := ring[0].Time.Time

	// One more — should evict the oldest and keep the cap.
	rotStart := base.Add(time.Duration(max) * time.Second)
	ring = appendReconcileHistory(ring, rotStart, 5*time.Millisecond, errors.New("boom"))
	if got := len(ring); got != max {
		t.Fatalf("after rotate: len=%d want=%d", got, max)
	}
	if ring[0].Time.Time.Equal(firstBeforeRotate) {
		t.Fatalf("oldest entry (time=%v) should have been evicted", firstBeforeRotate)
	}
	last := ring[len(ring)-1]
	if last.Phase != witwavev1alpha1.ReconcileHistoryPhaseError {
		t.Fatalf("rotated-in phase=%q want=%q", last.Phase, witwavev1alpha1.ReconcileHistoryPhaseError)
	}
	if last.Reason != "ReconcileFailed" {
		t.Fatalf("rotated-in reason=%q want=ReconcileFailed", last.Reason)
	}
	if last.Message != "boom" {
		t.Fatalf("rotated-in message=%q want=boom", last.Message)
	}

	// Rotate five more times; the ring should still be exactly at cap
	// and the oldest entry should keep marching forward in time.
	for i := 1; i <= 5; i++ {
		start := base.Add(time.Duration(max+i) * time.Second)
		ring = appendReconcileHistory(ring, start, time.Millisecond, nil)
		if got := len(ring); got != max {
			t.Fatalf("extra rotate %d: len=%d want=%d", i, got, max)
		}
	}
	// Ordering invariant: timestamps must be non-decreasing.
	for i := 1; i < len(ring); i++ {
		if ring[i].Time.Time.Before(ring[i-1].Time.Time) {
			t.Fatalf("ring out of order at %d: %v < %v", i, ring[i].Time.Time, ring[i-1].Time.Time)
		}
	}
}

// TestAppendReconcileHistory_TruncatesLongMessages asserts that error
// messages longer than ReconcileHistoryMessageMax bytes are clipped with
// a trailing ellipsis rather than written verbatim (keeps the status
// subresource small).
func TestAppendReconcileHistory_TruncatesLongMessages(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("x", witwavev1alpha1.ReconcileHistoryMessageMax*2)
	ring := appendReconcileHistory(nil, time.Now(), time.Millisecond, errors.New(long))
	if len(ring) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(ring))
	}
	msg := ring[0].Message
	if len(msg) > witwavev1alpha1.ReconcileHistoryMessageMax {
		t.Fatalf("message len=%d exceeds cap=%d", len(msg), witwavev1alpha1.ReconcileHistoryMessageMax)
	}
	if !strings.HasSuffix(msg, "…") {
		t.Fatalf("truncated message should end with ellipsis; got %q", msg)
	}
}
