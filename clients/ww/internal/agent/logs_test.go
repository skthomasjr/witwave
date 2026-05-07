package agent

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// Tests for the all-containers-by-default Logs path landed 2026-05-07.
// Cover the container-resolution helper + the prefix-shortening helper
// without spinning up real log streams.

func TestShortPodSuffix(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"replicaset shape", "evan-786489b5fc-vt5wx", "vt5wx"},
		{"single hyphen", "evan-vt5wx", "vt5wx"},
		{"no hyphen", "standalone", "standalone"},
		{"empty", "", ""},
		{"trailing hyphen", "evan-", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shortPodSuffix(tc.in); got != tc.want {
				t.Errorf("shortPodSuffix(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestPodContainerNames(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "evan-abc-xyz", Namespace: "witwave-self"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "harness"},
				{Name: "claude"},
				{Name: "git-sync"},
			},
		},
	}
	client := fake.NewClientset(pod)

	t.Run("no filter returns all containers in spec order", func(t *testing.T) {
		got, err := podContainerNames(context.Background(), client, "witwave-self", "evan-abc-xyz", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []string{"harness", "claude", "git-sync"}
		if len(got) != len(want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("at index %d: got %q, want %q", i, got[i], want[i])
			}
		}
	})

	t.Run("named filter returns just that container", func(t *testing.T) {
		got, err := podContainerNames(context.Background(), client, "witwave-self", "evan-abc-xyz", "claude")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 || got[0] != "claude" {
			t.Errorf("got %v, want [claude]", got)
		}
	})

	t.Run("missing container returns helpful error", func(t *testing.T) {
		_, err := podContainerNames(context.Background(), client, "witwave-self", "evan-abc-xyz", "clude") // typo
		if err == nil {
			t.Fatal("expected error for unknown container, got nil")
		}
		// The error must surface the available container names so the
		// user can spot the typo without re-querying. Match a substring
		// rather than the full string so cosmetic tweaks don't break.
		msg := err.Error()
		for _, want := range []string{"clude", "harness", "claude", "git-sync"} {
			if !strings.Contains(msg, want) {
				t.Errorf("error %q doesn't mention %q", msg, want)
			}
		}
	})

	t.Run("missing pod surfaces a get error", func(t *testing.T) {
		_, err := podContainerNames(context.Background(), client, "witwave-self", "no-such-pod", "")
		if err == nil {
			t.Fatal("expected error for missing pod, got nil")
		}
	})
}
