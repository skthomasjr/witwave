// Tests for the pure-helper functions in snapshot.go that the
// `ww jobs/tasks/triggers/continuations list|view` commands compose
// over `ww` snapshot fetches. Mirrors the table-driven shape used in
// validate_test.go and agent_test.go.
package cmd

import (
	"reflect"
	"strings"
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

// TestPickField pins the variadic first-non-empty-stringified-field
// helper used by the `ww jobs/tasks/triggers/continuations list`
// row renderer (via printList). Contract:
//   - tries each candidate key in order, first non-empty wins
//   - string values: returned verbatim when non-empty, skipped when empty
//   - nil values: skipped
//   - non-string values: JSON-marshalled, then surrounding quotes
//     stripped (so "42" not `"42"`), then "null" and empty-after-trim
//     are skipped
//   - returns "" when no candidate produces a non-empty stringification
//
// Drift here would silently change which key wins per column when an
// entry has multiple aliases set (e.g. both "name" and "id"), or how
// numeric / boolean fields appear in the table.
func TestPickField(t *testing.T) {
	t.Run("first non-empty string wins", func(t *testing.T) {
		e := snapshotEntry{"a": "alpha", "b": "beta"}
		got := e.pickField("a", "b")
		if got != "alpha" {
			t.Errorf("pickField(a,b) = %q, want %q", got, "alpha")
		}
	})

	t.Run("empty string at first key falls through", func(t *testing.T) {
		e := snapshotEntry{"a": "", "b": "beta"}
		got := e.pickField("a", "b")
		if got != "beta" {
			t.Errorf("pickField(a,b) = %q, want %q", got, "beta")
		}
	})

	t.Run("missing key skipped", func(t *testing.T) {
		e := snapshotEntry{"b": "beta"}
		got := e.pickField("a", "b")
		if got != "beta" {
			t.Errorf("pickField(a,b) = %q, want %q", got, "beta")
		}
	})

	t.Run("nil value skipped", func(t *testing.T) {
		e := snapshotEntry{"a": nil, "b": "beta"}
		got := e.pickField("a", "b")
		if got != "beta" {
			t.Errorf("pickField(a,b) = %q, want %q (nil should fall through)", got, "beta")
		}
	})

	t.Run("int value JSON-marshalled and unquoted", func(t *testing.T) {
		e := snapshotEntry{"a": 42}
		got := e.pickField("a")
		if got != "42" {
			t.Errorf("pickField(a) = %q, want %q", got, "42")
		}
	})

	t.Run("float value JSON-marshalled", func(t *testing.T) {
		e := snapshotEntry{"a": 3.14}
		got := e.pickField("a")
		if got != "3.14" {
			t.Errorf("pickField(a) = %q, want %q", got, "3.14")
		}
	})

	t.Run("bool value JSON-marshalled", func(t *testing.T) {
		e := snapshotEntry{"a": true}
		got := e.pickField("a")
		if got != "true" {
			t.Errorf("pickField(a) = %q, want %q", got, "true")
		}
	})

	t.Run("zero-int value JSON-marshals to 0 not skipped", func(t *testing.T) {
		// Subtle case: 0 is a "zero value" but the stringified "0"
		// is non-empty so pickField returns it. Pin this so a future
		// "skip zero-valued numbers" refactor is a deliberate choice.
		e := snapshotEntry{"a": 0}
		got := e.pickField("a")
		if got != "0" {
			t.Errorf("pickField(a) = %q, want %q", got, "0")
		}
	})

	t.Run("no candidate matches returns empty", func(t *testing.T) {
		e := snapshotEntry{"a": "", "b": nil}
		got := e.pickField("a", "b", "c")
		if got != "" {
			t.Errorf("pickField(a,b,c) = %q, want empty", got)
		}
	})

	t.Run("empty entry returns empty", func(t *testing.T) {
		e := snapshotEntry{}
		got := e.pickField("name")
		if got != "" {
			t.Errorf("pickField(name) on empty entry = %q, want empty", got)
		}
	})

	t.Run("no candidate keys returns empty", func(t *testing.T) {
		e := snapshotEntry{"a": "alpha"}
		got := e.pickField()
		if got != "" {
			t.Errorf("pickField() = %q, want empty (no keys)", got)
		}
	})

	t.Run("single-space string is non-empty (matches FirstNonEmpty semantics)", func(t *testing.T) {
		e := snapshotEntry{"a": " "}
		got := e.pickField("a")
		if got != " " {
			t.Errorf("pickField(a) = %q, want %q", got, " ")
		}
	})

	t.Run("map value JSON-marshals to object literal", func(t *testing.T) {
		e := snapshotEntry{"a": map[string]any{"k": "v"}}
		got := e.pickField("a")
		// json.Marshal of a map is `{"k":"v"}`; strings.Trim removes
		// only leading/trailing double-quote chars, of which there
		// are none on the outer braces — the full literal stays.
		if got != `{"k":"v"}` {
			t.Errorf("pickField(a) = %q, want %q", got, `{"k":"v"}`)
		}
	})
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

// TestParseSnapshot pins the shape-normaliser the snapshot fetch path
// (fetchSnapshot -> parseSnapshot) uses to coerce every variant the
// harness can return into the canonical []snapshotEntry slice.
// Production callers are the `ww jobs/tasks/triggers/continuations
// list|view` commands; drift here would silently change which
// envelopes render as a one-row table versus a list versus a
// "schema mismatch" error.
//
// The contract has six recognised shapes plus two error branches:
//  1. empty input (after whitespace trim) → (nil, nil) — no error.
//  2. JSON list `[ {...}, {...} ]` → entries verbatim.
//  3. Object with "items": [...] → unwrap into entries.
//  4. Object with one of "jobs"/"tasks"/"triggers"/"continuations"/
//     "heartbeat" → unwrap (array form OR single-object form).
//  5. Unknown top-level keys → error mentioning observed keys (#1244).
//  6. Malformed JSON → error wrapped with "parse list:" or "parse
//     object:" prefix depending on whether the input started with
//     '[' or not.
func TestParseSnapshot(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		wantLen   int
		wantErr   bool
		wantErrIn string // substring required in err.Error() when wantErr
	}{
		// Shape 1 — empty / whitespace-only input is not an error.
		{"empty input returns nil-nil", "", 0, false, ""},
		{"whitespace-only returns nil-nil", "   \n\t  ", 0, false, ""},
		// Shape 2 — top-level JSON list.
		{"empty list returns empty slice", "[]", 0, false, ""},
		{"list of one entry", `[{"name":"a"}]`, 1, false, ""},
		{"list of three entries", `[{"name":"a"},{"name":"b"},{"name":"c"}]`, 3, false, ""},
		// Shape 3 — object with "items".
		{"items empty", `{"items":[]}`, 0, false, ""},
		{"items with two", `{"items":[{"id":"x"},{"id":"y"}]}`, 2, false, ""},
		// Shape 4 — known-key array variant.
		{"jobs array", `{"jobs":[{"name":"j1"},{"name":"j2"}]}`, 2, false, ""},
		{"tasks array", `{"tasks":[{"name":"t1"}]}`, 1, false, ""},
		{"triggers array", `{"triggers":[{"name":"tr"}]}`, 1, false, ""},
		{"continuations array", `{"continuations":[{"name":"c"}]}`, 1, false, ""},
		{"heartbeat array", `{"heartbeat":[{"name":"h1"}]}`, 1, false, ""},
		// Shape 4 (cont) — known-key single-object variant (heartbeat).
		{"heartbeat single-object", `{"heartbeat":{"name":"hb","status":"ok"}}`, 1, false, ""},
		// Shape 5 — unknown envelope produces a descriptive error that
		// surfaces the observed top-level keys (#1244).
		{"unknown envelope lists keys", `{"data":[],"meta":{}}`, 0, true, "data, meta"},
		{"unknown envelope mentions expected shapes", `{"foo":1}`, 0, true, "expected a JSON list"},
		// Shape 6 — malformed JSON wraps with prefix that names which
		// branch the parser took.
		{"malformed list prefix", `[`, 0, true, "parse list:"},
		{"malformed object prefix", `{`, 0, true, "parse object:"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseSnapshot([]byte(tc.in))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseSnapshot(%q) err = nil, want non-nil", tc.in)
				}
				if tc.wantErrIn != "" && !strings.Contains(err.Error(), tc.wantErrIn) {
					t.Errorf("parseSnapshot(%q) err = %q, want substring %q", tc.in, err.Error(), tc.wantErrIn)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseSnapshot(%q) err = %v, want nil", tc.in, err)
			}
			if len(got) != tc.wantLen {
				t.Errorf("parseSnapshot(%q) len = %d, want %d (got %v)", tc.in, len(got), tc.wantLen, got)
			}
		})
	}
}

