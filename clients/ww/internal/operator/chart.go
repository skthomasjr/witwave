package operator

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
)

// EmbeddedChart ships a copy of charts/witwave-operator embedded into
// the `ww` binary so `ww operator install` works without the user
// configuring a Helm repository. The tree is kept in sync with the
// canonical charts/witwave-operator via scripts/sync-embedded-chart.sh
// (invoked from goreleaser pre-hook and by developers via `make sync`);
// CI has a drift check so the two copies can never diverge silently.
//
//go:embed all:embedded/witwave-operator
var embeddedChart embed.FS

// embeddedChartRoot is the prefix stripped from each embedded path when
// building the in-memory chart layout Helm's loader expects.
const embeddedChartRoot = "embedded/witwave-operator"

// LoadEmbeddedChart hydrates the embedded chart tree into a
// *chart.Chart that the Helm SDK's action.Install / action.Upgrade
// can consume. Returns a descriptive error if the embed is missing or
// the chart is malformed (which implies someone deleted the sync target).
func LoadEmbeddedChart() (*chart.Chart, error) {
	files, err := collectEmbeddedFiles()
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf(
			"embedded chart tree is empty — the build forgot to run " +
				"scripts/sync-embedded-chart.sh before compiling ww",
		)
	}
	return loader.LoadFiles(files)
}

// EmbeddedChartVersion returns the chart's declared version without
// fully constructing the chart object. Useful for version banners and
// skew detection.
func EmbeddedChartVersion() (string, error) {
	ch, err := LoadEmbeddedChart()
	if err != nil {
		return "", err
	}
	if ch.Metadata == nil {
		return "", fmt.Errorf("embedded chart has no metadata")
	}
	return ch.Metadata.Version, nil
}

// collectEmbeddedFiles walks the go:embed FS and builds the
// []*loader.BufferedFile Helm's loader expects. Paths are normalised
// so the loader sees "Chart.yaml" / "templates/foo.yaml" rather than
// the embed prefix.
func collectEmbeddedFiles() ([]*loader.BufferedFile, error) {
	var files []*loader.BufferedFile
	err := fs.WalkDir(embeddedChart, embeddedChartRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := embeddedChart.ReadFile(p)
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", p, err)
		}
		// embed.FS always uses forward slashes (documented io/fs
		// contract) regardless of host OS, so filepath.Rel — which is
		// OS-aware and uses \ on Windows — produces bogus paths.
		// strings.TrimPrefix on the embed's canonical "/"-separated
		// path is the correct stripper.
		rel := strings.TrimPrefix(p, embeddedChartRoot+"/")
		if rel == p {
			// p was the root itself (handled by d.IsDir above) or
			// lived outside the embed prefix — ignore.
			return nil
		}
		// Skip editor turd files the sync might accidentally pick up.
		// Dot-prefixed files are generally fine to drop (.DS_Store,
		// .swp, .idea/...) EXCEPT Helm's .helmignore, which the SDK
		// reads at render time — omitting it causes spurious files
		// (e.g. *.bak, OWNERS) to ship when the chart author relied on
		// it for filtering.
		base := path.Base(rel)
		if strings.HasSuffix(rel, "~") {
			return nil
		}
		if strings.HasPrefix(base, ".") && base != ".helmignore" {
			return nil
		}
		files = append(files, &loader.BufferedFile{Name: rel, Data: data})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk embedded chart: %w", err)
	}
	return files, nil
}
