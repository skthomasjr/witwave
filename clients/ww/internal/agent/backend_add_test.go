package agent

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// ---------------------------------------------------------------------------
// BackendAdd — the CR-level path (no gitSync wired → repo phase is a no-op)
// ---------------------------------------------------------------------------

func TestBackendAdd_AppendsToExistingCR(t *testing.T) {
	cr := seedAgent("hello", "default", nil)
	dyn := makeFakeDynamic(cr)
	k8s := makeFakeK8s()
	t.Cleanup(withFakeClients(t, dyn, k8s))

	out := captureOut()
	err := BackendAdd(context.Background(), nil, BackendAddOptions{
		Agent:     "hello",
		Namespace: "default",
		Backend:   BackendSpec{Name: "claude", Type: "claude"},
		// no Auth — we're only asserting CR shape here.
		Out: out,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	updated := readAgent(t, dyn, "default", "hello")
	backends, _, _ := unstructured.NestedSlice(updated.Object, "spec", "backends")
	if len(backends) != 2 {
		t.Fatalf("expected 2 backends after add, got %d", len(backends))
	}
	last := backends[1].(map[string]interface{})
	if last["name"] != "claude" {
		t.Errorf("appended backend name = %v; want 'claude'", last["name"])
	}
	// Port auto-picked to 8002 (echo seeded at 8001).
	if last["port"].(int64) != 8002 {
		t.Errorf("auto-port = %d; want 8002", last["port"])
	}
	mustContain(t, out.String(), "Added backend \"claude\"")
}

func TestBackendAdd_DuplicateName_Refuses(t *testing.T) {
	cr := seedAgent("hello", "default", nil) // seeded with echo
	dyn := makeFakeDynamic(cr)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))

	err := BackendAdd(context.Background(), nil, BackendAddOptions{
		Agent:     "hello",
		Namespace: "default",
		Backend:   BackendSpec{Name: "echo", Type: "echo"},
		Out:       captureOut(),
	})
	if err == nil {
		t.Fatal("expected duplicate-name error")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error = %q; want duplicate message", err)
	}
}

func TestBackendAdd_UnknownType_Refuses(t *testing.T) {
	cr := seedAgent("hello", "default", nil)
	dyn := makeFakeDynamic(cr)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))

	err := BackendAdd(context.Background(), nil, BackendAddOptions{
		Agent:     "hello",
		Namespace: "default",
		Backend:   BackendSpec{Name: "mystery", Type: "bogus-type"},
		Out:       captureOut(),
	})
	if err == nil {
		t.Fatal("expected unknown-type error")
	}
	if !strings.Contains(err.Error(), "unknown backend type") {
		t.Errorf("error = %q; want unknown-type message", err)
	}
}

func TestBackendAdd_InvalidName_Refuses(t *testing.T) {
	cr := seedAgent("hello", "default", nil)
	dyn := makeFakeDynamic(cr)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))

	err := BackendAdd(context.Background(), nil, BackendAddOptions{
		Agent:     "hello",
		Namespace: "default",
		Backend:   BackendSpec{Name: "Bad-UPPER", Type: "echo"},
		Out:       captureOut(),
	})
	if err == nil {
		t.Fatal("expected DNS-1123 error for uppercase name")
	}
}

func TestBackendAdd_AgentNotFound(t *testing.T) {
	dyn := makeFakeDynamic() // no agents seeded
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))

	err := BackendAdd(context.Background(), nil, BackendAddOptions{
		Agent:     "ghost",
		Namespace: "default",
		Backend:   BackendSpec{Name: "echo", Type: "echo"},
		Out:       captureOut(),
	})
	if err == nil {
		t.Fatal("expected not-found error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q; want 'not found' substring", err)
	}
}