// TestParseSnapshotSingle_FlatHeartbeatEnabled pins the contract between
// the ww CLI and the harness /heartbeat handler: harness returns a flat
// JSON object whose top-level keys are {enabled, schedule, model,
// backend_id, consensus, max_tokens, next_fire, last_fire, last_success}
// (see harness/main.py:heartbeat_handler — the response is built
// inline, not enveloped). parseSnapshotSingle decodes that body into a
// single snapshotEntry preserving those keys verbatim.
//
// Regression test for the 2026-05-16 06:30Z bug-work run: before the
// fix, runHeartbeatView called the envelope-only parseSnapshot which
// rejected the flat shape with "unexpected snapshot shape" because none
// of its envelope keys (jobs / tasks / triggers / continuations /
// heartbeat) match the flat payload's top-level keys. The companion
// sibling harness/test_heartbeat_handler_shape.py pins the same
// contract on the producer side.
func TestParseSnapshotSingle_FlatHeartbeatEnabled(t *testing.T) {
	raw := []byte(`{
	  "enabled": true,
	  "schedule": "*/30 * * * *",
	  "model": null,
	  "backend_id": null,
	  "consensus": [],
	  "max_tokens": null,
	  "next_fire": null,
	  "last_fire": null,
	  "last_success": null
	}`)
	entry, err := parseSnapshotSingle(raw)
	if err != nil {
		t.Fatalf("parseSnapshotSingle returned error: %v", err)
	}
	if entry == nil {
		t.Fatal("parseSnapshotSingle returned nil entry on a populated payload")
	}
	if got, ok := entry["schedule"].(string); !ok || got != "*/30 * * * *" {
		t.Errorf("schedule = %v; want %q", entry["schedule"], "*/30 * * * *")
	}
	if enabled, ok := entry["enabled"].(bool); !ok || !enabled {
		t.Errorf("enabled = %v; want true", entry["enabled"])
	}
	// next_fire / last_fire / last_success arrive as null in the
	// never-fired state (#1087). They must round-trip as nil so the
	// view renderer can surface them as "-" rather than as the string
	// "null".
	for _, k := range []string{"next_fire", "last_fire", "last_success"} {
		if entry[k] != nil {
			t.Errorf("%s = %v; want nil for never-fired state", k, entry[k])
		}
	}
}

