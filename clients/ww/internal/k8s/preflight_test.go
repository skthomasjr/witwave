package k8s

import (
	"bytes"
	"strings"
	"testing"
)

func TestIsLocalCluster(t *testing.T) {
	cases := []struct {
		name   string
		target *Target
		want   bool
	}{
		{"nil target", nil, false},
		{"empty target", &Target{}, false},

		// By context name
		{"kind cluster", &Target{Context: "kind-dev"}, true},
		{"minikube", &Target{Context: "minikube"}, true},
		{"docker-desktop", &Target{Context: "docker-desktop"}, true},
		{"rancher-desktop", &Target{Context: "rancher-desktop"}, true},
		{"orbstack", &Target{Context: "orbstack"}, true},
		{"k3d-x", &Target{Context: "k3d-test"}, true},
		{"colima", &Target{Context: "colima"}, true},
		{"colima-with-suffix", &Target{Context: "colima-profile1"}, true},

		// By server URL
		{"localhost server", &Target{Server: "https://localhost:6443"}, true},
		{"127.0.0.1 server", &Target{Server: "https://127.0.0.1:6443"}, true},
		{"docker-internal", &Target{Server: "https://kubernetes.docker.internal:6443"}, true},

		// Prod-looking — must NOT be classified local
		{"EKS ARN", &Target{Context: "arn:aws:eks:us-west-2:123:cluster/prod", Server: "https://abcd.eks.amazonaws.com"}, false},
		{"GKE", &Target{Context: "gke_myproject_us-central1-a_prod", Server: "https://34.1.2.3"}, false},
		{"bare name with remote IP", &Target{Context: "prod", Server: "https://10.0.0.5:6443"}, false},
		{"bare name no server", &Target{Context: "unknown"}, false},

		// Edge cases — substring-looking patterns must not match
		{"contains kind- in middle", &Target{Context: "my-kind-prod"}, false},
		{"contains minikube substring", &Target{Context: "minikube-prod"}, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := IsLocalCluster(c.target)
			if got != c.want {
				t.Errorf("IsLocalCluster(%+v) = %v; want %v", c.target, got, c.want)
			}
		})
	}
}

func TestConfirm_AssumeYes(t *testing.T) {
	var out bytes.Buffer
	// No stdin needed because AssumeYes short-circuits.
	ok, err := Confirm(&out, strings.NewReader(""),
		&Target{Context: "prod", Cluster: "prod-cluster", Server: "https://1.2.3.4"},
		[]PlanLine{{Key: "Chart", Value: "witwave-operator 0.4.4"}},
		PromptOptions{AssumeYes: true},
	)
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if !ok {
		t.Fatal("AssumeYes must return ok=true")
	}
	if !strings.Contains(out.String(), "prod-cluster") {
		t.Errorf("banner missing cluster name; got: %s", out.String())
	}
	if strings.Contains(out.String(), "Continue?") {
		t.Error("AssumeYes must not render the prompt")
	}
}

func TestConfirm_DryRun(t *testing.T) {
	var out bytes.Buffer
	ok, err := Confirm(&out, strings.NewReader(""),
		&Target{Context: "prod", Server: "https://1.2.3.4"},
		nil,
		PromptOptions{DryRun: true},
	)
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if ok {
		t.Fatal("DryRun must return ok=false so callers exit cleanly without acting")
	}
	if !strings.Contains(out.String(), "Dry-run mode") {
		t.Errorf("DryRun should emit a dry-run notice; got: %s", out.String())
	}
}

func TestConfirm_LocalSkipsPrompt(t *testing.T) {
	var out bytes.Buffer
	ok, err := Confirm(&out, strings.NewReader(""),
		&Target{Context: "kind-dev"},
		nil,
		PromptOptions{},
	)
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if !ok {
		t.Fatal("local cluster must auto-proceed without prompting")
	}
	if strings.Contains(out.String(), "Continue?") {
		t.Error("local cluster must not render the prompt")
	}
}

func TestConfirm_PromptYes(t *testing.T) {
	var out bytes.Buffer
	ok, err := Confirm(&out, strings.NewReader("y\n"),
		&Target{Context: "prod", Server: "https://1.2.3.4"},
		nil,
		PromptOptions{},
	)
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if !ok {
		t.Fatal("y response must yield ok=true")
	}
	if !strings.Contains(out.String(), "Continue?") {
		t.Error("production-looking target must render the prompt")
	}
}

func TestConfirm_PromptNo(t *testing.T) {
	var out bytes.Buffer
	for _, in := range []string{"n\n", "\n", "N\n", "no\n", ""} {
		ok, err := Confirm(&out, strings.NewReader(in),
			&Target{Context: "prod", Server: "https://1.2.3.4"},
			nil,
			PromptOptions{},
		)
		if err != nil {
			t.Fatalf("Confirm(%q): %v", in, err)
		}
		if ok {
			t.Errorf("response %q should decline but returned ok=true", in)
		}
	}
}
