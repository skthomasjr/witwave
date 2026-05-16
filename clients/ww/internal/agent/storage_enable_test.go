package agent

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestApplyStorageEnableInPlace_AddsRuntimeAndBackendState(t *testing.T) {
	cr := seedAgent("hello", "default", func(spec map[string]interface{}) {
		spec["backends"] = []interface{}{
			map[string]interface{}{
				"name": "claude",
				"storage": map[string]interface{}{
					"enabled": true,
					"size":    "10Gi",
					"mounts": []interface{}{
						map[string]interface{}{"subPath": "logs", "mountPath": "/home/agent/logs"},
					},
				},
			},
		}
	})

	changes, err := applyStorageEnableInPlace(cr, storageEnableConfig{
		RuntimeSize:  "1Gi",
		BackendState: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changes.Changed() {
		t.Fatal("expected storage changes")
	}
	if !changes.RuntimeStorageChanged {
		t.Fatal("expected runtimeStorage change")
	}
	if len(changes.BackendStateAdded) != 1 || changes.BackendStateAdded[0] != "claude" {
		t.Fatalf("BackendStateAdded = %+v; want [claude]", changes.BackendStateAdded)
	}

	enabled, found, err := unstructured.NestedBool(cr.Object, "spec", "runtimeStorage", "enabled")
	if err != nil || !found || !enabled {
		t.Fatalf("runtimeStorage.enabled = %v found=%v err=%v; want true", enabled, found, err)
	}
	size, _, _ := unstructured.NestedString(cr.Object, "spec", "runtimeStorage", "size")
	if size != "1Gi" {
		t.Errorf("runtimeStorage.size = %q; want 1Gi", size)
	}
	runtimeMounts, found, err := unstructured.NestedSlice(cr.Object, "spec", "runtimeStorage", "mounts")
	if err != nil || !found {
		t.Fatalf("runtimeStorage.mounts missing: found=%v err=%v", found, err)
	}
	if !mountListHasPath(runtimeMounts, "/home/agent/logs") || !mountListHasPath(runtimeMounts, "/home/agent/state") {
		t.Fatalf("runtimeStorage.mounts missing logs/state: %+v", runtimeMounts)
	}

	backends, _, _ := unstructured.NestedSlice(cr.Object, "spec", "backends")
	claude := backends[0].(map[string]interface{})
	storage := claude["storage"].(map[string]interface{})
	mounts := storage["mounts"].([]interface{})
	if !mountListHasPath(mounts, "/home/agent/state") {
		t.Fatalf("backend storage mounts missing state: %+v", mounts)
	}
}

func TestApplyStorageEnableInPlace_IsIdempotent(t *testing.T) {
	cr := seedAgent("hello", "default", func(spec map[string]interface{}) {
		spec["runtimeStorage"] = map[string]interface{}{
			"enabled": true,
			"size":    "1Gi",
			"mounts": []interface{}{
				map[string]interface{}{"subPath": "logs", "mountPath": "/home/agent/logs"},
				map[string]interface{}{"subPath": "state", "mountPath": "/home/agent/state"},
			},
		}
		spec["backends"] = []interface{}{
			map[string]interface{}{
				"name": "claude",
				"storage": map[string]interface{}{
					"enabled": true,
					"size":    "10Gi",
					"mounts": []interface{}{
						map[string]interface{}{"subPath": "logs", "mountPath": "/home/agent/logs"},
						map[string]interface{}{"subPath": "state", "mountPath": "/home/agent/state"},
					},
				},
			},
		}
	})

	changes, err := applyStorageEnableInPlace(cr, storageEnableConfig{
		RuntimeSize:  "1Gi",
		BackendState: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changes.Changed() {
		t.Fatalf("expected no changes, got %+v", changes)
	}
}

func TestApplyStorageEnableInPlace_SkipsBackendsWithoutStorage(t *testing.T) {
	cr := seedAgent("hello", "default", func(spec map[string]interface{}) {
		spec["backends"] = []interface{}{
			map[string]interface{}{"name": "echo"},
		}
	})

	changes, err := applyStorageEnableInPlace(cr, storageEnableConfig{
		RuntimeSize:  "1Gi",
		BackendState: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changes.RuntimeStorageChanged {
		t.Fatal("expected runtime storage to be enabled")
	}
	if len(changes.BackendStateAdded) != 0 {
		t.Fatalf("BackendStateAdded = %+v; want empty", changes.BackendStateAdded)
	}
	if len(changes.BackendStateSkipped) != 1 || changes.BackendStateSkipped[0] != "echo (no storage)" {
		t.Fatalf("BackendStateSkipped = %+v; want echo skipped", changes.BackendStateSkipped)
	}
}

func TestApplyStorageEnableInPlace_PreservesExistingRuntimeStorage(t *testing.T) {
	cr := seedAgent("hello", "default", func(spec map[string]interface{}) {
		spec["runtimeStorage"] = map[string]interface{}{
			"enabled":          true,
			"size":             "2Gi",
			"storageClassName": "fast",
			"mounts": []interface{}{
				map[string]interface{}{"subPath": "custom", "mountPath": "/custom"},
			},
		}
	})

	changes, err := applyStorageEnableInPlace(cr, storageEnableConfig{
		RuntimeSize:  "1Gi",
		BackendState: false,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changes.RuntimeStorageChanged {
		t.Fatal("expected missing default mounts to be added")
	}
	size, _, _ := unstructured.NestedString(cr.Object, "spec", "runtimeStorage", "size")
	if size != "2Gi" {
		t.Errorf("runtimeStorage.size = %q; want preserved 2Gi", size)
	}
	class, _, _ := unstructured.NestedString(cr.Object, "spec", "runtimeStorage", "storageClassName")
	if class != "fast" {
		t.Errorf("runtimeStorage.storageClassName = %q; want preserved fast", class)
	}
	mounts, _, _ := unstructured.NestedSlice(cr.Object, "spec", "runtimeStorage", "mounts")
	for _, want := range []string{"/custom", "/home/agent/logs", "/home/agent/state"} {
		if !mountListHasPath(mounts, want) {
			t.Fatalf("runtimeStorage.mounts missing %s: %+v", want, mounts)
		}
	}
}

func TestRuntimeStoragePlanValue(t *testing.T) {
	cases := []struct {
		name       string
		agentName  string
		mutateSpec func(spec map[string]interface{})
		want       string
	}{
		{
			name:       "no runtimeStorage uses default size",
			agentName:  "hello",
			mutateSpec: func(spec map[string]interface{}) {},
			want:       "hello-runtime-data (1Gi)",
		},
		{
			name:      "explicit size is preserved",
			agentName: "research",
			mutateSpec: func(spec map[string]interface{}) {
				spec["runtimeStorage"] = map[string]interface{}{
					"size": "10Gi",
				}
			},
			want: "research-runtime-data (10Gi)",
		},
		{
			name:      "empty size string falls back to default",
			agentName: "ops",
			mutateSpec: func(spec map[string]interface{}) {
				spec["runtimeStorage"] = map[string]interface{}{
					"size": "",
				}
			},
			want: "ops-runtime-data (1Gi)",
		},
		{
			name:      "existingClaim wins over size",
			agentName: "hello",
			mutateSpec: func(spec map[string]interface{}) {
				spec["runtimeStorage"] = map[string]interface{}{
					"existingClaim": "shared-runtime-pvc",
					"size":          "10Gi",
				}
			},
			want: "use existing claim shared-runtime-pvc",
		},
		{
			name:      "existingClaim alone (no size)",
			agentName: "hello",
			mutateSpec: func(spec map[string]interface{}) {
				spec["runtimeStorage"] = map[string]interface{}{
					"existingClaim": "preprovisioned",
				}
			},
			want: "use existing claim preprovisioned",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cr := seedAgent(tc.agentName, "default", tc.mutateSpec)
			got := runtimeStoragePlanValue(cr, tc.agentName)
			if got != tc.want {
				t.Errorf("runtimeStoragePlanValue() = %q; want %q", got, tc.want)
			}
		})
	}
}
