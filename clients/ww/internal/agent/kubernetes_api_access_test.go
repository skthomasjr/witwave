package agent

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestNormalizeKubernetesApiAccessMode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
	}{
		{"", KubernetesApiAccessModeReadOnly},
		{"readOnly", KubernetesApiAccessModeReadOnly},
		{"r/o", KubernetesApiAccessModeReadOnly},
		{"namespaceWrite", KubernetesApiAccessModeNamespaceWrite},
		{"namespace-write", KubernetesApiAccessModeNamespaceWrite},
		{"rw", KubernetesApiAccessModeNamespaceWrite},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got, err := NormalizeKubernetesApiAccessMode(tc.in)
			if err != nil {
				t.Fatalf("NormalizeKubernetesApiAccessMode(%q) returned error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("NormalizeKubernetesApiAccessMode(%q) = %q; want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNormalizeKubernetesApiAccessModeRejectsUnknown(t *testing.T) {
	t.Parallel()
	if _, err := NormalizeKubernetesApiAccessMode("cluster-admin"); err == nil {
		t.Fatal("expected an error for unsupported mode")
	}
}

func TestNewKubernetesApiAccessSpec(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
	}{
		{"", KubernetesApiAccessModeReadOnly},
		{"readOnly", KubernetesApiAccessModeReadOnly},
		{"r/o", KubernetesApiAccessModeReadOnly},
		{"namespaceWrite", KubernetesApiAccessModeNamespaceWrite},
		{"namespace-write", KubernetesApiAccessModeNamespaceWrite},
		{"rw", KubernetesApiAccessModeNamespaceWrite},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got, err := NewKubernetesApiAccessSpec(tc.in)
			if err != nil {
				t.Fatalf("NewKubernetesApiAccessSpec(%q) returned error: %v", tc.in, err)
			}
			if got == nil {
				t.Fatalf("NewKubernetesApiAccessSpec(%q) returned nil spec", tc.in)
			}
			if !got.Enabled {
				t.Fatalf("NewKubernetesApiAccessSpec(%q).Enabled = false; want true", tc.in)
			}
			if got.Mode != tc.want {
				t.Fatalf("NewKubernetesApiAccessSpec(%q).Mode = %q; want %q", tc.in, got.Mode, tc.want)
			}
		})
	}
}

func TestNewKubernetesApiAccessSpecRejectsUnknown(t *testing.T) {
	t.Parallel()
	got, err := NewKubernetesApiAccessSpec("cluster-admin")
	if err == nil {
		t.Fatal("expected an error for unsupported mode")
	}
	if got != nil {
		t.Fatalf("expected nil spec on error; got %+v", got)
	}
}

func TestApplyKubernetesApiAccessInPlace_AddsReadOnly(t *testing.T) {
	cr := seedAgent("mira", "witwave-self", nil)
	changed, err := applyKubernetesApiAccessInPlace(cr, &KubernetesApiAccessSpec{
		Enabled: true,
		Mode:    KubernetesApiAccessModeReadOnly,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Fatal("expected Kubernetes API access change")
	}
	enabled, found, err := unstructured.NestedBool(cr.Object, "spec", "kubernetesApiAccess", "enabled")
	if err != nil || !found || !enabled {
		t.Fatalf("enabled = %v found=%v err=%v; want true", enabled, found, err)
	}
	mode, found, err := unstructured.NestedString(cr.Object, "spec", "kubernetesApiAccess", "mode")
	if err != nil || !found || mode != KubernetesApiAccessModeReadOnly {
		t.Fatalf("mode = %q found=%v err=%v; want readOnly", mode, found, err)
	}
}

func TestApplyKubernetesApiAccessInPlace_UpdatesMode(t *testing.T) {
	cr := seedAgent("mira", "witwave-self", func(spec map[string]interface{}) {
		spec["kubernetesApiAccess"] = map[string]interface{}{
			"enabled": true,
			"mode":    KubernetesApiAccessModeReadOnly,
		}
	})
	changed, err := applyKubernetesApiAccessInPlace(cr, &KubernetesApiAccessSpec{
		Enabled: true,
		Mode:    KubernetesApiAccessModeNamespaceWrite,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Fatal("expected mode update")
	}
	mode, _, _ := unstructured.NestedString(cr.Object, "spec", "kubernetesApiAccess", "mode")
	if mode != KubernetesApiAccessModeNamespaceWrite {
		t.Fatalf("mode = %q; want namespaceWrite", mode)
	}
}

func TestApplyKubernetesApiAccessInPlace_IsIdempotent(t *testing.T) {
	cr := seedAgent("mira", "witwave-self", func(spec map[string]interface{}) {
		spec["kubernetesApiAccess"] = map[string]interface{}{
			"enabled": true,
			"mode":    KubernetesApiAccessModeReadOnly,
		}
	})
	changed, err := applyKubernetesApiAccessInPlace(cr, &KubernetesApiAccessSpec{
		Enabled: true,
		Mode:    KubernetesApiAccessModeReadOnly,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Fatal("expected no changes")
	}
}

func TestKubernetesApiAccessPlanValue(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "namespaceWrite renders the namespace-write banner",
			in:   KubernetesApiAccessModeNamespaceWrite,
			want: "namespaceWrite (bounded namespace-local remediation; no secrets/RBAC/cluster resources)",
		},
		{
			name: "readOnly renders the read-only banner",
			in:   KubernetesApiAccessModeReadOnly,
			want: "readOnly (get/list/watch + pod logs; no mutating verbs)",
		},
		{
			name: "empty input falls through to the read-only banner",
			in:   "",
			want: "readOnly (get/list/watch + pod logs; no mutating verbs)",
		},
		{
			name: "unknown input falls through to the read-only banner",
			in:   "cluster-admin",
			want: "readOnly (get/list/watch + pod logs; no mutating verbs)",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := kubernetesApiAccessPlanValue(tc.in)
			if got != tc.want {
				t.Fatalf("kubernetesApiAccessPlanValue(%q) = %q; want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestRemoveKubernetesApiAccessInPlace(t *testing.T) {
	cr := seedAgent("mira", "witwave-self", func(spec map[string]interface{}) {
		spec["kubernetesApiAccess"] = map[string]interface{}{
			"enabled": true,
			"mode":    KubernetesApiAccessModeReadOnly,
		}
	})
	changed, err := removeKubernetesApiAccessInPlace(cr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Fatal("expected removal")
	}
	if _, found, err := unstructured.NestedMap(cr.Object, "spec", "kubernetesApiAccess"); err != nil || found {
		t.Fatalf("kubernetesApiAccess found=%v err=%v; want removed", found, err)
	}
}
