package update

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectInstallMethod(t *testing.T) {
	cases := []struct {
		name   string
		exe    string
		env    map[string]string
		marker string // contents of <bindir>/.ww.install-info, "" = file absent
		want   InstallMethod
	}{
		{
			name: "homebrew apple silicon cellar",
			exe:  "/opt/homebrew/Cellar/ww/0.4.0-beta.5/bin/ww",
			want: InstallMethodBrew,
		},
		{
			name: "homebrew apple silicon bin symlink",
			exe:  "/opt/homebrew/bin/ww",
			want: InstallMethodBrew,
		},
		{
			name: "homebrew apple silicon caskroom",
			exe:  "/opt/homebrew/Caskroom/ww/0.18.0/ww",
			want: InstallMethodBrew,
		},
		{
			name: "homebrew intel cellar",
			exe:  "/usr/local/Cellar/ww/0.4.0-beta.5/bin/ww",
			want: InstallMethodBrew,
		},
		{
			name: "homebrew intel caskroom",
			exe:  "/usr/local/Caskroom/ww/0.18.0/ww",
			want: InstallMethodBrew,
		},
		{
			name: "linuxbrew default location",
			exe:  "/home/linuxbrew/.linuxbrew/Cellar/ww/0.4.0-beta.5/bin/ww",
			want: InstallMethodBrew,
		},
		{
			name: "linuxbrew caskroom",
			exe:  "/home/linuxbrew/.linuxbrew/Caskroom/ww/0.18.0/ww",
			want: InstallMethodBrew,
		},
		{
			name:   "stale curl marker inside brew caskroom is overridden",
			exe:    "/opt/homebrew/Caskroom/ww/0.18.0/ww",
			marker: "installer=curl\nversion=v0.19.0\nchannel=stable\n",
			want:   InstallMethodBrew, // brew prefix wins over the marker
		},
		{
			name: "go install with default GOPATH",
			exe:  "/home/alice/go/bin/ww",
			env:  map[string]string{"HOME": "/home/alice"},
			want: InstallMethodGoInstall,
		},
		{
			name: "go install with GOBIN set",
			exe:  "/home/alice/bin/ww",
			env:  map[string]string{"GOBIN": "/home/alice/bin"},
			want: InstallMethodGoInstall,
		},
		{
			name: "go install with custom GOPATH",
			exe:  "/opt/go-workspace/bin/ww",
			env:  map[string]string{"GOPATH": "/opt/go-workspace"},
			want: InstallMethodGoInstall,
		},
		{
			name: "arbitrary location falls back to binary",
			exe:  "/opt/my-custom-tools/ww",
			want: InstallMethodBinary,
		},
		{
			name: "tarball extracted to $HOME/bin",
			exe:  "/home/alice/bin/ww",
			env:  map[string]string{"HOME": "/home/alice"},
			want: InstallMethodBinary, // not /home/alice/go/bin/
		},
		{
			name:   "curl installer marker in /usr/local/bin",
			exe:    "/usr/local/bin/ww",
			marker: "installer=curl\nversion=v0.5.0\nchannel=stable\n",
			want:   InstallMethodCurl,
		},
		{
			name:   "curl installer marker in $HOME/.local/bin",
			exe:    "/home/alice/.local/bin/ww",
			env:    map[string]string{"HOME": "/home/alice"},
			marker: "installer=curl\nversion=v0.5.0-beta.3\nchannel=beta\n",
			want:   InstallMethodCurl,
		},
		{
			name:   "marker without installer=curl falls through to dir heuristics",
			exe:    "/usr/local/bin/ww",
			marker: "installer=tarball\nversion=v0.5.0\n",
			want:   InstallMethodBrew, // /usr/local/bin matches the brew prefix
		},
		{
			name:   "marker beats brew prefix when installer=curl",
			exe:    "/usr/local/bin/ww",
			marker: "# installed by curl|sh\ninstaller=curl\n",
			want:   InstallMethodCurl,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			getexec := func() (string, error) { return tc.exe, nil }
			getenv := func(k string) string { return tc.env[k] }
			expectedMarkerPath := ""
			if tc.exe != "" {
				expectedMarkerPath = filepath.Join(filepath.Dir(tc.exe), ".ww.install-info")
			}
			readfile := func(p string) ([]byte, error) {
				if p == expectedMarkerPath && tc.marker != "" {
					return []byte(tc.marker), nil
				}
				return nil, os.ErrNotExist
			}
			got := DetectInstallMethod(getexec, getenv, readfile)
			if got != tc.want {
				t.Errorf("DetectInstallMethod(exe=%q, env=%v, marker=%q) = %v, want %v",
					tc.exe, tc.env, tc.marker, got, tc.want)
			}
		})
	}
}

func TestDetectInstallMethod_GetexecError(t *testing.T) {
	// When os.Executable() fails, we should fall back to Binary rather
	// than panicking or suggesting a wrong upgrade command.
	getexec := func() (string, error) { return "", errDummy }
	got := DetectInstallMethod(getexec, func(string) string { return "" }, nil)
	if got != InstallMethodBinary {
		t.Errorf("getexec err: got %v, want %v", got, InstallMethodBinary)
	}
}

func TestInstallMethod_UpgradeCommand(t *testing.T) {
	cases := []struct {
		m    InstallMethod
		want string
	}{
		{InstallMethodBrew, "brew upgrade ww"},
		{InstallMethodGoInstall, "go install github.com/witwave-ai/witwave/clients/ww@latest"},
		{InstallMethodCurl, "curl -fsSL https://github.com/witwave-ai/witwave/releases/latest/download/install.sh | sh"},
		{InstallMethodBinary, ""},
	}
	for _, tc := range cases {
		if got := tc.m.UpgradeCommand(); got != tc.want {
			t.Errorf("%v.UpgradeCommand() = %q, want %q", tc.m, got, tc.want)
		}
	}
}

// errDummy stands in for any os.Executable() error shape; the update
// package never inspects the error's structure beyond "did it happen".
type dummyErr struct{}

func (dummyErr) Error() string { return "dummy" }

var errDummy = dummyErr{}
