package update

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
)

// markerFilename is the sibling file the curl installer
// (scripts/install.sh) drops next to the ww binary so we can
// distinguish a curl-installed binary from a hand-extracted tarball.
// Schema is simple key=value lines, currently:
//
//	installer=curl
//	version=v0.5.0
//	channel=stable|beta
//	install_url=<canonical install.sh URL>
//	installed_at=<RFC3339>
//
// Only `installer=curl` is currently consumed by detection — the rest
// is forward-compatibility metadata.
const markerFilename = ".ww.install-info"

// curlInstallURL is the canonical install-script URL surfaced as the
// upgrade hint for InstallMethodCurl. Mirrors the value
// scripts/install.sh writes into the marker file's `install_url=` line;
// kept in sync with that script by inspection (small surface, no churn).
const curlInstallURL = "https://github.com/witwave-ai/witwave/releases/latest/download/install.sh"

// InstallMethod is how ww was dropped on the user's PATH. Detected by
// resolving os.Executable() to its absolute path and matching the path
// shape against known conventions. Best-effort — a user can always
// bypass the detection by moving a binary somewhere arbitrary; we fall
// back to Binary in that case.
type InstallMethod int

const (
	// InstallMethodBinary is the fallback. Either a standalone download
	// dropped into a custom location, a tarball extracted manually, or
	// a package manager we don't recognize. Upgrade instruction points
	// at the GitHub Releases page and lets the user do whatever they
	// did last time.
	InstallMethodBinary InstallMethod = iota

	// InstallMethodBrew means the binary lives under a Homebrew prefix.
	// macOS Apple Silicon: /opt/homebrew/{Cellar,bin}. macOS Intel and
	// most Linuxbrew installs: /usr/local/{Cellar,bin}. Custom-prefix
	// Linuxbrew at $HOME/.linuxbrew/{Cellar,bin} is also recognized.
	InstallMethodBrew

	// InstallMethodGoInstall means the binary lives under a Go bin
	// directory, i.e. the output of `go install`. Recognized shapes:
	// $GOPATH/bin, $HOME/go/bin, $GOBIN explicitly set. Distinguishing
	// this matters because the upgrade command is different —
	// `go install @latest` rather than `brew upgrade`.
	InstallMethodGoInstall

	// InstallMethodCurl means the binary was placed by the curl
	// installer at scripts/install.sh, identified by a sibling
	// `.ww.install-info` file with `installer=curl`. The matching
	// upgrade path re-runs the same install pipeline; the script is
	// idempotent so this is safe.
	InstallMethodCurl
)

// String satisfies fmt.Stringer so we can log the detection result.
func (m InstallMethod) String() string {
	switch m {
	case InstallMethodBrew:
		return "homebrew"
	case InstallMethodGoInstall:
		return "go-install"
	case InstallMethodCurl:
		return "curl-installer"
	default:
		return "binary"
	}
}

// UpgradeCommand returns the shell command that upgrades a binary
// installed via this method to the latest release. "" means "no
// automatic upgrade path — tell the user to download manually."
func (m InstallMethod) UpgradeCommand() string {
	switch m {
	case InstallMethodBrew:
		return "brew upgrade ww"
	case InstallMethodGoInstall:
		return "go install github.com/witwave-ai/witwave/clients/ww@latest"
	case InstallMethodCurl:
		return "curl -fsSL " + curlInstallURL + " | sh"
	default:
		return ""
	}
}

