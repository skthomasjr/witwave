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

	// Collision check — refuse unless --force. Empty-repo case short-
	// circuits: nothing to collide with.
	if !wasEmpty && !opts.Force {
		if err := refuseOnExisting(cloneDir, opts.Name, opts.Group); err != nil {
			return err
		}
	}

	// Materialise skeleton on the working tree. Directories are
	// auto-created per file.
	created, err := writeSkeleton(cloneDir, skeleton)
	if err != nil {
		return fmt.Errorf("write skeleton: %w", err)
	}
	if len(created) == 0 {
		// All files already matched — nothing to commit. Not an error
		// but worth calling out so the user knows nothing shipped.
		fmt.Fprintln(opts.Out, "Skeleton already matches — nothing to commit.")
		keepOnErr = false
		return nil
	}

	// Stage + commit.
	commitMsg := opts.CommitMessage
	if commitMsg == "" {
		commitMsg = fmt.Sprintf("Scaffold agent %s", opts.Name)
	}
	sha, err := commitSkeleton(repo, created, commitMsg)
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

	fmt.Fprintln(opts.Out, "")
	fmt.Fprintln(opts.Out, "Next steps:")
	fmt.Fprintf(opts.Out, "  ww agent create %s\n", opts.Name)
	fmt.Fprintf(opts.Out, "  ww agent send %s \"hello\"\n", opts.Name)
	if !opts.Force {
		fmt.Fprintf(opts.Out,
			"\nWhen `ww agent git add` ships (Phase 2), wire the deployed agent to this repo:\n"+
				"  ww agent git add %s --repo %s\n", opts.Name, ref.Display,
		)
	}
	keepOnErr = false
	return nil
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

// refuseOnExisting returns a usage-level error when `.agents/<group?>/<name>/`
// already has any content on the cloned ref. Matches the Force
// discipline: re-scaffolding on top of real work is rarely meant.
func refuseOnExisting(dir, name, group string) error {
	root := agentRepoRoot(name, group)
	fullPath := filepath.Join(dir, root)
	entries, err := os.ReadDir(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("check existing %s: %w", root, err)
	}
	if len(entries) == 0 {
		return nil
	}
	return fmt.Errorf(
		"%q already has %d entries on the remote. Pass --force to overwrite, "+
			"or pick a different agent name", root, len(entries),
	)
}

// writeSkeleton materialises the skeleton files on disk. Returns the
// list of paths actually written (skipped files are those whose exact
// content already matches on disk — a tiny optimisation so re-runs
// with --force don't produce empty commits).
func writeSkeleton(root string, files []skeletonFile) ([]string, error) {
	var written []string
	for _, f := range files {
		full := filepath.Join(root, filepath.FromSlash(f.Path))
		if existing, err := os.ReadFile(full); err == nil && string(existing) == f.Content {
			continue // already matches — skip
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(full, []byte(f.Content), 0o644); err != nil {
			return nil, err
		}
		written = append(written, f.Path)
	}
	sort.Strings(written)
	return written, nil
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
