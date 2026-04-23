package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
)

// ScaffoldOptions collects the runtime inputs for `ww agent scaffold`.
// See the cobra command in cmd/agent.go for the user-facing flag names.
type ScaffoldOptions struct {
	Name    string
	Group   string // optional; "" produces flat .agents/<name>/ layout
	Repo    string // accepted shorthands: owner/repo, host/owner/repo, full URL, git@host:…
	Branch  string // default "main"
	Backend string // one of agent.KnownBackends(); default echo

	// CommitMessage overrides the auto-generated commit subject. When
	// empty, the scaffolder uses "Scaffold agent <name>".
	CommitMessage string

	// CloneTo, when set, persists the clone at this path instead of
	// using a temp dir that's deleted at the end. Useful for users
	// who want to iterate on the skeleton locally before a follow-up
	// push.
	CloneTo string

	// NoPush stops after the commit is created so the caller can
	// inspect / amend / push manually. Primarily for CI + debugging.
	NoPush bool

	// DryRun renders the plan and the would-write file list without
	// touching the network or local disk. Complements `ww agent *`'s
	// established --dry-run semantics.
	DryRun bool

	// Force permits overwriting an existing `.agents/<group?>/<name>/`
	// tree. Without it, the scaffolder refuses — re-scaffolding on top
	// of populated content is rarely what the user meant.
	Force bool

	// NoHeartbeat skips writing `.witwave/HEARTBEAT.md`. Default false,
	// i.e. scaffold seeds an hourly heartbeat. This is a documented
	// exception to DESIGN.md SUB-4's "subsystems are dormant by
	// default" — a heartbeat-on-scaffold is the cheapest possible
	// proof-of-life that the dispatch path and backend sidecar
	// actually work; users who genuinely want a silent agent opt out.
	NoHeartbeat bool

	// CLIVersion from cmd.Version; used when any skeleton content
	// needs to reference a ww release (e.g. image tags in sample
	// commentary). Empty / "dev" falls through to "latest" markers.
	CLIVersion string

	// Out receives progress output. Defaults to os.Stdout for CLI use.
	Out io.Writer
}