// TestParseSnapshotSingle_FlatHeartbeatDisabled pins the disabled
// branch of the handler contract: when HEARTBEAT.md is missing or
// disabled the handler returns `{"enabled": false, ...}` with the
// same flat shape, not an empty body. The CLI command layer must
// distinguish "no heartbeat" via the `enabled` field rather than via
// len(entries) == 0 — that latter check is unreachable against the
// real harness because the body is never empty.
func TestParseSnapshotSingle_FlatHeartbeatDisabled(t *testing.T) {
	raw := []byte(`{"enabled":false,"schedule":null,"model":null,"backend_id":null,"consensus":[],"max_tokens":null,"next_fire":null,"last_fire":null,"last_success":null}`)
	entry, err := parseSnapshotSingle(raw)
	if err != nil {
		t.Fatalf("parseSnapshotSingle returned error: %v", err)
	}
	if entry == nil {
		t.Fatal("parseSnapshotSingle returned nil on a disabled-heartbeat payload")
	}
	if heartbeatEnabled(entry) {
		t.Errorf("heartbeatEnabled = true; want false for disabled payload")
	}
}

// TestParseSnapshotSingle_EmptyBody covers the degenerate empty-body
// path. parseSnapshotSingle returns (nil, nil) — the caller treats
// that the same as a disabled heartbeat (printed as "no heartbeat
// configured") rather than as an error.
func TestParseSnapshotSingle_EmptyBody(t *testing.T) {
	entry, err := parseSnapshotSingle([]byte(""))
	if err != nil {
		t.Fatalf("parseSnapshotSingle on empty body returned error: %v", err)
	}
	if entry != nil {
		t.Errorf("parseSnapshotSingle on empty body = %v; want nil", entry)
	}
	entry, err = parseSnapshotSingle([]byte("   \n\t  "))
	if err != nil {
		t.Fatalf("parseSnapshotSingle on whitespace body returned error: %v", err)
	}
	if entry != nil {
		t.Errorf("parseSnapshotSingle on whitespace body = %v; want nil", entry)
	}
}

