package agent

import (
	"strings"
	"testing"
)

// nextFreeBackendPort is the port-allocator for `ww agent backend add`.
// Its existing integration coverage (TestBackendAdd_PortPicksFirstFreeSlot_Sparse,
// TestBackendAdd_RefusesPast50) seeds backends with int64 ports only —
// the locally-built map-literal shape the test harness produces. The
// helper's documented production input, however, is the apiserver-
// returned unstructured CR whose port fields deserialize as float64
// (encoding/json's default for numbers into interface{}); that branch
// is fenced inside the type switch and is unexercised by the integration
// tests. The test suite below pins each branch of the switch + the
// empty-slate + all-50-claimed boundaries directly, so the type-tolerance
// commentary in backend_add.go stays load-bearing instead of drifting
// into dead documentation.
//
// Same-package test (not _test); calls unexported nextFreeBackendPort
// directly, matching sibling helpers tested across backend_*_test.go.

func TestNextFreeBackendPort_EmptySlate(t *testing.T) {
	got, err := nextFreeBackendPort(nil)
	if err != nil {
		t.Fatalf("unexpected error on empty slate: %v", err)
	}
	if got != DefaultBackendBasePort {
		t.Errorf("port = %d; want %d (base port)", got, DefaultBackendBasePort)
	}
}

func TestNextFreeBackendPort_Int64Ports_FillsGap(t *testing.T) {
	existing := []map[string]interface{}{
		{"name": "echo-1", "port": int64(8001)},
		{"name": "echo-3", "port": int64(8003)},
	}
	got, err := nextFreeBackendPort(existing)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 8002 {
		t.Errorf("port = %d; want 8002 (filling the gap between 8001 and 8003)", got)
	}
}

// Float64Ports — the apiserver-returned-CR shape. encoding/json decodes
// numbers into interface{} as float64; the type-switch's float64 case
// is what makes the picker correct against live cluster state. Without
// this branch under test, dropping the float64 case from the switch
// would still let the int64-only integration tests pass.
func TestNextFreeBackendPort_Float64Ports_FillsGap(t *testing.T) {
	existing := []map[string]interface{}{
		{"name": "echo-1", "port": float64(8001)},
		{"name": "echo-3", "port": float64(8003)},
	}
	got, err := nextFreeBackendPort(existing)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 8002 {
		t.Errorf("port = %d; want 8002 (float64 branch must claim the gap)", got)
	}
}

// Int32Ports — the third branch of the switch. No production decode
// produces int32 directly today (apiserver gives float64, local-build
// gives int64), but the branch exists for defensiveness and shouldn't
// silently rot.
func TestNextFreeBackendPort_Int32Ports_FillsGap(t *testing.T) {
	existing := []map[string]interface{}{
		{"name": "echo-1", "port": int32(8001)},
		{"name": "echo-3", "port": int32(8003)},
	}
	got, err := nextFreeBackendPort(existing)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 8002 {
		t.Errorf("port = %d; want 8002 (int32 branch must claim the gap)", got)
	}
}

// MixedNumericTypes — guards against a regression where a future
// refactor consolidates the switch to one branch and assumes uniform
// input. Real CRs can mix shapes when a user hand-edits a CR (int64)
// and then ww reads it back through the dynamic client (float64); the
// picker must recognise both as "claimed".
func TestNextFreeBackendPort_MixedNumericTypes_AllClaimed(t *testing.T) {
	existing := []map[string]interface{}{
		{"name": "echo-1", "port": int64(8001)},   // local-build path
		{"name": "echo-2", "port": float64(8002)}, // apiserver path
		{"name": "echo-3", "port": int32(8003)},   // defensive branch
	}
	got, err := nextFreeBackendPort(existing)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 8004 {
		t.Errorf("port = %d; want 8004 (first free after three mixed-type claims)", got)
	}
}

// Unsupported numeric type — string, nil, int (machine-width). The
// type-switch falls through without marking the slot claimed, so the
// picker treats the entry as port-less and the slot stays free. This
// pins that behavior so a future "add int support" change doesn't
// silently change the answer.
func TestNextFreeBackendPort_UnsupportedPortType_TreatedAsFree(t *testing.T) {
	existing := []map[string]interface{}{
		{"name": "echo-stringy", "port": "8001"},    // string — not a switch case
		{"name": "echo-nilly", "port": nil},         // missing — not a switch case
		{"name": "echo-untyped", "port": int(8002)}, // machine-width int — not a case
	}
	got, err := nextFreeBackendPort(existing)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != DefaultBackendBasePort {
		t.Errorf("port = %d; want %d (all entries unrecognised → slate effectively empty)", got, DefaultBackendBasePort)
	}
}

// AllSlotsClaimed — exercises the bottom-of-function error path. The
// only existing coverage (TestBackendAdd_RefusesPast50) hits the
// caller's 50-cap check before nextFreeBackendPort even runs; this
// test reaches the picker's own "no free port" branch directly.
func TestNextFreeBackendPort_AllSlotsClaimed_Errors(t *testing.T) {
	existing := make([]map[string]interface{}, 0, 50)
	for p := DefaultBackendBasePort; p <= DefaultBackendMaxPort; p++ {
		existing = append(existing, map[string]interface{}{
			"name": "echo",
			"port": int64(p),
		})
	}
	_, err := nextFreeBackendPort(existing)
	if err == nil {
		t.Fatal("expected error when all 50 slots are claimed; got nil")
	}
	want := "no free backend port"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error %q does not mention %q", err.Error(), want)
	}
}

// OutOfRangeClaims — ports outside [8001, 8050] in the existing list
// (a malformed CR, or a future schema bump) shouldn't confuse the
// picker; the in-range scan still returns the first free in-range slot.
func TestNextFreeBackendPort_OutOfRangePortsIgnored(t *testing.T) {
	existing := []map[string]interface{}{
		{"name": "echo-low", "port": int64(7000)},  // below base
		{"name": "echo-high", "port": int64(9000)}, // above max
	}
	got, err := nextFreeBackendPort(existing)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != DefaultBackendBasePort {
		t.Errorf("port = %d; want %d (out-of-range claims don't block in-range allocation)", got, DefaultBackendBasePort)
	}
}
