package k8s

import (
	"bufio"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
)

// IsLocalCluster classifies a Target as "local/dev" per the prompt-vs-skip
// heuristic agreed in issue #1477.
//
// A target is local when ANY of:
//
//   - Context name matches a known local-cluster pattern
//     (kind-*, minikube, docker-desktop, rancher-desktop, orbstack,
//     k3d-*, colima, colima-*).
//   - Server URL host is localhost / 127.0.0.1 / ::1 /
//     kubernetes.docker.internal.
//
// Otherwise it's treated as production-looking and callers must prompt.
// The exact match list is intentionally narrow — false positives here
// mean we skip a confirmation on a real cluster, which is the worse
// failure mode. When in doubt, we prompt.
func IsLocalCluster(t *Target) bool {
	if t == nil {
		return false
	}
	if isLocalContextName(t.Context) {
		return true
	}
	if isLocalServer(t.Server) {
		return true
	}
	return false
}

var localContextPatterns = []*regexp.Regexp{
	regexp.MustCompile(`^kind-.+$`),
	regexp.MustCompile(`^minikube$`),
	regexp.MustCompile(`^docker-desktop$`),
	regexp.MustCompile(`^rancher-desktop$`),
	regexp.MustCompile(`^orbstack$`),
	regexp.MustCompile(`^k3d-.+$`),
	regexp.MustCompile(`^colima(-.+)?$`),
}

func isLocalContextName(name string) bool {
	if name == "" {
		return false
	}
	for _, p := range localContextPatterns {
		if p.MatchString(name) {
			return true
		}
	}
	return false
}

var localHosts = map[string]struct{}{
	"localhost":                   {},
	"127.0.0.1":                   {},
	"::1":                         {},
	"kubernetes.docker.internal":  {},
}

func isLocalServer(server string) bool {
	if server == "" {
		return false
	}
	u, err := url.Parse(server)
	if err != nil {
		return false
	}
	h := u.Hostname()
	_, ok := localHosts[strings.ToLower(h)]
	return ok
}

// PromptOptions configures Confirm. Embed directly into command-level
// flag structs.
type PromptOptions struct {
	// AssumeYes short-circuits confirmation. Set by --yes / -y or the
	// WW_ASSUME_YES=true env var.
	AssumeYes bool
	// DryRun means "print the plan and exit" — no prompt, no mutation.
	// Callers decide whether to treat DryRun as success.
	DryRun bool
}

// PlanLine represents a single "Key: value" row in the preflight banner.
// Order is preserved; callers control which lines appear.
type PlanLine struct {
	Key   string
	Value string
}

// Confirm prints the preflight banner and returns whether the caller
// should proceed. Rules per #1477:
//
//   - opts.DryRun → print banner, return false (caller should treat
//     as success + exit cleanly).
//   - opts.AssumeYes → print banner, return true (no prompt).
//   - IsLocalCluster(t) → print banner, return true (no prompt on
//     local dev clusters).
//   - otherwise → print banner, read y/N from `in`, return the answer.
//
// Writes go to `out`; reads come from `in`. Caller usually passes
// os.Stdout and os.Stdin.
func Confirm(out io.Writer, in io.Reader, t *Target, plan []PlanLine, opts PromptOptions) (bool, error) {
	if t == nil {
		return false, fmt.Errorf("Confirm: nil Target")
	}

	// Banner first — same shape regardless of what the prompt logic decides.
	printBanner(out, t, plan)

	switch {
	case opts.DryRun:
		fmt.Fprintln(out, "Dry-run mode — no changes applied.")
		return false, nil
	case opts.AssumeYes:
		return true, nil
	case IsLocalCluster(t):
		// Skip the prompt on local clusters — banner already printed.
		return true, nil
	}

	fmt.Fprint(out, "Continue? [y/N] ")
	reader := bufio.NewReader(in)
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return false, fmt.Errorf("read confirmation: %w", err)
	}
	resp := strings.ToLower(strings.TrimSpace(line))
	return resp == "y" || resp == "yes", nil
}

func printBanner(out io.Writer, t *Target, plan []PlanLine) {
	// Fixed-width key column so the banner columns line up regardless
	// of which lines the caller includes.
	const pad = 16
	fmt.Fprintln(out, "")
	fmt.Fprintf(out, "%-*s %s  (context: %s)\n", pad, "Target cluster:", displayServer(t), t.Context)
	fmt.Fprintf(out, "%-*s %s\n", pad, "Namespace:", t.Namespace)
	for _, pl := range plan {
		fmt.Fprintf(out, "%-*s %s\n", pad, pl.Key+":", pl.Value)
	}
	fmt.Fprintln(out, "")
}

// displayServer returns a compact string for the "Target cluster" line.
// We prefer the Cluster nickname when set (EKS/GKE ARNs can be huge);
// fall back to Server URL.
func displayServer(t *Target) string {
	if t.Cluster != "" {
		return t.Cluster
	}
	return t.Server
}