// Scaffold materialises a ww-conformant agent directory structure on a
// remote git repo. See the command help in cmd/agent.go for the
// user-facing contract. Caller passes a context with an appropriate
// deadline; the clone + push steps honour cancellation.
func Scaffold(ctx context.Context, opts ScaffoldOptions) error {
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	if err := validateScaffoldOptions(&opts); err != nil {
		return err
	}
	ref, err := parseRepoRef(opts.Repo)
	if err != nil {
		return err
	}

	backend := opts.Backend
	if backend == "" {
		backend = DefaultBackend
	}
	branch := opts.Branch
	if branch == "" {
		// No explicit --branch → detect the remote's actual default by
		// asking its HEAD symref. This covers repos whose default is
		// `master`, `develop`, `trunk`, etc. without the user having to
		// remember to pass the flag. Empty repos (no HEAD yet) fall
		// through to the hardcoded fallback below.
		if detected := detectRemoteDefaultBranch(ref.CloneURL); detected != "" {
			branch = detected
			fmt.Fprintf(opts.Out, "Detected default branch: %s\n", branch)
		} else {
			branch = "main"
		}
	}

	skeleton := buildSkeleton(skeletonOpts{
		Name:        opts.Name,
		Group:       opts.Group,
		Backend:     backend,
		CLIVersion:  opts.CLIVersion,
		NoHeartbeat: opts.NoHeartbeat,
	})

	// Preflight banner — always renders, even under --dry-run.
	printScaffoldPlan(opts.Out, opts, ref, backend, branch, skeleton)

	if opts.DryRun {
		fmt.Fprintln(opts.Out, "Dry-run mode — no files written, no push.")
		return nil
	}

	// Resolve local clone path. CloneTo wins; otherwise a temp dir
	// that we clean up at the end (unless something fails, in which
	// case we leave it for forensics).
	cloneDir, cleanup, err := prepareCloneDir(opts.CloneTo, opts.Name)
	if err != nil {
		return fmt.Errorf("prepare clone dir: %w", err)
	}
	keepOnErr := true
	defer func() {
		if cleanup != nil && !keepOnErr {
			cleanup()
		}
	}()

	auth, err := resolveGitAuth(ref)
	if err != nil {
		return fmt.Errorf("resolve git auth: %w", err)
	}

	repo, wasEmpty, err := cloneOrInit(ctx, cloneDir, ref, branch, auth, opts.Out)
	if err != nil {
		return err
	}

	// Materialise skeleton on the working tree in merge mode:
	// - Missing files are written.
	// - Existing identical files are skipped silently.
	// - Existing differing files are preserved (default) or overwritten
	//   (--force). Files outside the skeleton list are never touched.
	//
	// Empty-repo short-circuit: when the remote had no commits yet,
	// nothing can possibly collide; we skip the preserved-path entirely
	// and just write everything.
	_ = wasEmpty // reserved for future "first-scaffold" optimisations
	outcome, err := writeSkeletonMerge(cloneDir, skeleton, opts.Force)
	if err != nil {
		return fmt.Errorf("write skeleton: %w", err)
	}
	reportOutcome(opts.Out, outcome, opts.Force)

	changed := append(append([]string{}, outcome.Added...), outcome.Overwrote...)
	if len(changed) == 0 {
		// Idempotent re-run or full drift preservation — nothing for
		// git to track. Exit clean, still print the next-step hints
		// so the user gets the same mental map on every invocation.
		printNextSteps(opts.Out, opts, ref, false)
		keepOnErr = false
		return nil
	}

	// Stage + commit only the files we actually changed. Preserved
	// files are not re-added to the index — they were never modified.
	commitMsg := opts.CommitMessage
	if commitMsg == "" {
		commitMsg = defaultCommitMessage(opts.Name, outcome)
	}
	sha, err := commitSkeleton(repo, changed, commitMsg)
	if err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	fmt.Fprintf(opts.Out, "Committed %s: %s\n", shortSHA(sha), commitMsg)

	if opts.NoPush {
		fmt.Fprintf(opts.Out, "Skipping push (--no-push). Clone retained at %s.\n", cloneDir)
		keepOnErr = false // intentional retention
		return nil
	}

	if err := pushBranch(ctx, repo, branch, auth, opts.Out); err != nil {
		return fmt.Errorf("push: %w", err)
	}

	printNextSteps(opts.Out, opts, ref, true)
	keepOnErr = false
	return nil
}

// printNextSteps emits the post-scaffold hints. Rendered on both the
// fresh-scaffold success path and the idempotent "nothing to change"
// path so users get consistent guidance regardless of invocation shape.
func printNextSteps(out io.Writer, opts ScaffoldOptions, ref repoRef, includeGitAdd bool) {
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Next steps:")
	fmt.Fprintf(out, "  ww agent create %s\n", opts.Name)
	fmt.Fprintf(out, "  ww agent send %s \"hello\"\n", opts.Name)
	if includeGitAdd {
		fmt.Fprintf(out,
			"\nWhen `ww agent git add` ships (Phase 2), wire the deployed agent to this repo:\n"+
				"  ww agent git add %s --repo %s\n", opts.Name, ref.Display,
		)
	}
}

func validateScaffoldOptions(opts *ScaffoldOptions) error {
	if err := ValidateName(opts.Name); err != nil {
		return err
	}
	if opts.Group != "" {
		if err := ValidateName(opts.Group); err != nil {
			return fmt.Errorf("group name: %w", err)
		}
	}
	if opts.Repo == "" {
		return errors.New("repo is required (--repo)")
	}
	if opts.Backend != "" && !IsKnownBackend(opts.Backend) {
		return fmt.Errorf("unknown backend %q; valid backends: %v",
			opts.Backend, KnownBackends())
	}
	return nil
}

// printScaffoldPlan is the preflight banner for scaffold. Mirrors the
// shape of k8s.Confirm's banner but doesn't gate on user confirmation
// — scaffold acts on an external git remote, not on the cluster, and
// the local-cluster heuristic doesn't apply.
func printScaffoldPlan(out io.Writer, opts ScaffoldOptions, ref repoRef, backend, branch string, files []skeletonFile) {
	const pad = 14
	fmt.Fprintln(out, "")
	fmt.Fprintf(out, "%-*s %s\n", pad, "Action:", fmt.Sprintf("scaffold agent %q", opts.Name))
	fmt.Fprintf(out, "%-*s %s\n", pad, "Repo:", ref.Display)
	fmt.Fprintf(out, "%-*s %s\n", pad, "Branch:", branch)
	if opts.Group != "" {
		fmt.Fprintf(out, "%-*s %s\n", pad, "Group:", opts.Group)
	}
	fmt.Fprintf(out, "%-*s %s\n", pad, "Backend:", backend)
	fmt.Fprintf(out, "%-*s %s\n", pad, "Files:", fmt.Sprintf("%d", len(files)))
	for _, f := range files {
		fmt.Fprintf(out, "  - %s\n", f.Path)
	}
	fmt.Fprintln(out, "")
}

