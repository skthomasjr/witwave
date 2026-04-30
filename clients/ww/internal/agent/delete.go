package agent

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	gogit "github.com/go-git/go-git/v5"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"

	"github.com/witwave-ai/witwave/clients/ww/internal/k8s"
)

// DeleteOptions controls the `ww agent delete` flow.
type DeleteOptions struct {
	Name      string
	Namespace string

	// RemoveRepoFolder, when true, also wipes the agent's directory
	// (`.agents/<name>/` or `.agents/<group>/<name>/`) from the gitSync
	// repo: clone, git rm -r, commit, push. Runs BEFORE the CR is
	// deleted — if the repo-side fails, the cluster state is preserved
	// so a re-run can retry. Default false: the CRD and the repo are
	// orthogonal, and a user who wants the config preserved for later
	// re-attach should keep the folder.
	RemoveRepoFolder bool

	// DeleteGitSecret, when true, also deletes every ww-managed
	// credential Secret referenced by the CR's gitSyncs[]. User-
	// created Secrets under the same names are preserved — the
	// `app.kubernetes.io/managed-by: ww` label gates deletion.
	DeleteGitSecret bool

	// KeepBackendSecrets, when true, preserves the ww-managed
	// credential Secrets referenced by the CR's
	// spec.backends[].credentials.existingSecret. Default behaviour
	// is to delete them — backend Secrets are per-agent (their name
	// embeds the agent's name), so they're unambiguous orphans
	// after the agent is gone, and the per-backend PVCs already
	// cascade-delete via owner refs (data is more destructive than
	// the Secret would be). Same label-gated safety regardless:
	// Secrets without `app.kubernetes.io/managed-by: ww` are never
	// touched.
	KeepBackendSecrets bool

	// CommitMessage overrides the auto-generated commit subject on the
	// repo-side wipe. Empty → "Remove agent <name>".
	CommitMessage string

	AssumeYes bool
	DryRun    bool
	Out       io.Writer
	In        io.Reader
}

