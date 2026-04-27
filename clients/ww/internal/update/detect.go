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
// Detection order: curl-installer marker (most specific — explicit
// per-binary metadata) → Homebrew prefix → Go install location → Binary
// fallback. The curl marker wins over directory heuristics because a
// user might `--prefix=/usr/local` via the script, which would
// otherwise look indistinguishable from a hand-extracted tarball
// dropped into /usr/local/bin.
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
	// EvalSymlinks so /opt/homebrew/bin/ww (symlink) resolves to
	// /opt/homebrew/Cellar/ww/<version>/bin/ww. We want to classify
	// based on the real location, not the symlink, because users may
	// have their own symlinks for the Go install path too.
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	exe = filepath.Clean(exe)

	// Curl-installer marker: a sibling .ww.install-info file with
	// `installer=curl`. Checked before any directory heuristic — the
	// marker is explicit metadata, every other branch is inference.
	if data, err := readfile(filepath.Join(filepath.Dir(exe), markerFilename)); err == nil {
		if markerInstaller(data) == "curl" {
			return InstallMethodCurl
		}
	}

	// Brew prefixes — ordered most-specific-first so /opt/homebrew/bin
	// doesn't match before /opt/homebrew/Cellar. Both pre- and
	// post-symlink-eval paths are checked because EvalSymlinks can fail
	// on some filesystems.
	brewPrefixes := []string{
		"/opt/homebrew/Cellar/",
		"/opt/homebrew/bin/",
		"/usr/local/Cellar/",
		"/usr/local/bin/",
		filepath.Join(getenv("HOME"), ".linuxbrew/Cellar") + string(filepath.Separator),
		filepath.Join(getenv("HOME"), ".linuxbrew/bin") + string(filepath.Separator),
		"/home/linuxbrew/.linuxbrew/Cellar/",
		"/home/linuxbrew/.linuxbrew/bin/",
	}
	for _, p := range brewPrefixes {
		if p == "" || p == string(filepath.Separator) {
			continue
		}
		if strings.HasPrefix(exe, p) {
			return InstallMethodBrew
		}
	}

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
