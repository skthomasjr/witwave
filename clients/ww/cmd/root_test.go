// Tests for the pure error-classification helpers in root.go that the
// CLI's exit-code propagation contract depends on. Mirrors the table-
// driven shape used in operator_test.go / snapshot_test.go: these
// helpers feed `handleErr`, which is exercised end-to-end against a
// real cobra command tree elsewhere — this file just pins the helper
// contracts so a future tweak to the unwrap shape or the exit-code
// constants fails loudly here instead of changing what shell pipelines
// observe.
package cmd

import (
	"errors"
	"fmt"
	"testing"

	"github.com/witwave-ai/witwave/clients/ww/internal/client"
)

// TestExtractDirect pins the trivial case: extract recognises a bare
// *commandErr and stashes it.
func TestExtractDirect(t *testing.T) {
	src := &commandErr{code: 7, msg: "boom"}
	var out *commandErr
	if !extract(src, &out) {
		t.Fatalf("extract returned false for direct *commandErr")
	}
	if out == nil {
		t.Fatalf("extract returned true but did not stash the *commandErr (out is nil)")
	}
	if out.code != 7 || out.msg != "boom" {
		t.Errorf("stashed *commandErr = %+v, want {code:7 msg:boom}", out)
	}
}

// TestExtractWrappedFmt confirms extract sees through `fmt.Errorf("…%w…")`,
// the single-Unwrap path that the pre-#1552 implementation also covered.
func TestExtractWrappedFmt(t *testing.T) {
	src := &commandErr{code: 3, msg: "inner"}
	wrapped := fmt.Errorf("outer: %w", src)
	var out *commandErr
	if !extract(wrapped, &out) {
		t.Fatalf("extract returned false for fmt.Errorf-wrapped *commandErr")
	}
	if out == nil || out.code != 3 || out.msg != "inner" {
		t.Errorf("extract did not surface inner *commandErr through fmt.Errorf wrap: out=%+v", out)
	}
}

// TestExtractJoined is the #1552 regression pin: a *commandErr buried
// inside an errors.Join tree must be detected. The pre-#1552
// implementation walked a single Unwrap chain and missed multi-unwrap
// shapes; the current errors.As-based impl must traverse the join.
func TestExtractJoined(t *testing.T) {
	cmdErr := &commandErr{code: 9, msg: "buried"}
	other := errors.New("sibling-error-in-join")
	joined := errors.Join(other, cmdErr)
	var out *commandErr
	if !extract(joined, &out) {
		t.Fatalf("extract returned false for *commandErr inside errors.Join — #1552 regression")
	}
	if out == nil || out.code != 9 || out.msg != "buried" {
		t.Errorf("extract did not surface joined *commandErr: out=%+v", out)
	}
}

// TestExtractJoinedFirstPosition mirrors TestExtractJoined but flips
// the join order so the *commandErr is the first element. errors.Join
// returns a wrapper whose Unwrap() returns []error; the helper must
// find the sentinel regardless of position.
func TestExtractJoinedFirstPosition(t *testing.T) {
	cmdErr := &commandErr{code: 5, msg: "first"}
	other := errors.New("sibling")
	joined := errors.Join(cmdErr, other)
	var out *commandErr
	if !extract(joined, &out) {
		t.Fatalf("extract returned false when *commandErr is the first errors.Join element")
	}
	if out == nil || out.code != 5 || out.msg != "first" {
		t.Errorf("extract surfaced wrong *commandErr: out=%+v", out)
	}
}

// TestExtractMisses confirms the negative path: errors that are not (and
// don't wrap) *commandErr return false and leave the out pointer
// untouched / nil.
func TestExtractMisses(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"plain errors.New", errors.New("just-a-string-error")},
		{"fmt.Errorf no wrap", fmt.Errorf("formatted without wrap")},
		{"nested fmt wrap of non-commandErr", fmt.Errorf("outer: %w", errors.New("inner-not-commandErr"))},
		{"errors.Join of unrelated errors", errors.Join(errors.New("a"), errors.New("b"))},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var out *commandErr
			if extract(tc.err, &out) {
				t.Errorf("extract returned true for non-commandErr (%T): out=%+v", tc.err, out)
			}
			if out != nil {
				t.Errorf("extract did not return true but stashed out=%+v (expected nil)", out)
			}
		})
	}
}

