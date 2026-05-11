// Tests for the pure-helper parsers in gitsync_flags.go that the
// `ww agent create --gitsync-map` and `--gitsync-secret` flag
// handlers compose over user input. Mirrors the table-driven
// shape used in backend_spec_test.go (TestParseBackendSpecs) and
// persist_flags_test.go — the cross-flag validators
// (ApplyGitSyncSecrets, ValidateGitFlags) need GitSync/GitMapping
// fixtures coupled together so they're left for a higher-tier
// sweep; this file stays at "pure string → struct / map → error"
// coverage.
package agent

import (
	"strings"
	"testing"
)

// TestParseGitMappings pins the `--gitsync-map` parser. Contract:
//   - "<sync>:<src>:<dest>" → defaults Container to HarnessContainer.
//   - "<container>=<sync>:<src>:<dest>" → explicit container.
//   - Disambiguation rule: `=` before the first `:` is the container
//     separator; `=` after the first `:` is part of the body
//     (URLs / k=v query params can contain `=`).
//   - empty value, missing-half, missing-third → error.
//   - non-absolute dest → error.
//   - empty container before `=` → error.
//   - whitespace around tokens trimmed.
func TestParseGitMappings(t *testing.T) {
	t.Run("nil input returns empty slice", func(t *testing.T) {
		got, err := ParseGitMappings(nil)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if len(got) != 0 {
			t.Errorf("got = %v, want empty slice", got)
		}
	})

	t.Run("default container is HarnessContainer", func(t *testing.T) {
		got, err := ParseGitMappings([]string{"sync1:src/path:/dest/path"})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("len = %d, want 1", len(got))
		}
		if got[0].Container != HarnessContainer {
			t.Errorf("container = %q, want %q", got[0].Container, HarnessContainer)
		}
		if got[0].GitSync != "sync1" || got[0].Src != "src/path" || got[0].Dest != "/dest/path" {
			t.Errorf("got = %+v", got[0])
		}
	})

	t.Run("explicit container before equals", func(t *testing.T) {
		got, err := ParseGitMappings([]string{"claude=sync1:src:/dest"})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if got[0].Container != "claude" {
			t.Errorf("container = %q, want claude", got[0].Container)
		}
	})

	t.Run("equals after first colon stays in the body (URL-safe)", func(t *testing.T) {
		// An entry like "sync1:src=val:/dest" must NOT be parsed as
		// container=sync1 — the `=` is past the first `:` so the
		// container defaults and the body is "sync1:src=val:/dest"
		// which then SplitN-3s into sync1 / src=val / /dest.
		got, err := ParseGitMappings([]string{"sync1:src=val:/dest"})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if got[0].Container != HarnessContainer {
			t.Errorf("container = %q, want default %q", got[0].Container, HarnessContainer)
		}
		if got[0].GitSync != "sync1" || got[0].Src != "src=val" || got[0].Dest != "/dest" {
			t.Errorf("got = %+v", got[0])
		}
	})

	t.Run("body limited to 3 parts (extra colons stay in dest)", func(t *testing.T) {
		// SplitN with n=3 — any colons in the dest segment are
		// preserved (e.g. a Windows-style C:\path… won't normally
		// appear here, but a URL-style dest containing `:` would).
		got, err := ParseGitMappings([]string{"sync1:src:/dest:/with:/colons"})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if got[0].Dest != "/dest:/with:/colons" {
			t.Errorf("dest = %q, want /dest:/with:/colons", got[0].Dest)
		}
	})

	t.Run("whitespace around tokens trimmed", func(t *testing.T) {
		got, err := ParseGitMappings([]string{"  claude  =  sync1 : src : /dest  "})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if got[0].Container != "claude" || got[0].GitSync != "sync1" || got[0].Src != "src" || got[0].Dest != "/dest" {
			t.Errorf("got = %+v", got[0])
		}
	})

	t.Run("multiple entries preserved in order", func(t *testing.T) {
		got, err := ParseGitMappings([]string{
			"sync1:a:/dest1",
			"claude=sync2:b:/dest2",
		})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if len(got) != 2 || got[0].GitSync != "sync1" || got[1].Container != "claude" || got[1].GitSync != "sync2" {
			t.Errorf("got = %+v", got)
		}
	})

	errCases := []struct {
		name    string
		in      string
		wantSub string
	}{
		{"empty value", "", "empty value"},
		{"empty container before equals", "=sync1:src:/dest", "empty container before '='"},
		{"missing all colons", "sync1", "form is [<container>=]<gitsync>:<src>:<dest>"},
		{"only one colon", "sync1:src", "form is [<container>=]<gitsync>:<src>:<dest>"},
		{"empty gitsync after container", "claude=:src:/dest", "gitsync, src, and dest are all required"},
		{"empty src", "sync1::/dest", "gitsync, src, and dest are all required"},
		{"empty dest", "sync1:src:", "gitsync, src, and dest are all required"},
		{"non-absolute dest", "sync1:src:dest", "must be absolute"},
	}
	for _, tc := range errCases {
		tc := tc
		t.Run("error: "+tc.name, func(t *testing.T) {
			_, err := ParseGitMappings([]string{tc.in})
			if err == nil {
				t.Fatalf("err = nil, want error containing %q", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("err = %q, want substring %q", err.Error(), tc.wantSub)
			}
		})
	}
}