// TestParseSnapshotSingle_Malformed pins the error-wrapping branch:
// malformed JSON returns an error prefixed with "parse object:" so a
// future caller can distinguish a transport-level fetch failure from
// a body-level decode failure.
func TestParseSnapshotSingle_Malformed(t *testing.T) {
	_, err := parseSnapshotSingle([]byte("{"))
	if err == nil {
		t.Fatal("parseSnapshotSingle on malformed JSON returned nil err; want non-nil")
	}
	if !strings.Contains(err.Error(), "parse object:") {
		t.Errorf("parseSnapshotSingle err = %q; want substring %q", err.Error(), "parse object:")
	}
}

// TestHeartbeatEnabled pins the truthiness helper used by
// runHeartbeatView to decide whether to render the heartbeat or warn
// "no heartbeat configured". Missing `enabled` key returns true
// (permissive): an older harness or a future schema change that drops
// the field shouldn't be silently treated as disabled — better to
// render what was returned than to suppress it.
func TestHeartbeatEnabled(t *testing.T) {
	cases := []struct {
		name string
		in   snapshotEntry
		want bool
	}{
		{"enabled true", snapshotEntry{"enabled": true}, true},
		{"enabled false", snapshotEntry{"enabled": false}, false},
		{"enabled missing — permissive", snapshotEntry{"schedule": "*/5 * * * *"}, true},
		{"enabled wrong type — defensive false", snapshotEntry{"enabled": "true"}, false},
		{"enabled nil — defensive false", snapshotEntry{"enabled": nil}, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := heartbeatEnabled(tc.in)
			if got != tc.want {
				t.Errorf("heartbeatEnabled(%v) = %v; want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestParseSnapshot_FlatHeartbeatRejected guards the contract boundary
// in the opposite direction: parseSnapshot (the envelope parser) MUST
// still reject the flat heartbeat shape. The flat shape is owned by
// parseSnapshotSingle; teaching parseSnapshot to silently accept it
// would mask real envelope-schema-drift errors on the jobs / tasks /
// triggers / continuations endpoints (#1244 documented that risk in
// the parseSnapshot error message itself).
func TestParseSnapshot_FlatHeartbeatRejected(t *testing.T) {
	raw := []byte(`{"enabled":true,"schedule":"*/30 * * * *","model":null,"backend_id":null,"consensus":[],"max_tokens":null,"next_fire":null,"last_fire":null,"last_success":null}`)
	_, err := parseSnapshot(raw)
	if err == nil {
		t.Fatal("parseSnapshot accepted a flat heartbeat shape; expected envelope-shape error")
	}
	if !strings.Contains(err.Error(), "unexpected snapshot shape") {
		t.Errorf("parseSnapshot err = %q; want substring %q", err.Error(), "unexpected snapshot shape")
	}
}