// TestExtractNilError mirrors the errors.As contract for nil input:
// extract returns false and does not touch out.
func TestExtractNilError(t *testing.T) {
	var out *commandErr
	if extract(nil, &out) {
		t.Errorf("extract(nil, &out) returned true; expected false")
	}
	if out != nil {
		t.Errorf("extract(nil, &out) populated out=%+v; expected nil", out)
	}
}

// TestTransportErr pins the constructor's two outputs: the wrapped error
// is a *commandErr with client.ExitTransport (= 2) and its message is
// the original error's Error() string verbatim.
func TestTransportErr(t *testing.T) {
	src := errors.New("dial tcp 1.2.3.4:443: connect: connection refused")
	wrapped := transportErr(src)
	if wrapped == nil {
		t.Fatalf("transportErr returned nil for non-nil input")
	}
	ce, ok := wrapped.(*commandErr)
	if !ok {
		t.Fatalf("transportErr return type = %T, want *commandErr", wrapped)
	}
	if ce.code != client.ExitTransport {
		t.Errorf("transportErr code = %d, want client.ExitTransport (%d)", ce.code, client.ExitTransport)
	}
	if ce.msg != src.Error() {
		t.Errorf("transportErr msg = %q, want %q (verbatim source Error())", ce.msg, src.Error())
	}
}

// TestLogicalErr pins the sibling constructor: same shape as
// transportErr but with client.ExitLogical (= 1). Pinning both
// constants protects the user-facing shell exit-code-2-vs-1 promise.
func TestLogicalErr(t *testing.T) {
	src := errors.New("agent foo not found in namespace bar")
	wrapped := logicalErr(src)
	if wrapped == nil {
		t.Fatalf("logicalErr returned nil for non-nil input")
	}
	ce, ok := wrapped.(*commandErr)
	if !ok {
		t.Fatalf("logicalErr return type = %T, want *commandErr", wrapped)
	}
	if ce.code != client.ExitLogical {
		t.Errorf("logicalErr code = %d, want client.ExitLogical (%d)", ce.code, client.ExitLogical)
	}
	if ce.msg != src.Error() {
		t.Errorf("logicalErr msg = %q, want %q (verbatim source Error())", ce.msg, src.Error())
	}
}

// TestExitCodeContrast guards the constants themselves so a future
// renumbering surfaces here: transportErr and logicalErr must produce
// distinct exit codes, with transport > logical (the convention shell
// pipelines key off — exit 1 = logical/user-facing failure, exit 2 =
// transport/network failure that retry could plausibly recover).
func TestExitCodeContrast(t *testing.T) {
	src := errors.New("x")
	tCode := transportErr(src).(*commandErr).code
	lCode := logicalErr(src).(*commandErr).code
	if tCode == lCode {
		t.Fatalf("transportErr (%d) and logicalErr (%d) produced the same exit code; shell pipelines key off the difference", tCode, lCode)
	}
	if tCode != 2 || lCode != 1 {
		t.Errorf("exit codes drifted from documented values: transport=%d (want 2), logical=%d (want 1) — update root.go comment AND this assertion together", tCode, lCode)
	}
}

// TestExtractThenIdentify is the integration-shape pin: a wrapped
// commandErr extracted via extract() should expose code+msg identical
// to a freshly-constructed transportErr — the round-trip is what
// handleErr depends on for exit-code propagation.
func TestExtractThenIdentify(t *testing.T) {
	src := errors.New("network unreachable")
	wrapped := transportErr(src)
	var out *commandErr
	if !extract(wrapped, &out) {
		t.Fatalf("extract did not surface a freshly-constructed transportErr")
	}
	if out.code != client.ExitTransport {
		t.Errorf("round-tripped code = %d, want client.ExitTransport (%d)", out.code, client.ExitTransport)
	}
	if out.msg != src.Error() {
		t.Errorf("round-tripped msg = %q, want %q", out.msg, src.Error())
	}
	// Sanity-check Error() formatting passes through.
	if got := wrapped.Error(); got != src.Error() {
		t.Errorf("(*commandErr).Error() = %q, want %q (the source message)", got, src.Error())
	}
}
