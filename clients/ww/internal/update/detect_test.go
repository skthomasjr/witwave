package update

import "testing"

func TestDetectInstallMethod(t *testing.T) {
	cases := []struct {
		name  string
		exe   string
		env   map[string]string
		want  InstallMethod
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
			name: "homebrew intel cellar",
			exe:  "/usr/local/Cellar/ww/0.4.0-beta.5/bin/ww",
			want: InstallMethodBrew,
		},
		{
			name: "linuxbrew default location",
			exe:  "/home/linuxbrew/.linuxbrew/Cellar/ww/0.4.0-beta.5/bin/ww",
			want: InstallMethodBrew,
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
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			getexec := func() (string, error) { return tc.exe, nil }
			getenv := func(k string) string { return tc.env[k] }
			got := DetectInstallMethod(getexec, getenv)
			if got != tc.want {
				t.Errorf("DetectInstallMethod(exe=%q, env=%v) = %v, want %v",
					tc.exe, tc.env, got, tc.want)
			}
		})
	}
}

func TestDetectInstallMethod_GetexecError(t *testing.T) {
	// When os.Executable() fails, we should fall back to Binary rather
	// than panicking or suggesting a wrong upgrade command.
	getexec := func() (string, error) { return "", errDummy }
	got := DetectInstallMethod(getexec, func(string) string { return "" })
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
		{InstallMethodGoInstall, "go install github.com/skthomasjr/witwave/clients/ww@latest"},
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
