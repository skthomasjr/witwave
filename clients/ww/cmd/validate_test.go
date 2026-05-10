// Tests for the inferKind helper that decides which X-Ww-Kind header
// `ww validate` sends to the harness `/validate` endpoint. Pure-helper
// table-driven coverage in the spirit of internal/output/output_test.go;
// the HTTP path is exercised end-to-end against a real harness, here we
// just pin the path-to-kind inference rules so a future rename in the
// `inferKind` switch (or a typo in one of the parent-dir keywords)
// fails loudly rather than silently dropping the X-Ww-Kind header.
package cmd

import (
	"path/filepath"
	"testing"
)

func TestInferKind(t *testing.T) {
	cases := []struct {
		name string
		path string
		want string
	}{
		{"jobs parent dir", filepath.Join("jobs", "daily-report.md"), "job"},
		{"tasks parent dir", filepath.Join("tasks", "daily-report.md"), "task"},
		{"triggers parent dir", filepath.Join("triggers", "notify.md"), "trigger"},
		{"continuations parent dir", filepath.Join("continuations", "after-deploy.md"), "continuation"},
		{"webhooks parent dir", filepath.Join("webhooks", "github-push.md"), "webhook"},
		{"heartbeat by basename in unrelated dir", filepath.Join(".witwave", "heartbeat.md"), "heartbeat"},
		{"heartbeat by basename at repo root", "heartbeat.md", "heartbeat"},
		{"unknown parent dir falls through", filepath.Join("snippets", "something.md"), ""},
		{"empty path returns empty", "", ""},
		{"parent dir is case-insensitive", filepath.Join("Jobs", "x.md"), "job"},
		{"basename is case-insensitive for heartbeat", filepath.Join("dir", "Heartbeat.md"), "heartbeat"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := inferKind(tc.path)
			if got != tc.want {
				t.Errorf("inferKind(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}