// Delete removes the WitwaveAgent CR. The operator handles pod/Service
// teardown via owner references — we don't cascade manually. Prints a
// preflight banner before acting (DESIGN.md KC-4).
//
// When --remove-repo-folder is set, the agent's directory on the wired
// gitSync repo is wiped (clone → git rm -r → commit → push) BEFORE the
// CR is deleted, so a repo-side failure leaves the cluster state intact
// and the user can retry. Requires exactly one gitSync on the agent —
// multiple syncs are refused as ambiguous with a clear error.
//
// When --delete-git-secret is set, every ww-managed credential Secret
// referenced by the CR's gitSyncs[] is deleted AFTER the CR is removed.
// Secrets without the ww managed-by label are preserved regardless.
func Delete(
	ctx context.Context,
	target *k8s.Target,
	cfg *rest.Config,
	opts DeleteOptions,
) error {
	if opts.Out == nil {
		return fmt.Errorf("DeleteOptions.Out is required")
	}

	dyn, err := newDynamicClient(cfg)
	if err != nil {
		return err
	}

	// Fetch the CR up-front. Even the baseline delete benefits from
	// this: a clean "not found" error here is nicer than the one from
	// the Delete call. When --remove-repo-folder or --delete-git-secret
	// is set, we need the CR content to resolve repo / secret names.
	cr, err := fetchAgentCR(ctx, dyn, opts.Namespace, opts.Name)
	if err != nil {
		return err
	}

	// Resolve repo + secret state ahead of time so the confirmation
	// banner can describe the full blast radius.
	var (
		repoScope       deleteRepoScope
		repoScopeReason string
		managedSecrets  []string
	)
	if opts.RemoveRepoFolder {
		var err error
		repoScope, repoScopeReason, err = resolveDeleteRepoScope(cr)
		if err != nil {
			return err
		}
		// Hard-fail on ambiguity or broken CR state — quietly skipping
		// when the user asked for `--remove-repo-folder` is the worst-of-
		// both-worlds outcome. Soft-skip stays only for the benign "no
		// gitSync configured" case (nothing plausible to wipe anyway).
		if repoScope.agentDir == "" {
			switch {
			case strings.Contains(repoScopeReason, "no gitSync configured"):
				// Soft-skip: plan banner explains it, user sees they
				// got nothing destroyed in the repo, continues.
			default:
				return fmt.Errorf("--remove-repo-folder: %s", repoScopeReason)
			}
		}
	}
	if opts.DeleteGitSecret {
		managedSecrets = collectGitSecretNames(cr)
	}
	// Backend Secrets default to delete (per-agent naming makes
	// orphans unambiguous; the cascading PVCs are already more
	// destructive). --keep-backend-secrets is the opt-out.
	var backendSecrets []string
	if !opts.KeepBackendSecrets {
		backendSecrets = collectBackendCredentialSecretNames(cr)
	}

	plan := []k8s.PlanLine{
		{Key: "Action", Value: fmt.Sprintf("delete WitwaveAgent %q", opts.Name)},
		{Key: "Cascade", Value: "operator removes pods + Services via owner refs"},
	}
	if opts.RemoveRepoFolder {
		if repoScope.agentDir != "" {
			plan = append(plan, k8s.PlanLine{
				Key:   "Repo",
				Value: fmt.Sprintf("git rm -r %s in %s (branch %s)", repoScope.agentDir, repoScope.repoDisplay, repoScope.branch),
			})
		} else {
			plan = append(plan, k8s.PlanLine{
				Key:   "Repo",
				Value: fmt.Sprintf("repo-side wipe skipped — %s", repoScopeReason),
			})
		}
	}
	if opts.DeleteGitSecret {
		if len(managedSecrets) == 0 {
			plan = append(plan, k8s.PlanLine{
				Key:   "Secrets",
				Value: "no gitSync credentials referenced — nothing to delete",
			})
		} else {
			plan = append(plan, k8s.PlanLine{
				Key:   "Secrets",
				Value: fmt.Sprintf("delete ww-managed gitSync Secret(s): %s", strings.Join(managedSecrets, ", ")),
			})
		}
	}
	if opts.KeepBackendSecrets {
		// Opt-out path: explicit "we're preserving them" line so
		// the banner doesn't go silent on a relevant decision.
		preserved := collectBackendCredentialSecretNames(cr)
		switch {
		case len(preserved) == 0:
			// No backend Secrets exist; nothing to preserve and
			// nothing to delete. Skip the line entirely.
		default:
			plan = append(plan, k8s.PlanLine{
				Key:   "Backend Secrets",
				Value: fmt.Sprintf("preserve ww-managed backend Secret(s): %s", strings.Join(preserved, ", ")),
			})
		}
	} else if len(backendSecrets) > 0 {
		plan = append(plan, k8s.PlanLine{
			Key:   "Backend Secrets",
			Value: fmt.Sprintf("delete ww-managed backend Secret(s): %s", strings.Join(backendSecrets, ", ")),
		})
	}

	proceed, err := k8s.Confirm(opts.Out, opts.In, target, plan, k8s.PromptOptions{
		AssumeYes: opts.AssumeYes,
		DryRun:    opts.DryRun,
	})
	if err != nil {
		return err
	}
	if !proceed {
		return nil
	}

	// Phase 1 — repo-side wipe (best-effort-wrapped but fatal to the
	// overall operation when it fails, because a half-clean state here
	// is the actually-dangerous one). Runs BEFORE CR deletion so the
	// user can retry without re-creating the agent.
	if opts.RemoveRepoFolder && repoScope.agentDir != "" {
		if err := runDeleteRepoWipe(ctx, repoScope, opts); err != nil {
			return fmt.Errorf("repo-side wipe failed (CR preserved so you can retry): %w", err)
		}
	} else if opts.RemoveRepoFolder {
		fmt.Fprintf(opts.Out, "Skipped repo-side wipe — %s\n", repoScopeReason)
	}

	// Phase 2 — CR deletion.
	if err := dyn.Resource(GVR()).Namespace(opts.Namespace).Delete(ctx, opts.Name, metav1.DeleteOptions{}); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("WitwaveAgent %q not found in namespace %q", opts.Name, opts.Namespace)
		}
		return fmt.Errorf("delete agent: %w", err)
	}
	fmt.Fprintf(opts.Out, "Deleted WitwaveAgent %s from namespace %s.\n", opts.Name, opts.Namespace)

	// Phase 3 — credential Secret reap. Soft-failure (warn, don't
	// error) because the CR is already gone; a stuck Secret is a
	// minor cleanup item, not an incident.
	needSecretReap := (opts.DeleteGitSecret && len(managedSecrets) > 0) ||
		(!opts.KeepBackendSecrets && len(backendSecrets) > 0)
	if needSecretReap {
		k8sClient, err := newKubernetesClient(cfg)
		if err != nil {
			fmt.Fprintf(opts.Out, "WARNING: build kubernetes client: %v\n", err)
			return nil
		}
		for _, secret := range managedSecrets {
			if err := deleteIfWWManaged(ctx, k8sClient, opts.Namespace, secret, opts.Out); err != nil {
				fmt.Fprintf(opts.Out, "WARNING: delete Secret %s/%s: %v\n", opts.Namespace, secret, err)
			}
		}
		for _, secret := range backendSecrets {
			if err := deleteIfWWManaged(ctx, k8sClient, opts.Namespace, secret, opts.Out); err != nil {
				fmt.Fprintf(opts.Out, "WARNING: delete Secret %s/%s: %v\n", opts.Namespace, secret, err)
			}
		}
	}
	return nil
}

