/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package agent

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// TestSendUsesSendCtxForServiceGet covers #1720: the prerequisite Service
// Get must use the timeout-bounded sendCtx, not the outer parent ctx.
//
// Send() owns a real kubernetes client wired through rest.Config, so
// mocking the apiserver round-trip from outside is heavyweight. Instead
// we pin the source shape so a future edit cannot silently re-introduce
// the unbounded-Get bug.
func TestSendUsesSendCtxForServiceGet(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	src, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), "send.go"))
	if err != nil {
		t.Fatalf("read send.go: %v", err)
	}
	body := string(src)

	// The Service Get must address sendCtx, not the outer ctx.
	if !regexp.MustCompile(`Services\(opts\.Namespace\)\.Get\(\s*sendCtx\b`).MatchString(body) {
		t.Errorf("Services().Get must use sendCtx (timeout-bounded), not the outer ctx (#1720)")
	}
	if regexp.MustCompile(`Services\(opts\.Namespace\)\.Get\(\s*ctx\b`).MatchString(body) {
		t.Errorf("regression: Services().Get is back to using the outer ctx (#1720)")
	}

	// sendCtx must be derived BEFORE the Service Get.
	getIdx := strings.Index(body, "Services(opts.Namespace).Get(")
	ctxIdx := strings.Index(body, "context.WithTimeout(ctx, timeout)")
	if getIdx < 0 || ctxIdx < 0 {
		t.Fatalf("could not locate Get / WithTimeout sites (Get=%d, WithTimeout=%d)", getIdx, ctxIdx)
	}
	if ctxIdx >= getIdx {
		t.Errorf("context.WithTimeout(ctx, timeout) must precede the Service Get (#1720)")
	}
}
