package agent

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ev builds a corev1.Event with explicitly settable timestamps and Type.
// Pure helper to keep the table-driven cases below readable.
func ev(typ string, last, et, created time.Time) corev1.Event {
	e := corev1.Event{Type: typ}
	if !last.IsZero() {
		e.LastTimestamp = metav1.Time{Time: last}
	}
	if !et.IsZero() {
		e.EventTime = metav1.MicroTime{Time: et}
	}
	if !created.IsZero() {
		e.CreationTimestamp = metav1.Time{Time: created}
	}
	return e
}

func TestFilterWarnings(t *testing.T) {
	w := corev1.Event{Type: "Warning"}
	n := corev1.Event{Type: "Normal"}
	tests := []struct {
		name string
		in   []corev1.Event
		want int
	}{
		{"empty", nil, 0},
		{"all-normal", []corev1.Event{n, n, n}, 0},
		{"all-warning", []corev1.Event{w, w}, 2},
		{"mixed", []corev1.Event{w, n, w, n, n}, 2},
		{"single-warning", []corev1.Event{w}, 1},
	}
	for _, tc := range tests {
		got := filterWarnings(tc.in)
		if len(got) != tc.want {
			t.Errorf("%s: filterWarnings len = %d, want %d", tc.name, len(got), tc.want)
		}
		for _, e := range got {
			if e.Type != "Warning" {
				t.Errorf("%s: filterWarnings yielded non-Warning event Type=%q", tc.name, e.Type)
			}
		}
	}
}

func TestFilterSince(t *testing.T) {
	cutoff := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	before := ev("Normal", cutoff.Add(-time.Hour), time.Time{}, time.Time{})
	at := ev("Normal", cutoff, time.Time{}, time.Time{})
	after := ev("Normal", cutoff.Add(time.Hour), time.Time{}, time.Time{})

	tests := []struct {
		name string
		in   []corev1.Event
		want int
	}{
		{"empty", nil, 0},
		{"all-before", []corev1.Event{before, before}, 0},
		// Boundary semantics: filterSince uses strict After(cutoff), so
		// an event exactly at the cutoff is dropped.
		{"at-boundary-excluded", []corev1.Event{at}, 0},
		{"all-after", []corev1.Event{after, after}, 2},
		{"mixed", []corev1.Event{before, at, after, before, after}, 2},
	}
	for _, tc := range tests {
		got := filterSince(tc.in, cutoff)
		if len(got) != tc.want {
			t.Errorf("%s: filterSince len = %d, want %d", tc.name, len(got), tc.want)
		}
	}
}

func TestEventTime_PrefersLastTimestamp(t *testing.T) {
	last := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	et := time.Date(2026, 5, 16, 11, 0, 0, 0, time.UTC)
	created := time.Date(2026, 5, 16, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		in   corev1.Event
		want time.Time
	}{
		{
			name: "all-three-set-prefers-LastTimestamp",
			in:   ev("Normal", last, et, created),
			want: last,
		},
		{
			name: "no-LastTimestamp-falls-back-to-EventTime",
			in:   ev("Normal", time.Time{}, et, created),
			want: et,
		},
		{
			name: "only-CreationTimestamp",
			in:   ev("Normal", time.Time{}, time.Time{}, created),
			want: created,
		},
		{
			name: "no-LastTimestamp-no-EventTime",
			in:   ev("Normal", time.Time{}, time.Time{}, created),
			want: created,
		},
	}
	for _, tc := range tests {
		got := eventTime(tc.in)
		if !got.Equal(tc.want) {
			t.Errorf("%s: eventTime = %v, want %v", tc.name, got, tc.want)
		}
	}
}