// prepareCloneDir returns an absolute path to clone into plus a
// cleanup function. When cloneTo is empty, creates a temp dir that the
// cleanup removes; when cloneTo is set, returns a no-op cleanup and
// preserves the caller-supplied path.
func prepareCloneDir(cloneTo, agentName string) (string, func(), error) {
	if cloneTo != "" {
		abs, err := filepath.Abs(cloneTo)
		if err != nil {
			return "", nil, err
		}
		if err := os.MkdirAll(abs, 0o755); err != nil {
			return "", nil, err
		}
		entries, err := os.ReadDir(abs)
		if err != nil {
			return "", nil, err
		}
		if len(entries) > 0 {
			return "", nil, fmt.Errorf(
				"--clone-to %q is not empty (%d entries); refusing to clone on top of existing content",
				abs, len(entries),
			)
		}
		return abs, nil, nil
	}
	dir, err := os.MkdirTemp("", fmt.Sprintf("ww-scaffold-%s-", agentName))
	if err != nil {
		return "", nil, err
	}
	return dir, func() { _ = os.RemoveAll(dir) }, nil
}

// cloneOrInit clones the remote into dir. When the remote is an empty
// repository (no commits yet — GitHub reports this as an "empty
// repository" transport error), we init a fresh repo locally with a
// branch set to the requested name and add the remote. Returns the
// bound *gogit.Repository and whether the remote was empty.
func cloneOrInit(
	ctx context.Context,
	dir string,
	ref repoRef,
	branch string,
	auth transport.AuthMethod,
	out io.Writer,
) (*gogit.Repository, bool, error) {
	fmt.Fprintf(out, "Cloning %s …\n", ref.Display)
	cloneOpts := &gogit.CloneOptions{
		URL:           ref.CloneURL,
		Auth:          auth,
		SingleBranch:  true,
		ReferenceName: plumbing.NewBranchReferenceName(branch),
		Depth:         1,
	}
	repo, err := gogit.PlainCloneContext(ctx, dir, false, cloneOpts)
	if err == nil {
		return repo, false, nil
	}
	// Empty remote. go-git reports this through transport.ErrEmptyRemoteRepository.
	if errors.Is(err, transport.ErrEmptyRemoteRepository) {
		fmt.Fprintln(out, "Remote repository is empty — initialising local branch and bootstrapping first commit.")
		repo, err := gogit.PlainInit(dir, false)
		if err != nil {
			return nil, true, fmt.Errorf("init empty repo locally: %w", err)
		}
		if _, err := repo.CreateRemote(&config.RemoteConfig{
			Name: "origin",
			URLs: []string{ref.CloneURL},
		}); err != nil {
			return nil, true, fmt.Errorf("add origin remote: %w", err)
		}
		// Force HEAD to the requested branch — default PlainInit uses
		// "master" which (a) is stylistically wrong, (b) may not match
		// what the remote expects once the branch is created. Writing
		// a symbolic ref without content is legal; the first commit
		// will populate it.
		if err := repo.Storer.SetReference(plumbing.NewSymbolicReference(
			plumbing.HEAD, plumbing.NewBranchReferenceName(branch),
		)); err != nil {
			return nil, true, fmt.Errorf("set HEAD to %s: %w", branch, err)
		}
		return repo, true, nil
	}
	// "reference not found" means the requested branch doesn't exist
	// on a non-empty remote. Distinct from empty-repo; caller should
	// pick a different branch explicitly.
	if errors.Is(err, plumbing.ErrReferenceNotFound) {
		return nil, false, fmt.Errorf(
			"branch %q not found on %s. If the remote has a different default branch, "+
				"pass --branch <name> explicitly", branch, ref.Display,
		)
	}
	return nil, false, fmt.Errorf("clone %s: %w", ref.Display, err)
}

