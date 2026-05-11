// Tests for the pure io.Writer-accepting helpers in tabwriter.go.
// Mirrors the bytes.Buffer assertion shape used in output_test.go
// for the EmitYAML/EmitJSON paths.
package output

import (
	"bytes"
	"strings"
	"testing"
)

// TestTable pins the tabwriter-backed Table renderer that every
// `ww <verb> list` subcommand uses. The contract: headers + rows
// emitted as one line each, columns aligned with the text/tabwriter
// settings (padding=2, padchar=' '). Pin headers-only, single-row,
// multi-row, ragged-row (tabwriter pads short rows to header width),
// and empty-input behaviour so a future tabwriter setting tweak
// fails the test rather than silently re-flowing every CLI table.
func TestTable(t *testing.T) {
	cases := []struct {
		name    string
		headers []string
		rows    [][]string
		wantSub []string // substrings every output must contain
		wantNot []string // substrings the output must NOT contain
	}{
		{
			name:    "headers-only emits header row",
			headers: []string{"NAME", "PHASE"},
			rows:    nil,
			wantSub: []string{"NAME", "PHASE"},
		},
		{
			name:    "single row emits header + row",
			headers: []string{"NAME", "PHASE"},
			rows: [][]string{
				{"witwave-operator-0", "Running"},
			},
			wantSub: []string{"NAME", "PHASE", "witwave-operator-0", "Running"},
		},
		{
			name:    "multiple rows preserve order",
			headers: []string{"NAME", "PHASE"},
			rows: [][]string{
				{"first", "Running"},
				{"second", "Pending"},
			},
			wantSub: []string{"first", "second", "Running", "Pending"},
		},
		{
			name:    "empty headers + empty rows emits a trailing newline only",
			headers: nil,
			rows:    nil,
			wantNot: []string{"any-row-content"},
		},
		{
			name:    "every row is on its own line",
			headers: []string{"COL"},
			rows: [][]string{
				{"a"},
				{"b"},
				{"c"},
			},
			wantSub: []string{"a\n", "b\n", "c\n"},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			buf := &bytes.Buffer{}
			Table(buf, tc.headers, tc.rows)
			got := buf.String()
			for _, sub := range tc.wantSub {
				if !strings.Contains(got, sub) {
					t.Errorf("Table output missing substring %q\nfull output:\n%q", sub, got)
				}
			}
			for _, sub := range tc.wantNot {
				if strings.Contains(got, sub) {
					t.Errorf("Table output should not contain %q\nfull output:\n%q", sub, got)
				}
			}
		})
	}

	// Cross-check alignment: when row values are different widths in
	// the first column, the second column starts at the same offset
	// across every row. tabwriter computes the per-column width, so
	// "first" and "longer-name" both produce a column-2 starting at
	// the same byte offset on their respective lines.
	t.Run("columns align across rows", func(t *testing.T) {
		buf := &bytes.Buffer{}
		Table(buf, []string{"NAME", "PHASE"}, [][]string{
			{"a", "Running"},
			{"longer-name", "Pending"},
		})
		lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
		if len(lines) != 3 {
			t.Fatalf("expected 3 lines (header + 2 rows), got %d:\n%s", len(lines), buf.String())
		}
		// Find the column-2 start offset on each row by locating the
		// second cell's first char. tabwriter uses spaces for padding,
		// so we look for the run of spaces between cell-1 and cell-2.
		offset := func(line string, val string) int {
			i := strings.Index(line, val)
			return i
		}
		if offset(lines[1], "Running") != offset(lines[2], "Pending") {
			t.Errorf("column 2 not aligned across rows:\n%q\n%q", lines[1], lines[2])
		}
	})
}

// TestKV pins the key/value emitter used by every `ww <verb> view`
// subcommand to render the "single resource detail" block. The
// contract: pairs emitted one per line, key + ':' + value separated
// by tabwriter padding so the colons line up vertically across
// uneven key widths. Empty input emits nothing.
func TestKV(t *testing.T) {
	cases := []struct {
		name    string
		pairs   [][2]string
		wantSub []string
		wantNot []string
	}{
		{
			name:    "empty input emits no rows",
			pairs:   nil,
			wantNot: []string{":"},
		},
		{
			name: "single pair emits one line with colon",
			pairs: [][2]string{
				{"Name", "witwave-operator-0"},
			},
			wantSub: []string{"Name:", "witwave-operator-0"},
		},
		{
			name: "multiple pairs preserve order",
			pairs: [][2]string{
				{"Name", "first"},
				{"Phase", "Running"},
				{"Age", "5m"},
			},
			wantSub: []string{"Name:", "Phase:", "Age:", "first", "Running", "5m"},
		},
		{
			name: "empty value still emits the key with colon",
			pairs: [][2]string{
				{"Name", ""},
			},
			wantSub: []string{"Name:"},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			buf := &bytes.Buffer{}
			KV(buf, tc.pairs)
			got := buf.String()
			for _, sub := range tc.wantSub {
				if !strings.Contains(got, sub) {
					t.Errorf("KV output missing substring %q\nfull output:\n%q", sub, got)
				}
			}
			for _, sub := range tc.wantNot {
				if strings.Contains(got, sub) {
					t.Errorf("KV output should not contain %q\nfull output:\n%q", sub, got)
				}
			}
		})
	}

	// Cross-check vertical value-column alignment: the value column
	// must start at the same byte offset on both lines, even when the
	// key widths differ (tabwriter's job — the colon is glued to the
	// key by the "%s:\t%s" format, so it's values that align, not
	// colons). Catches a future tabwriter setting drift that would
	// mis-align the view-subcommand value column.
	t.Run("values align across pairs of uneven key widths", func(t *testing.T) {
		buf := &bytes.Buffer{}
		KV(buf, [][2]string{
			{"A", "short"},
			{"LongerKey", "longer"},
		})
		lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
		if len(lines) != 2 {
			t.Fatalf("expected 2 lines, got %d:\n%s", len(lines), buf.String())
		}
		v1 := strings.Index(lines[0], "short")
		v2 := strings.Index(lines[1], "longer")
		if v1 != v2 {
			t.Errorf("values not aligned: line1 value at %d, line2 value at %d\n%q\n%q", v1, v2, lines[0], lines[1])
		}
	})
}