// DetectInstallMethod inspects os.Executable() and classifies the
// binary's provenance. The getexec + getenv + readfile seams let tests
// exercise every branch without relocating real binaries. Any error
// resolving the executable path produces InstallMethodBinary — the
// fallback path is always "tell the user to download from the releases
// page."
//
// Detection order: Homebrew prefix (matched against BOTH the symlink
// path and the resolved real path) → curl-installer marker → Go install
// location → Binary fallback. Brew wins over the marker because a prior
// buggy `ww update` may have dropped a `.ww.install-info` file inside a
// Caskroom or Cellar directory; the brew-managed location is the
// authoritative classification, and the marker in that case is a stale
// remnant we should ignore on subsequent runs so `brew upgrade ww` is
// what runs.
func DetectInstallMethod(
	getexec func() (string, error),
	getenv func(string) string,
	readfile func(string) ([]byte, error),
) InstallMethod {
	if getexec == nil {
		getexec = os.Executable
	}
	if getenv == nil {
		getenv = os.Getenv
	}
	if readfile == nil {
		readfile = os.ReadFile
	}
	exe, err := getexec()
	if err != nil {
		return InstallMethodBinary
	}
	exe = filepath.Clean(exe)
	// EvalSymlinks so /opt/homebrew/bin/ww (symlink) resolves to
	// /opt/homebrew/Caskroom/ww/<version>/ww (cask) or
	// /opt/homebrew/Cellar/ww/<version>/bin/ww (formula). We want to
	// classify based on the real location, not the symlink, because
	// users may have their own symlinks for the Go install path too.
	// We keep BOTH paths around because either one matching a brew
	// prefix is sufficient — a broken Cellar/Caskroom symlink
	// shouldn't make us miss the obvious /opt/homebrew/bin/ entry.
	resolved := exe
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		resolved = filepath.Clean(real)
	}

	// Brew classification has two tiers:
	//
	//   1. UNAMBIGUOUS brew dirs — Caskroom and Cellar live under brew
	//      prefixes that nothing else writes into. A binary resolved
	//      into one of these is brew, full stop. Checked BEFORE the
	//      curl marker because a prior buggy `ww update` may have
	//      dropped `.ww.install-info` inside one of these dirs; the
	//      brew-managed location is authoritative and the stale marker
	//      should be ignored.
	//
	//   2. AMBIGUOUS bin dirs — /opt/homebrew/bin, /usr/local/bin, and
	//      $HOME/.linuxbrew/bin can hold either brew-managed symlinks
	//      OR install.sh-deposited binaries. Checked AFTER the curl
	//      marker so a real curl install (which drops the marker) is
	//      classified correctly. With no marker, default to brew —
	//      these dirs are most commonly populated by brew.
	unambiguousBrew := []string{
		"/opt/homebrew/Caskroom/",
		"/opt/homebrew/Cellar/",
		"/usr/local/Caskroom/",
		"/usr/local/Cellar/",
		filepath.Join(getenv("HOME"), ".linuxbrew/Caskroom") + string(filepath.Separator),
		filepath.Join(getenv("HOME"), ".linuxbrew/Cellar") + string(filepath.Separator),
		"/home/linuxbrew/.linuxbrew/Caskroom/",
		"/home/linuxbrew/.linuxbrew/Cellar/",
	}
	for _, p := range unambiguousBrew {
		if p == "" || p == string(filepath.Separator) {
			continue
		}
		if strings.HasPrefix(exe, p) || strings.HasPrefix(resolved, p) {
			return InstallMethodBrew
		}
	}

	// Curl-installer marker: a sibling .ww.install-info file with
	// `installer=curl`. Checked against the resolved path so a marker
	// next to the real binary is found even when invoked through a
	// symlink. Doesn't shadow Caskroom/Cellar (already handled above).
	if data, err := readfile(filepath.Join(filepath.Dir(resolved), markerFilename)); err == nil {
		if markerInstaller(data) == "curl" {
			return InstallMethodCurl
		}
	}

	// Ambiguous brew bin dirs. Hit only when the marker check above
	// didn't classify as curl — preserves the marker-wins-in-/usr/local/bin
	// behaviour for genuine curl installs while keeping the
	// brew-symlink-with-broken-Cellar case classified as brew.
	ambiguousBrew := []string{
		"/opt/homebrew/bin/",
		"/usr/local/bin/",
		filepath.Join(getenv("HOME"), ".linuxbrew/bin") + string(filepath.Separator),
		"/home/linuxbrew/.linuxbrew/bin/",
	}
	for _, p := range ambiguousBrew {
		if p == "" || p == string(filepath.Separator) {
			continue
		}
		if strings.HasPrefix(exe, p) || strings.HasPrefix(resolved, p) {
			return InstallMethodBrew
		}
	}

	// From here on we classify based on the resolved real path —
	// matches the original behaviour for the go-install heuristic.
	exe = resolved

	// `go install` locations. GOBIN wins if set; otherwise GOPATH/bin;
	// otherwise the default $HOME/go/bin.
	var goBins []string
	if gobin := getenv("GOBIN"); gobin != "" {
		goBins = append(goBins, filepath.Clean(gobin)+string(filepath.Separator))
	}
	if gopath := getenv("GOPATH"); gopath != "" {
		// GOPATH can be a colon-separated list — check each entry's bin.
		for _, entry := range strings.Split(gopath, string(os.PathListSeparator)) {
			if entry = strings.TrimSpace(entry); entry != "" {
				goBins = append(goBins, filepath.Join(entry, "bin")+string(filepath.Separator))
			}
		}
	}
	if home := getenv("HOME"); home != "" {
		goBins = append(goBins, filepath.Join(home, "go", "bin")+string(filepath.Separator))
	}
	for _, p := range goBins {
		if strings.HasPrefix(exe, p) {
			return InstallMethodGoInstall
		}
	}

	return InstallMethodBinary
}

// markerInstaller pulls the `installer=` value out of a marker file's
// raw bytes. The format is intentionally trivial — POSIX shell-friendly
// key=value lines, comments allowed via leading `#`. Returns "" when
// the key isn't present so callers can treat absence and an unknown
// value identically.
func markerInstaller(data []byte) string {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if v, ok := strings.CutPrefix(line, "installer="); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