// scaffoldOutcome records what happened to each skeleton file during a
// merge. The four buckets let `reportOutcome` produce an accurate,
// non-alarming summary — in particular distinguishing "these files are
// already exactly what we'd scaffold" (Identical; silent) from "we
// kept your edits" (Preserved; needs to be called out) from "we wrote
// new content" (Added / Overwrote; commit candidates).
type scaffoldOutcome struct {
	Added     []string // didn't exist before; freshly written
	Identical []string // existed and matched the template byte-for-byte
	Preserved []string // existed and differed; left alone (default) — pass --force to overwrite
	Overwrote []string // existed and differed; overwritten (--force was set)
}

// writeSkeletonMerge materialises the skeleton into `root`, preserving
// any existing file whose content differs from the template. Pass
// force=true to replace drifted files with the current template.
// Files outside the skeleton list are never read, written, or touched
// — user-added content (jobs/, tasks/, customised HEARTBEAT.md, …) is
// safe regardless of force.
func writeSkeletonMerge(root string, files []skeletonFile, force bool) (scaffoldOutcome, error) {
	var out scaffoldOutcome
	for _, f := range files {
		full := filepath.Join(root, filepath.FromSlash(f.Path))
		existing, readErr := os.ReadFile(full)
		switch {
		case readErr != nil && os.IsNotExist(readErr):
			// Fresh write.
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				return out, err
			}
			if err := os.WriteFile(full, []byte(f.Content), 0o644); err != nil {
				return out, err
			}
			out.Added = append(out.Added, f.Path)
		case readErr != nil:
			return out, fmt.Errorf("read %s: %w", f.Path, readErr)
		case string(existing) == f.Content:
			out.Identical = append(out.Identical, f.Path)
		case force:
			// Drifted + caller opted in to overwrite.
			if err := os.WriteFile(full, []byte(f.Content), 0o644); err != nil {
				return out, err
			}
			out.Overwrote = append(out.Overwrote, f.Path)
		default:
			// Drifted + default mode — user's content wins.
			out.Preserved = append(out.Preserved, f.Path)
		}
	}
	sort.Strings(out.Added)
	sort.Strings(out.Identical)
	sort.Strings(out.Preserved)
	sort.Strings(out.Overwrote)
	return out, nil
}

// reportOutcome prints a human-readable summary of what the merge did.
// Skipped (Identical) files are silent — they're a normal re-run
// idempotence signal, not something the user needs to see. Preserved
// files are called out explicitly so users know (a) their edits weren't
// blown away and (b) what flag to pass if they actually wanted the
// scaffold template back.
func reportOutcome(out io.Writer, outcome scaffoldOutcome, force bool) {
	switch {
	case len(outcome.Added) == 0 && len(outcome.Overwrote) == 0 &&
		len(outcome.Preserved) == 0 && len(outcome.Identical) > 0:
		fmt.Fprintln(out, "Already up to date — scaffold files match the current template.")
	case len(outcome.Added) == 0 && len(outcome.Overwrote) == 0 && len(outcome.Preserved) > 0:
		fmt.Fprintf(out,
			"No changes needed — %d file(s) already exist with local edits (preserved). Pass --force to overwrite with the current template.\n",
			len(outcome.Preserved),
		)
	default:
		if len(outcome.Added) > 0 {
			fmt.Fprintf(out, "Added %d file(s):\n", len(outcome.Added))
			for _, p := range outcome.Added {
				fmt.Fprintf(out, "  + %s\n", p)
			}
		}
		if len(outcome.Overwrote) > 0 {
			fmt.Fprintf(out, "Overwrote %d file(s) (--force):\n", len(outcome.Overwrote))
			for _, p := range outcome.Overwrote {
				fmt.Fprintf(out, "  ~ %s\n", p)
			}
		}
	}
	// Preserved is worth calling out even on the happy-path "we also
	// added some files" case — the user needs to know their edits
	// survived in every scenario.
	if len(outcome.Preserved) > 0 && !force {
		fmt.Fprintf(out, "Preserved %d file(s) with local edits (pass --force to overwrite):\n",
			len(outcome.Preserved),
		)
		for _, p := range outcome.Preserved {
			fmt.Fprintf(out, "  = %s\n", p)
		}
	}
}

