// Tests for the pure-helper functions in snapshot.go that the
// `ww jobs/tasks/triggers/continuations list|view` commands compose
// over `ww` snapshot fetches. Mirrors the table-driven shape used in
// validate_test.go and agent_test.go.
package cmd

import (
	"reflect"
	"testing"
)

// TestFirstNonEmpty pins the variadic-args first-non-empty fallback
// helper used pervasively in snapshot rendering to choose a display
// label when multiple alias keys may exist for the same field
// (e.g. "name" / "id" / "displayName"). Drift here would silently
// change which key wins when more than one is set.
func TestFirstNonEmpty(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"zero args returns empty", nil, ""},
		{"single empty returns empty", []string{""}, ""},
		{"all empty returns empty", []string{"", "", ""}, ""},
		{"single non-empty returns it", []string{"foo"}, "foo"},
		{"first non-empty wins", []string{"foo", "bar"}, "foo"},
		{"first non-empty after empties wins", []string{"", "", "first-real", "second"}, "first-real"},
		{"single-space is non-empty", []string{"", " ", "x"}, " "},
		{"multibyte first wins", []string{"", "café"}, "café"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := firstNonEmpty(tc.in...)
			if got != tc.want {
				t.Errorf("firstNonEmpty(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestKeyLess pins the snapshot-render key-ordering comparator that
// the `printView` helper uses to put well-known fields ("name",
// "status", etc.) before unknown / extension fields. The contract is:
// keys present in the priority map sort by their priority value;
// keys not in the map sort lexicographically AFTER all priority keys.
// Drift here would silently rearrange every `ww jobs view` table.
func TestKeyLess(t *testing.T) {
	prio := map[string]int{"name": 0, "status": 1, "kind": 2}
	cases := []struct {
		name string
		a    string
		b    string
		want bool
	}{
		{"both in priority — lower-prio wins", "name", "status", true},
		{"both in priority — higher-prio loses", "status", "name", false},
		{"both in priority — equal-prio is false (a<b not by lex)", "name", "name", false},
		{"a in priority, b not — a wins", "name", "zzz", true},
		{"a not in priority, b is — a loses", "zzz", "name", false},
		{"neither in priority — lex order", "alpha", "beta", true},
		{"neither in priority — lex reverse", "beta", "alpha", false},
		{"neither in priority — equal is false", "x", "x", false},
		{"empty string vs priority key — empty loses", "", "name", false},
		{"empty string vs unknown key — empty wins lex", "", "x", true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := keyLess(tc.a, tc.b, prio)
			if got != tc.want {
				t.Errorf("keyLess(%q, %q, prio) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// TestSortKeys pins the in-place insertion sort that orders snapshot
// keys for the `ww jobs view` rendering pipeline. Wraps keyLess (covered
// above) — this test exercises the end-to-end ordering on representative
// inputs so a future swap to `sort.SliceStable` or a different comparator
// keeps the same observable order.
func TestSortKeys(t *testing.T) {
	prio := map[string]int{"name": 0, "status": 1, "kind": 2}
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"empty slice stays empty", []string{}, []string{}},
		{"single element unchanged", []string{"x"}, []string{"x"}},
		{"all priority keys ordered by priority", []string{"status", "kind", "name"}, []string{"name", "status", "kind"}},
		{"priority keys precede unknown keys", []string{"zeta", "name", "alpha"}, []string{"name", "alpha", "zeta"}},
		{"all unknown keys sort lex", []string{"gamma", "alpha", "beta"}, []string{"alpha", "beta", "gamma"}},
		{"mixed: priorities first, unknown lex tail", []string{"zeta", "kind", "alpha", "name", "beta"}, []string{"name", "kind", "alpha", "beta", "zeta"}},
		{"already-sorted stays sorted", []string{"name", "status", "alpha"}, []string{"name", "status", "alpha"}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := append([]string{}, tc.in...)
			sortKeys(got, prio)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("sortKeys(%v, prio) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestFormatTime pins the "LAST_FIRE"-cell renderer used in the
// trigger / continuation table columns. Contract:
//   - nil → "-"
//   - non-string → JSON encoding (covers numbers, bools, objects)
//   - string that parses as RFC3339 → returned verbatim
//   - string that does NOT parse as RFC3339 → also returned verbatim
//
// Both string branches return the input unchanged today; the test
// pins that observable behaviour so a future "humanise non-RFC3339"
// rewrite is a deliberate decision rather than a quiet drift.
func TestFormatTime(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"nil returns dash", nil, "-"},
		{"valid RFC3339 returned verbatim", "2025-01-02T03:04:05Z", "2025-01-02T03:04:05Z"},
		{"invalid RFC3339 string returned verbatim", "yesterday", "yesterday"},
		{"empty string returned verbatim", "", ""},
		{"int marshals as JSON number", 42, "42"},
		{"bool marshals as JSON bool", true, "true"},
		{"map marshals as JSON object", map[string]any{"k": "v"}, `{"k":"v"}`},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := formatTime(tc.in)
			if got != tc.want {
				t.Errorf("formatTime(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestFindEntryByName pins the snapshot-list lookup used by
// `ww jobs view <name>` etc. to resolve a user-supplied name to a
// single entry. Contract:
//   - default (no keys passed) looks at the "name" field
//   - custom keys are tried in order; first match wins
//   - non-string field values do not match (string-typed lookup only)
//   - no match returns nil
func TestFindEntryByName(t *testing.T) {
	a := snapshotEntry{"name": "alpha", "id": "id-a"}
	b := snapshotEntry{"name": "beta", "id": "id-b"}
	c := snapshotEntry{"id": "id-c"} // no "name" key
	d := snapshotEntry{"name": 42}   // non-string name
	entries := []snapshotEntry{a, b, c, d}

	t.Run("default keys find by name", func(t *testing.T) {
		got := findEntryByName(entries, "beta")
		if !reflect.DeepEqual(got, b) {
			t.Errorf("findEntryByName(entries, %q) = %v, want %v", "beta", got, b)
		}
	})

	t.Run("default keys no match returns nil", func(t *testing.T) {
		got := findEntryByName(entries, "nope")
		if got != nil {
			t.Errorf("findEntryByName(entries, %q) = %v, want nil", "nope", got)
		}
	})

	t.Run("custom key falls back to id", func(t *testing.T) {
		got := findEntryByName(entries, "id-c", "name", "id")
		if !reflect.DeepEqual(got, c) {
			t.Errorf("findEntryByName(entries, %q, name, id) = %v, want %v", "id-c", got, c)
		}
	})

	t.Run("custom key tried in order", func(t *testing.T) {
		// "alpha" matches via the first key ("name"), even though "id"
		// also exists on the entry — first key wins.
		got := findEntryByName(entries, "alpha", "name", "id")
		if !reflect.DeepEqual(got, a) {
			t.Errorf("findEntryByName(entries, %q, name, id) = %v, want %v", "alpha", got, a)
		}
	})

	t.Run("non-string field value does not match", func(t *testing.T) {
		// d has name=42 (int); the lookup is string-typed via the
		// `v.(string)` assertion in findEntryByName, so a numeric
		// "42" string should NOT match.
		got := findEntryByName(entries, "42")
		if got != nil {
			t.Errorf("findEntryByName(entries, %q) = %v, want nil (non-string field)", "42", got)
		}
	})

	t.Run("empty entries returns nil", func(t *testing.T) {
		got := findEntryByName(nil, "alpha")
		if got != nil {
			t.Errorf("findEntryByName(nil, %q) = %v, want nil", "alpha", got)
		}
	})
}