func TestBackendAdd_DryRun_NoMutation(t *testing.T) {
	cr := seedAgent("hello", "default", nil)
	dyn := makeFakeDynamic(cr)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))

	err := BackendAdd(context.Background(), nil, BackendAddOptions{
		Agent:     "hello",
		Namespace: "default",
		Backend:   BackendSpec{Name: "claude", Type: "claude"},
		DryRun:    true,
		Out:       captureOut(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	updated := readAgent(t, dyn, "default", "hello")
	backends, _, _ := unstructured.NestedSlice(updated.Object, "spec", "backends")
	if len(backends) != 1 {
		t.Fatalf("dry-run must not mutate — expected 1 backend, got %d", len(backends))
	}
}

// ---------------------------------------------------------------------------
// Auth resolution — mint + existing-secret
// ---------------------------------------------------------------------------

func TestBackendAdd_WithAuthProfile_MintsSecret(t *testing.T) {
	// Can't t.Parallel — t.Setenv mutates process env.
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "sk-oauth-fake-1234")

	cr := seedAgent("hello", "default", nil)
	dyn := makeFakeDynamic(cr)
	k8s := makeFakeK8s()
	t.Cleanup(withFakeClients(t, dyn, k8s))

	err := BackendAdd(context.Background(), nil, BackendAddOptions{
		Agent:     "hello",
		Namespace: "default",
		Backend:   BackendSpec{Name: "claude", Type: "claude"},
		Auth:      BackendAuthResolver{Mode: BackendAuthProfile, Profile: "oauth"},
		Out:       captureOut(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Secret was minted with the predictable name and the conventional key.
	sec, err := k8s.CoreV1().Secrets("default").Get(
		context.Background(), "hello-claude-credentials", metav1.GetOptions{},
	)
	if err != nil {
		t.Fatalf("Secret not minted: %v", err)
	}
	if string(sec.StringData["CLAUDE_CODE_OAUTH_TOKEN"]) != "sk-oauth-fake-1234" {
		t.Error("Secret did not receive the env var value")
	}

	// CR entry references the minted Secret.
	updated := readAgent(t, dyn, "default", "hello")
	backends, _, _ := unstructured.NestedSlice(updated.Object, "spec", "backends")
	added := backends[1].(map[string]interface{})
	creds, _ := added["credentials"].(map[string]interface{})
	if creds == nil || creds["existingSecret"] != "hello-claude-credentials" {
		t.Errorf("backend credentials = %+v; want existingSecret=hello-claude-credentials", creds)
	}
}

func TestBackendAdd_NoAuth_LLMBackend_WarnsInBanner(t *testing.T) {
	cr := seedAgent("hello", "default", nil)
	dyn := makeFakeDynamic(cr)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))

	out := captureOut()
	if err := BackendAdd(context.Background(), nil, BackendAddOptions{
		Agent:     "hello",
		Namespace: "default",
		Backend:   BackendSpec{Name: "claude", Type: "claude"},
		// No Auth — this is the warn path.
		Out: out,
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	mustContain(t, out.String(), "none — claude is an LLM backend")
}

// ---------------------------------------------------------------------------
// Port picking + inline backend.yaml regeneration
// ---------------------------------------------------------------------------

func TestBackendAdd_PortPicksFirstFreeSlot_Sparse(t *testing.T) {
	// Seed two backends with non-contiguous ports so the picker has to
	// fill the gap rather than appending at len(existing).
	cr := seedAgent("hello", "default", func(spec map[string]interface{}) {
		spec["backends"] = []interface{}{
			map[string]interface{}{
				"name": "echo-1",
				"port": int64(8001),
				"image": map[string]interface{}{
					"repository": "ghcr.io/witwave-ai/images/echo",
					"tag":        "test",
				},
			},
			map[string]interface{}{
				"name": "echo-3",
				"port": int64(8003),
				"image": map[string]interface{}{
					"repository": "ghcr.io/witwave-ai/images/echo",
					"tag":        "test",
				},
			},
		}
	})
	dyn := makeFakeDynamic(cr)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))

	err := BackendAdd(context.Background(), nil, BackendAddOptions{
		Agent:     "hello",
		Namespace: "default",
		Backend:   BackendSpec{Name: "echo-2", Type: "echo"},
		Out:       captureOut(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	updated := readAgent(t, dyn, "default", "hello")
	backends, _, _ := unstructured.NestedSlice(updated.Object, "spec", "backends")
	added := backends[2].(map[string]interface{})
	if added["port"].(int64) != 8002 {
		t.Errorf("port = %d; want 8002 (filling the gap between 8001 and 8003)", added["port"])
	}
}

func TestBackendAdd_RewritesInlineBackendYAML(t *testing.T) {
	// Seed with the inline backend.yaml layout create produces so we
	// exercise the "ww owns the config" regeneration path.
	cr := seedAgent("hello", "default", func(spec map[string]interface{}) {
		spec["config"] = []interface{}{
			map[string]interface{}{
				"name":      "backend.yaml",
				"mountPath": "/home/agent/.witwave/backend.yaml",
				"content":   "# old — will be regenerated\n",
			},
		}
	})
	dyn := makeFakeDynamic(cr)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))

	out := captureOut()
	err := BackendAdd(context.Background(), nil, BackendAddOptions{
		Agent:     "hello",
		Namespace: "default",
		Backend:   BackendSpec{Name: "claude", Type: "claude"},
		Out:       out,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	updated := readAgent(t, dyn, "default", "hello")
	cfg, _, _ := unstructured.NestedSlice(updated.Object, "spec", "config")
	inline, _ := cfg[0].(map[string]interface{})
	content, _ := inline["content"].(string)
	// New backend must appear in the regenerated file. Exact format
	// is renderBackendYAML's business; just assert the name landed.
	if !strings.Contains(content, "claude") {
		t.Errorf("regenerated backend.yaml missing 'claude':\n%s", content)
	}
	// Old placeholder comment should be gone (full regen, not a patch).
	if strings.Contains(content, "old — will be regenerated") {
		t.Errorf("regenerated backend.yaml still carries the stale placeholder:\n%s", content)
	}
	mustContain(t, out.String(), "backend.yaml (inline, ww-managed) will be regenerated")
}

// ---------------------------------------------------------------------------
// 50-backend cap
// ---------------------------------------------------------------------------

func TestBackendAdd_RefusesPast50(t *testing.T) {
	cr := seedAgent("hello", "default", func(spec map[string]interface{}) {
		// Fill to 50. Ports deliberately fill the full 8001..8050
		// range so the port picker also bottoms out alongside the
		// item cap — either path should fail with a clear message.
		bs := make([]interface{}, 50)
		for i := 0; i < 50; i++ {
			bs[i] = map[string]interface{}{
				"name": "b" + string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)),
				"port": int64(8001 + i),
				"image": map[string]interface{}{
					"repository": "ghcr.io/witwave-ai/images/echo",
					"tag":        "test",
				},
			}
		}
		spec["backends"] = bs
	})
	dyn := makeFakeDynamic(cr)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))

	err := BackendAdd(context.Background(), nil, BackendAddOptions{
		Agent:     "hello",
		Namespace: "default",
		Backend:   BackendSpec{Name: "zzz", Type: "echo"},
		Out:       captureOut(),
	})
	if err == nil {
		t.Fatal("expected an error past the 50-backend cap")
	}
	if !strings.Contains(err.Error(), "50") {
		t.Errorf("error = %q; want a message mentioning the cap", err)
	}
}