// defaultCommitMessage builds a commit message that reflects what the
// merge actually did. A pure "added N files" re-run shouldn't be
// called "Scaffold agent X" — that misrepresents the history and
// makes `git log` harder to read.
func defaultCommitMessage(name string, outcome scaffoldOutcome) string {
	switch {
	case len(outcome.Added) > 0 && len(outcome.Overwrote) == 0:
		if len(outcome.Added) == len(outcome.Added)+len(outcome.Identical)+len(outcome.Preserved)+len(outcome.Overwrote) {
			return fmt.Sprintf("Scaffold agent %s", name)
		}
		return fmt.Sprintf("Scaffold agent %s: add %d missing file(s)",
			name, len(outcome.Added))
	case len(outcome.Overwrote) > 0 && len(outcome.Added) == 0:
		return fmt.Sprintf("Scaffold agent %s: refresh %d scaffolded file(s)",
			name, len(outcome.Overwrote))
	case len(outcome.Added) > 0 && len(outcome.Overwrote) > 0:
		return fmt.Sprintf("Scaffold agent %s: add %d + refresh %d file(s)",
			name, len(outcome.Added), len(outcome.Overwrote))
	}
	return fmt.Sprintf("Scaffold agent %s", name)
}

// commitSkeleton stages every file in `paths` and creates a commit.
// Author + committer default to whatever go-git resolves from the
// user's ~/.gitconfig. A trailer marks ww as the mint source so
// future-you can tell scaffolded commits from hand-authored ones.
func commitSkeleton(repo *gogit.Repository, paths []string, message string) (plumbing.Hash, error) {
	wt, err := repo.Worktree()
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("worktree: %w", err)
	}
	for _, p := range paths {
		if _, err := wt.Add(p); err != nil {
			return plumbing.ZeroHash, fmt.Errorf("add %s: %w", p, err)
		}
	}
	sig := signatureFromGitConfig()
	fullMsg := message
	if !strings.Contains(fullMsg, "Scaffolded-by:") {
		fullMsg = fullMsg + "\n\nScaffolded-by: ww agent scaffold\n"
	}
	hash, err := wt.Commit(fullMsg, &gogit.CommitOptions{Author: sig, Committer: sig})
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("commit: %w", err)
	}
	return hash, nil
}

// signatureFromGitConfig builds a git signature using the user's
// global git config. Falls back to a plausible default if config is
// unreadable — this matters for CI environments where user.name /
// user.email aren't set. A clear marker ("ww-agent-scaffold") in the
// fallback makes the origin obvious in `git log`.
func signatureFromGitConfig() *object.Signature {
	name, email := readGitIdentity()
	if name == "" {
		name = "ww-agent-scaffold"
	}
	if email == "" {
		email = "ww-agent-scaffold@localhost"
	}
	return &object.Signature{Name: name, Email: email, When: time.Now()}
}

// readGitIdentity returns the user's configured git author. Uses
// `git config --get` as a subprocess — matches what go-git itself
// would try to discover, but surfaces errors cleanly.
var readGitIdentity = func() (name, email string) {
	return gitConfigGet("user.name"), gitConfigGet("user.email")
}

// pushBranch pushes the given local branch to origin. For the empty-
// repo / bootstrap case the push creates the branch on the remote;
// for the populated case it fast-forwards an existing branch. Either
// way, a force-push is never performed — go-git's default RefSpec
// includes the "+" only when explicitly requested.
func pushBranch(ctx context.Context, repo *gogit.Repository, branch string, auth transport.AuthMethod, out io.Writer) error {
	fmt.Fprintf(out, "Pushing %s to origin …\n", branch)
	refSpec := config.RefSpec(fmt.Sprintf(
		"refs/heads/%[1]s:refs/heads/%[1]s", branch,
	))
	err := repo.PushContext(ctx, &gogit.PushOptions{
		RemoteName: "origin",
		Auth:       auth,
		RefSpecs:   []config.RefSpec{refSpec},
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Pushed %s.\n", branch)
	return nil
}

func shortSHA(h plumbing.Hash) string {
	s := h.String()
	if len(s) < 7 {
		return s
	}
	return s[:7]
}