// collectBackendCredentialSecretNames returns the distinct set of
// Secret names referenced by the CR's
// spec.backends[].credentials.existingSecret fields. Mirrors
// collectGitSecretNames but for the backend-credential side. Callers
// filter to ww-managed ones at deletion time via the managed-by
// label gate.
func collectBackendCredentialSecretNames(cr *unstructured.Unstructured) []string {
	backends, err := readBackends(cr)
	if err != nil {
		return nil
	}
	seen := make(map[string]struct{}, len(backends))
	out := make([]string, 0, len(backends))
	for _, b := range backends {
		creds, _ := b["credentials"].(map[string]interface{})
		if creds == nil {
			continue
		}
		name, _ := creds["existingSecret"].(string)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

// deleteRepoScope carries the repo-wipe parameters derived once from
// the CR and reused by both the plan-banner rendering and the actual
// git operation.
type deleteRepoScope struct {
	repoURL     string
	repoDisplay string
	branch      string
	agentDir    string // repo-relative path to the agent's subtree — the full `git rm -r` target
}

// resolveDeleteRepoScope inspects the CR and returns the agent-dir
// scope for a repo-side wipe. Returns (zero-scope, reason, nil) when
// repo work isn't applicable but the situation is benign (no gitSyncs,
// multiple gitSyncs, missing harness mapping). Returns an error only
// for hard failures — unparseable repo URLs, malformed CR.
func resolveDeleteRepoScope(cr *unstructured.Unstructured) (deleteRepoScope, string, error) {
	syncs, err := readGitSyncs(cr)
	if err != nil {
		return deleteRepoScope{}, "", fmt.Errorf("read gitSyncs: %w", err)
	}
	switch len(syncs) {
	case 0:
		return deleteRepoScope{}, "no gitSync configured on this agent", nil
	case 1:
		// fall through
	default:
		return deleteRepoScope{}, fmt.Sprintf(
			"%d gitSyncs configured — repo-side wipe is ambiguous; detach unused syncs first with `ww agent git remove`",
			len(syncs)), nil
	}
	sync := syncs[0]

	// Same agent-dir discovery as backend rename/remove: back out the
	// agent's path from the harness mapping's `<agentDir>/.witwave/` src.
	harnessMaps, _ := readHarnessGitMappings(cr)
	var agentDir string
	for _, m := range harnessMaps {
		src, _ := m["src"].(string)
		if strings.HasSuffix(src, "/.witwave/") {
			agentDir = strings.TrimSuffix(src, "/.witwave/")
			break
		}
	}
	if agentDir == "" {
		return deleteRepoScope{}, "no harness gitMapping anchoring .witwave/ — can't locate agent dir", nil
	}

	repoURL, _ := sync["repo"].(string)
	branch, _ := sync["ref"].(string)
	if branch == "" {
		branch = "main"
	}
	ref, refErr := parseRepoRef(repoURL)
	display := repoURL
	if refErr == nil {
		display = ref.Display
	}
	return deleteRepoScope{
		repoURL:     repoURL,
		repoDisplay: display,
		branch:      branch,
		agentDir:    agentDir,
	}, "", nil
}

// collectGitSecretNames returns the distinct set of Secret names
// referenced by the CR's gitSyncs[].credentials.existingSecret fields.
// Callers filter to ww-managed ones at deletion time via the managed-by
// label gate.
func collectGitSecretNames(cr *unstructured.Unstructured) []string {
	syncs, err := readGitSyncs(cr)
	if err != nil {
		return nil
	}
	seen := make(map[string]struct{}, len(syncs))
	out := make([]string, 0, len(syncs))
	for _, s := range syncs {
		creds, _ := s["credentials"].(map[string]interface{})
		if creds == nil {
			continue
		}
		name, _ := creds["existingSecret"].(string)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

// runDeleteRepoWipe performs the clone → git rm -r agentDir → commit
// → push cycle. Borrows the same cloneOrInit + pushBranch helpers the
// rename/remove flows use so behaviour (auth resolution, init-on-empty,
// signature fallback) stays consistent across verbs.
func runDeleteRepoWipe(ctx context.Context, scope deleteRepoScope, opts DeleteOptions) error {
	ref, err := parseRepoRef(scope.repoURL)
	if err != nil {
		return fmt.Errorf("parse repo %q: %w", scope.repoURL, err)
	}
	auth, err := resolveGitAuth(ref)
	if err != nil {
		return fmt.Errorf("resolve git auth: %w", err)
	}

	tmp, err := os.MkdirTemp("", fmt.Sprintf("ww-agent-delete-%s-", opts.Name))
	if err != nil {
		return fmt.Errorf("temp dir: %w", err)
	}
	defer os.RemoveAll(tmp)

	repo, _, err := cloneOrInit(ctx, tmp, ref, scope.branch, auth, opts.Out)
	if err != nil {
		return err
	}

	folderAbs := filepath.Join(tmp, filepath.FromSlash(scope.agentDir))
	if _, statErr := os.Stat(folderAbs); os.IsNotExist(statErr) {
		fmt.Fprintf(opts.Out, "Repo folder %q not present — nothing to wipe on the repo side.\n", scope.agentDir)
		return nil
	}

	cmd := exec.CommandContext(ctx, "git", "-C", tmp, "rm", "-r", scope.agentDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git rm -r %s: %w (output: %s)", scope.agentDir, err, strings.TrimSpace(string(out)))
	}

	msg := opts.CommitMessage
	if msg == "" {
		msg = fmt.Sprintf("Remove agent %s", opts.Name)
	}
	msg += "\n\nRemoved-by: ww agent delete\n"

	wt, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("worktree: %w", err)
	}
	status, err := wt.Status()
	if err != nil {
		return fmt.Errorf("worktree status: %w", err)
	}
	if status.IsClean() {
		fmt.Fprintln(opts.Out, "Repo already in sync — no commit.")
		return nil
	}

	sig := signatureFromGitConfig()
	hash, err := wt.Commit(msg, &gogit.CommitOptions{Author: sig, Committer: sig})
	if err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	fmt.Fprintf(opts.Out, "Committed %s: %s\n", shortSHA(hash), strings.Split(msg, "\n")[0])
	return pushBranch(ctx, repo, scope.branch, auth, opts.Out)
}
