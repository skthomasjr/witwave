// Tests for the pure-helper functions in helm.go that the
// `ww operator install/upgrade/uninstall` success banners compose
// over a `*release.Release` returned by the Helm SDK. Mirrors the
// table-driven shape used in chart_test.go and status_test.go; the
// Install / Upgrade / Uninstall paths are exercised against a real
// cluster, so this file just pins the defensive nil-walk contract
// in helmReleaseStatus so the #1550 panic-on-missing-Info regression
// can't sneak back in.
package operator

import (
	"testing"

	"helm.sh/helm/v3/pkg/release"
)

// TestHelmReleaseStatus pins the nil-defensive status accessor used
// by the install / upgrade success banner renderers. Contract:
//   - nil *release.Release                   → "(unknown)"
//   - non-nil *release.Release, Info == nil  → "(unknown)" (#1550)
//   - non-nil release with non-nil Info      → string(rel.Info.Status)
//
// The "(unknown)" sentinel is the load-bearing piece: the Helm SDK
// has shipped versions where Install returned a release whose Info
// pointer was nil on early-failure paths, and the prior naive
// `rel.Info.Status` dereference turned a successful (or recoverable-
// failure) install into a panic-level exit (#1550). Pin every
// nil shape so a future refactor that re-introduces the unsafe
// dereference fails this test instead of users.
func TestHelmReleaseStatus(t *testing.T) {
	cases := []struct {
		name string
		rel  *release.Release
		want string
	}{
		{"nil release returns sentinel", nil, "(unknown)"},
		{
			"non-nil release, nil Info returns sentinel (#1550)",
			&release.Release{Name: "witwave-operator"},
			"(unknown)",
		},
		{
			"deployed status returned verbatim",
			&release.Release{Info: &release.Info{Status: release.StatusDeployed}},
			"deployed",
		},
		{
			"failed status returned verbatim",
			&release.Release{Info: &release.Info{Status: release.StatusFailed}},
			"failed",
		},
		{
			"pending-install status returned verbatim",
			&release.Release{Info: &release.Info{Status: release.StatusPendingInstall}},
			"pending-install",
		},
		{
			"uninstalled status returned verbatim",
			&release.Release{Info: &release.Info{Status: release.StatusUninstalled}},
			"uninstalled",
		},
		{
			"zero-valued Info.Status renders empty (Helm's own default-zero behaviour)",
			&release.Release{Info: &release.Info{}},
			"",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := helmReleaseStatus(tc.rel)
			if got != tc.want {
				t.Errorf("helmReleaseStatus(%+v) = %q, want %q", tc.rel, got, tc.want)
			}
		})
	}
}
