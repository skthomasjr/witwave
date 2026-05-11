// Tests for the pure-helper functions in events.go that the
// `ww operator events` row renderer composes over corev1.Event
// values returned from the watch+list pipeline. Mirrors the
// table-driven shape used in operator/chart_test.go and
// cmd/snapshot_test.go; the watch / list / kubeconfig paths are
// exercised against a real cluster, so this file just pins the
// rendering contracts so a future change to which timestamp wins
// or how an InvolvedObject ref renders fails loudly here instead
// of silently changing what users see in the events table.
package operator

import (
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestEffectiveTimestamp pins the 4-branch timestamp-selection used
// by the row renderer. Contract: LastTimestamp wins when non-zero
// (the coalesced "most recent occurrence" for v1 Events); else
// FirstTimestamp; else EventTime (for events.k8s.io/v1-shaped data
// that leaks through); else CreationTimestamp. Drift here would
// silently re-sort the events table.
func TestEffectiveTimestamp(t *testing.T) {
	creation := time.Date(2026, 5, 11, 1, 0, 0, 0, time.UTC)
	first := time.Date(2026, 5, 11, 2, 0, 0, 0, time.UTC)
	eventTime := time.Date(2026, 5, 11, 3, 0, 0, 0, time.UTC)
	last := time.Date(2026, 5, 11, 4, 0, 0, 0, time.UTC)

	cases := []struct {
		name string
		ev   *corev1.Event
		want time.Time
	}{
		{
			"LastTimestamp wins when all four set",
			&corev1.Event{
				ObjectMeta:     metav1.ObjectMeta{CreationTimestamp: metav1.Time{Time: creation}},
				FirstTimestamp: metav1.Time{Time: first},
				EventTime:      metav1.MicroTime{Time: eventTime},
				LastTimestamp:  metav1.Time{Time: last},
			},
			last,
		},
		{
			"FirstTimestamp wins when LastTimestamp zero",
			&corev1.Event{
				ObjectMeta:     metav1.ObjectMeta{CreationTimestamp: metav1.Time{Time: creation}},
				FirstTimestamp: metav1.Time{Time: first},
				EventTime:      metav1.MicroTime{Time: eventTime},
			},
			first,
		},
		{
			"EventTime wins when First+Last zero",
			&corev1.Event{
				ObjectMeta: metav1.ObjectMeta{CreationTimestamp: metav1.Time{Time: creation}},
				EventTime:  metav1.MicroTime{Time: eventTime},
			},
			eventTime,
		},
		{
			"CreationTimestamp is final fallback",
			&corev1.Event{
				ObjectMeta: metav1.ObjectMeta{CreationTimestamp: metav1.Time{Time: creation}},
			},
			creation,
		},
		{
			"all-zero event returns zero time",
			&corev1.Event{},
			time.Time{},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := effectiveTimestamp(tc.ev)
			if !got.Equal(tc.want) {
				t.Errorf("effectiveTimestamp = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestAgeOrExact pins the duration→display formatter used in the
// AGE column. Contract:
//   - zero time     → "<unknown>"
//   - future time   → RFC3339 (clock-skew safety, prevents "-3s")
//   - >24h old      → RFC3339 (long-lived events stay deterministic)
//   - <1m old       → "Ns"
//   - <1h old       → "Nm"
//   - 1-24h old     → "Nh"
//
// Drift here would change every row in the AGE column.
func TestAgeOrExact(t *testing.T) {
	now := time.Now()

	t.Run("zero time returns unknown sentinel", func(t *testing.T) {
		got := ageOrExact(time.Time{})
		if got != "<unknown>" {
			t.Errorf("ageOrExact(zero) = %q, want %q", got, "<unknown>")
		}
	})

	t.Run("future time falls back to RFC3339", func(t *testing.T) {
		future := now.Add(30 * time.Second)
		got := ageOrExact(future)
		// future.Format with our chosen layout
		want := future.Format("2006-01-02T15:04:05Z07:00")
		if got != want {
			t.Errorf("ageOrExact(future) = %q, want %q", got, want)
		}
	})

	t.Run("older than 24h falls back to RFC3339", func(t *testing.T) {
		old := now.Add(-48 * time.Hour)
		got := ageOrExact(old)
		want := old.Format("2006-01-02T15:04:05Z07:00")
		if got != want {
			t.Errorf("ageOrExact(48h ago) = %q, want %q", got, want)
		}
	})

	t.Run("seconds-old renders Ns", func(t *testing.T) {
		got := ageOrExact(now.Add(-10 * time.Second))
		// Allow either "10s" or "11s" — the test runs after Now()
		// captured so there's <1s drift either way.
		if got != "10s" && got != "11s" && got != "9s" {
			t.Errorf("ageOrExact(10s ago) = %q, want ~10s", got)
		}
	})

	t.Run("minutes-old renders Nm", func(t *testing.T) {
		got := ageOrExact(now.Add(-5 * time.Minute))
		if got != "5m" && got != "4m" {
			t.Errorf("ageOrExact(5m ago) = %q, want ~5m", got)
		}
	})

	t.Run("hours-old renders Nh", func(t *testing.T) {
		got := ageOrExact(now.Add(-3 * time.Hour))
		if got != "3h" && got != "2h" {
			t.Errorf("ageOrExact(3h ago) = %q, want ~3h", got)
		}
	})

	t.Run("just-now renders 0s", func(t *testing.T) {
		got := ageOrExact(now)
		// The function's `time.Since(t)` call may return a positive
		// small duration by the time it runs, but it's <1m → Ns format.
		if !strings.HasSuffix(got, "s") {
			t.Errorf("ageOrExact(now) = %q, want a seconds-formatted string", got)
		}
	})
}

// TestDescribeObject pins the InvolvedObject ref formatter used in
// the OBJECT column. Contract:
//   - "Kind/Name"
//   - "?/Name" or "Kind/?" when one is empty
//   - "?/?" when both empty
//   - " (ns)" suffix only when InvolvedObject.Namespace is set AND
//     differs from the Event.Namespace (cross-namespace refs are
//     rare but real)
//
// Drift here would change how every event row identifies its object.
func TestDescribeObject(t *testing.T) {
	cases := []struct {
		name string
		ev   *corev1.Event
		want string
	}{
		{
			"normal kind/name same namespace",
			&corev1.Event{
				ObjectMeta: metav1.ObjectMeta{Namespace: "witwave"},
				InvolvedObject: corev1.ObjectReference{
					Kind: "Pod", Name: "harness-x", Namespace: "witwave",
				},
			},
			"Pod/harness-x",
		},
		{
			"empty kind renders question mark",
			&corev1.Event{
				InvolvedObject: corev1.ObjectReference{Name: "harness-x"},
			},
			"?/harness-x",
		},
		{
			"empty name renders question mark",
			&corev1.Event{
				InvolvedObject: corev1.ObjectReference{Kind: "Pod"},
			},
			"Pod/?",
		},
		{
			"both empty renders ?/?",
			&corev1.Event{
				InvolvedObject: corev1.ObjectReference{},
			},
			"?/?",
		},
		{
			"cross-namespace ref appends (ns)",
			&corev1.Event{
				ObjectMeta: metav1.ObjectMeta{Namespace: "witwave"},
				InvolvedObject: corev1.ObjectReference{
					Kind: "Secret", Name: "creds", Namespace: "kube-system",
				},
			},
			"Secret/creds (kube-system)",
		},
		{
			"empty involvedObject namespace does not append suffix",
			&corev1.Event{
				ObjectMeta: metav1.ObjectMeta{Namespace: "witwave"},
				InvolvedObject: corev1.ObjectReference{
					Kind: "Pod", Name: "harness-x", Namespace: "",
				},
			},
			"Pod/harness-x",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := describeObject(tc.ev)
			if got != tc.want {
				t.Errorf("describeObject = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestTruncateMessage pins the MESSAGE column truncator. Contract:
//   - newlines replaced with single space (events frequently embed
//     newlines in their Message; the row renderer requires a one-liner)
//   - length <= max returned verbatim
//   - length > max truncated to (max-1) chars plus ellipsis '…'
//
// Drift would change every multi-line event's table appearance.
func TestTruncateMessage(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		max  int
		want string
	}{
		{"empty stays empty", "", 10, ""},
		{"short string returned verbatim", "hello", 10, "hello"},
		{"length-equal-max returned verbatim", "0123456789", 10, "0123456789"},
		{"length-over-max truncated with ellipsis", "0123456789x", 10, "012345678…"},
		{"newline replaced with space (no truncation)", "line one\nline two", 30, "line one line two"},
		{
			"multiple newlines all replaced with spaces",
			"a\nb\nc\nd",
			20,
			"a b c d",
		},
		{
			"newline replacement happens before length check",
			"abc\ndef\nghi", 7, // post-replace length is 11 → truncate to 6+…
			"abc de…",
		},
		{
			"max smaller than ellipsis still produces something",
			"hello world", 2,
			// max=2 → take msg[:1] + "…" → "h…"
			"h…",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := truncateMessage(tc.msg, tc.max)
			if got != tc.want {
				t.Errorf("truncateMessage(%q, %d) = %q, want %q", tc.msg, tc.max, got, tc.want)
			}
		})
	}
}
