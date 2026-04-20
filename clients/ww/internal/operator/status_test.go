package operator

import "testing"

func TestSkewLabel(t *testing.T) {
	cases := []struct {
		name       string
		wwVersion  string
		appVersion string
		want       string
	}{
		{"no release", "0.4.4", "", ""},
		{"dev build", "dev", "0.4.4", "(local build — skew unknown)"},
		{"empty ww version", "", "0.4.4", "(local build — skew unknown)"},
		{"exact match", "0.4.4", "0.4.4", "(match)"},
		{"exact match with v prefix", "v0.4.4", "v0.4.4", "(match)"},
		{"patch skew", "0.4.4", "0.4.3", "(patch skew)"},
		{"patch skew reverse", "0.4.3", "0.4.4", "(patch skew)"},
		{"minor skew", "0.4.4", "0.5.0", "(minor skew)"},
		{"major skew", "0.4.4", "1.0.0", "(major skew — upgrade blocked)"},
		{"non-semver equal", "experimental", "experimental", "(match)"},
		{"non-semver unequal", "experimental", "0.4.4", "(skew — non-semver version)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var rel *ReleaseInfo
			if c.appVersion != "" {
				rel = &ReleaseInfo{AppVersion: c.appVersion}
			}
			got := skewLabel(c.wwVersion, rel)
			if got != c.want {
				t.Errorf("skewLabel(%q, %+v) = %q; want %q", c.wwVersion, rel, got, c.want)
			}
		})
	}
}