// TestParseGitSyncSecrets pins the `--gitsync-secret` parser.
// Contract:
//   - "<name>=<secret>" → one map entry.
//   - multiple distinct names accumulate.
//   - empty value, missing `=`, empty halves, duplicate name → error.
//   - whitespace trimmed.
func TestParseGitSyncSecrets(t *testing.T) {
	t.Run("nil input returns empty map", func(t *testing.T) {
		got, err := ParseGitSyncSecrets(nil)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if len(got) != 0 {
			t.Errorf("got = %v, want empty map", got)
		}
	})

	t.Run("single entry", func(t *testing.T) {
		got, err := ParseGitSyncSecrets([]string{"sync1=k8s-secret-name"})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if got["sync1"] != "k8s-secret-name" {
			t.Errorf("got = %+v, want sync1→k8s-secret-name", got)
		}
	})

	t.Run("multiple distinct names accumulate", func(t *testing.T) {
		got, err := ParseGitSyncSecrets([]string{"sync1=s1", "sync2=s2", "sync3=s3"})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if len(got) != 3 || got["sync1"] != "s1" || got["sync2"] != "s2" || got["sync3"] != "s3" {
			t.Errorf("got = %+v", got)
		}
	})

	t.Run("whitespace trimmed on both halves", func(t *testing.T) {
		got, err := ParseGitSyncSecrets([]string{"  sync1  =  k8s-secret-name  "})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if got["sync1"] != "k8s-secret-name" {
			t.Errorf("got = %+v", got)
		}
	})

	errCases := []struct {
		name    string
		in      []string
		wantSub string
	}{
		{"empty value", []string{""}, "empty value"},
		{"missing equals", []string{"sync1"}, "form is <gitsync-name>=<k8s-secret>"},
		{"equals at start (empty name)", []string{"=k8s-secret-name"}, "form is <gitsync-name>=<k8s-secret>"},
		{"empty secret half", []string{"sync1="}, "both name and secret required"},
		{"duplicate gitsync name", []string{"sync1=s1", "sync1=s2"}, "duplicate"},
	}
	for _, tc := range errCases {
		tc := tc
		t.Run("error: "+tc.name, func(t *testing.T) {
			_, err := ParseGitSyncSecrets(tc.in)
			if err == nil {
				t.Fatalf("err = nil, want error containing %q", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("err = %q, want substring %q", err.Error(), tc.wantSub)
			}
		})
	}
}
