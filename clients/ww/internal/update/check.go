package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Channel controls whether prereleases (e.g. `v0.4.0-beta.5`) count as
// candidates in the "is there a newer version" check.
type Channel string

const (
	// ChannelStable queries `GET /releases/latest`, which the GitHub
	// API defines as the most recent non-prerelease release. Users on
	// a prerelease still get notified when a subsequent stable tag
	// ships, because per semver `v0.4.0 > v0.4.0-beta.N`.
	ChannelStable Channel = "stable"

	// ChannelBeta queries `GET /releases` (list, newest first) and
	// takes the first entry — prereleases included. Use this to track
	// the bleeding edge of the `-beta.N` / `-rc.N` train.
	ChannelBeta Channel = "beta"
)

// DefaultChannel is applied when no config or env var sets one.
const DefaultChannel = ChannelStable

// ParseChannel accepts "stable"/"beta" case-insensitively. Empty string
// returns DefaultChannel. Any other value is an error so a typo in
// config.toml surfaces immediately.
func ParseChannel(s string) (Channel, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return DefaultChannel, nil
	case "stable":
		return ChannelStable, nil
	case "beta":
		return ChannelBeta, nil
	default:
		return "", fmt.Errorf("update: unknown channel %q (expected stable/beta)", s)
	}
}

// Release is the subset of the GitHub Releases API response we care
// about. Extra fields are tolerated by json.Decode so future schema
// additions don't break older ww versions.
type Release struct {
	TagName    string `json:"tag_name"`
	HTMLURL    string `json:"html_url"`
	Prerelease bool   `json:"prerelease"`
	Draft      bool   `json:"draft"`
}

// Cache is the on-disk shape of the version-check cache. A tiny file
// keyed by channel — switching channels retains stale results until the
// next interval lapse for the OLD channel, which is harmless because
// the new channel's first lookup runs a fresh API call.
type Cache struct {
	// Channel this cache entry describes. Lets us reuse the same file
	// for stable vs beta without races.
	Channel Channel `json:"channel"`
	// CheckedAt is when we last successfully fetched. Age >= interval
	// means the cache is stale and must be refreshed.
	CheckedAt time.Time `json:"checked_at"`
	// LatestTag and LatestURL are the API's answer at CheckedAt time.
	LatestTag string `json:"latest_tag"`
	LatestURL string `json:"latest_url"`
}

// Checker encapsulates the configured sources + targets for a version
// check so it can be called with minimal wiring from the cobra hook.
// All fields are safe to share across goroutines — a Checker is
// essentially immutable once constructed.
type Checker struct {
	// CurrentVersion is the tag of this running binary (as ldflag-ed
	// into cmd.Version). Leading "v" is tolerated.
	CurrentVersion string

	// Channel selects stable-only vs include-prerelease candidates.
	Channel Channel

	// Interval is the minimum time between live API calls. Results
	// inside this window are served from the on-disk cache.
	Interval time.Duration

	// Owner + Repo identify the GitHub repository hosting releases.
	// Parameterized so tests can point at httptest fixtures.
	Owner string
	Repo  string

	// APIBase overrides the GitHub API base URL (default:
	// https://api.github.com). Tests set this to an httptest.Server
	// URL; production callers leave it empty.
	APIBase string

	// HTTPClient is the transport. Must have a finite timeout — the
	// check is best-effort and must not keep the user's command
	// waiting. The default Checker is constructed with a 2-second
	// timeout.
	HTTPClient *http.Client

	// CachePath is the absolute path of the cache file. Empty means
	// "derive from XDG_CACHE_HOME or the platform default user-cache
	// dir."
	CachePath string
}

// NewChecker builds a Checker with production-safe defaults.
func NewChecker(currentVersion string, channel Channel, interval time.Duration) *Checker {
	if interval <= 0 {
		interval = DefaultInterval
	}
	return &Checker{
		CurrentVersion: currentVersion,
		Channel:        channel,
		Interval:       interval,
		Owner:          "skthomasjr",
		Repo:           "witwave",
		HTTPClient: &http.Client{
			// Short — better to report "check timed out" silently than
			// to make the user wait for a slow network on every run.
			Timeout: 2 * time.Second,
		},
	}
}

// Notice describes an available upgrade. nil means "no upgrade
// available" or "check failed silently."
type Notice struct {
	CurrentVersion string
	LatestTag      string
	LatestURL      string
	Channel        Channel
}

// Check returns a Notice iff a newer release exists on the configured
// channel. It NEVER returns an error — failures (network timeout, JSON
// parse error, cache write error) are logged nowhere and swallowed,
// because a version check must never interfere with the caller's
// actual command. The ctx is honored for cancellation and timeout
// propagation.
//
// The cache is checked first; if it is fresh (age < Interval), the
// cached tag is compared against CurrentVersion without any network
// call. When the cache is stale or missing, a single API call is
// made and the result is written back.
func (c *Checker) Check(ctx context.Context) *Notice {
	cachePath := c.resolveCachePath()

	// Cache hit — fresh AND matching the configured channel AND the
	// cached "latest" is actually ahead of our running version. When
	// the cached latest matches our version (user is up to date), we
	// intentionally bypass the cache and re-fetch on the next call so
	// a freshly-cut release is visible within one command, not up to
	// `Interval` later. The cost is one extra API call per run for
	// users who stay current, which is negligible (the call is ~500ms
	// and the GitHub API quota is 60/hr).
	if cached, ok := readCache(cachePath); ok {
		if cached.Channel == c.Channel && time.Since(cached.CheckedAt) < c.Interval {
			if cmp, ok := compareSemver(c.CurrentVersion, cached.LatestTag); ok && cmp < 0 {
				// Cached answer is strictly "newer than us" — use it.
				return c.buildNotice(cached.LatestTag, cached.LatestURL)
			}
			// Cache says "you're on the latest" (or tag is newer-but-
			// unparseable). Fall through and re-fetch so a freshly
			// cut release is detected promptly.
		}
	}

	// Fetch live.
	rel, err := c.fetchLatest(ctx)
	if err != nil {
		// Silent failure — return nil so the caller neither prints a
		// banner nor surfaces the error.
		return nil
	}

	// Persist whatever we got, even if no upgrade is available. The
	// cache's CheckedAt still rate-limits older-binary polling; the
	// "current == latest" case above just reads and bypasses it.
	_ = writeCache(cachePath, Cache{
		Channel:   c.Channel,
		CheckedAt: time.Now().UTC(),
		LatestTag: rel.TagName,
		LatestURL: rel.HTMLURL,
	})

	return c.buildNotice(rel.TagName, rel.HTMLURL)
}

// buildNotice returns a *Notice iff latest is semver-newer than the
// current version. Handles the "unknown current version" case (the
// `dev` fallback assigned when ldflags aren't applied) by refusing to
// notify — no point telling a locally-built dev binary about a release
// tag.
func (c *Checker) buildNotice(latestTag, latestURL string) *Notice {
	if c.CurrentVersion == "" || c.CurrentVersion == "dev" {
		return nil
	}
	if latestTag == "" {
		return nil
	}
	cmp, ok := compareSemver(c.CurrentVersion, latestTag)
	if !ok {
		return nil
	}
	if cmp >= 0 {
		return nil // current is >= latest; nothing to notify
	}
	return &Notice{
		CurrentVersion: c.CurrentVersion,
		LatestTag:      latestTag,
		LatestURL:      latestURL,
		Channel:        c.Channel,
	}
}

// fetchLatest performs the GitHub API request. For ChannelStable it
// uses /releases/latest which auto-skips prereleases. For ChannelBeta
// it uses /releases (list) and picks the first non-draft entry.
func (c *Checker) fetchLatest(ctx context.Context) (*Release, error) {
	base := c.APIBase
	if base == "" {
		base = "https://api.github.com"
	}

	var url string
	switch c.Channel {
	case ChannelStable:
		url = fmt.Sprintf("%s/repos/%s/%s/releases/latest", base, c.Owner, c.Repo)
	case ChannelBeta:
		// Ask for a short page — we only care about the newest one.
		url = fmt.Sprintf("%s/repos/%s/%s/releases?per_page=5", base, c.Owner, c.Repo)
	default:
		return nil, fmt.Errorf("unsupported channel %q", c.Channel)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	// Recommended by GitHub's API docs for rate-limiting and schema
	// stability.
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "ww-update-check")

	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Handle common non-success responses explicitly for clarity — any
	// non-200 becomes a silent error upstream.
	if resp.StatusCode == http.StatusNotFound {
		return nil, errors.New("no releases found")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github api returned %d", resp.StatusCode)
	}

	// Cap the body size — a pathological response (MITM, misconfigured
	// proxy) shouldn't let us allocate unbounded memory on what's
	// meant to be a courtesy check.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20 /* 1 MiB */))
	if err != nil {
		return nil, err
	}

	switch c.Channel {
	case ChannelStable:
		var rel Release
		if err := json.Unmarshal(body, &rel); err != nil {
			return nil, err
		}
		if rel.Draft || rel.TagName == "" {
			return nil, errors.New("stable latest returned draft or empty tag")
		}
		return &rel, nil
	case ChannelBeta:
		var list []Release
		if err := json.Unmarshal(body, &list); err != nil {
			return nil, err
		}
		for _, rel := range list {
			if rel.Draft || rel.TagName == "" {
				continue
			}
			return &rel, nil // newest non-draft, prerelease or not
		}
		return nil, errors.New("no non-draft release found")
	}
	return nil, fmt.Errorf("unsupported channel %q", c.Channel)
}

// resolveCachePath returns an absolute path to the cache file, creating
// the parent directory as needed. Empty return value means "caching
// disabled for this invocation" — the caller gracefully falls back to
// always-fetch behavior, which a short interval would have done anyway.
func (c *Checker) resolveCachePath() string {
	if c.CachePath != "" {
		return c.CachePath
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil || cacheDir == "" {
		return ""
	}
	dir := filepath.Join(cacheDir, "ww")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ""
	}
	return filepath.Join(dir, "update-check.json")
}

// readCache is nil-safe on read errors — returns ok=false so the caller
// falls through to a live fetch.
func readCache(path string) (Cache, bool) {
	if path == "" {
		return Cache{}, false
	}
	f, err := os.Open(path)
	if err != nil {
		return Cache{}, false
	}
	defer f.Close()

	var c Cache
	if err := json.NewDecoder(io.LimitReader(f, 64*1024)).Decode(&c); err != nil {
		return Cache{}, false
	}
	return c, true
}

// writeCache persists the cache entry. Errors are returned but callers
// treat them as non-fatal — the cache is a performance feature, not a
// correctness requirement.
func writeCache(path string, c Cache) error {
	if path == "" {
		return errors.New("no cache path")
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".update-check-*.json")
	if err != nil {
		return err
	}
	// Best-effort cleanup on any exit path. Rename on success supersedes
	// the temp file so the Remove below is a no-op when everything works.
	defer func() {
		_ = os.Remove(f.Name())
	}()
	if err := json.NewEncoder(f).Encode(c); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// --- semver comparison -----------------------------------------------

// semver parses a subset of semver sufficient to compare release tags
// produced by this project's goreleaser pipeline. Specifically: the
// major.minor.patch triple plus an optional prerelease segment. Build
// metadata ("+build") is stripped and ignored per SemVer 2.0.
//
// Tag inputs tolerate a leading "v" (e.g. "v0.4.0-beta.5"). The
// returned ok=false for any input we can't parse so the caller
// conservatively treats it as "no comparable version available" and
// skips the notification.
var semverRe = regexp.MustCompile(
	`^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]+)?$`,
)

type parsedSemver struct {
	major      int
	minor      int
	patch      int
	prerelease string // "" means "not a prerelease" (ranks higher than any prerelease)
}

func parseSemver(s string) (parsedSemver, bool) {
	m := semverRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return parsedSemver{}, false
	}
	mj, err1 := strconv.Atoi(m[1])
	mn, err2 := strconv.Atoi(m[2])
	pt, err3 := strconv.Atoi(m[3])
	if err1 != nil || err2 != nil || err3 != nil {
		return parsedSemver{}, false
	}
	return parsedSemver{
		major:      mj,
		minor:      mn,
		patch:      pt,
		prerelease: m[4],
	}, true
}

// compareSemver returns -1 if a<b, 0 if a==b, +1 if a>b. ok=false when
// either side is unparseable.
//
// SemVer prerelease precedence: "1.0.0-beta < 1.0.0". Within prerelease
// identifiers, dot-separated fields are compared left-to-right: numeric
// fields as integers, alphanumeric as ASCII.
func compareSemver(a, b string) (int, bool) {
	pa, okA := parseSemver(a)
	pb, okB := parseSemver(b)
	if !okA || !okB {
		return 0, false
	}
	if c := cmpInt(pa.major, pb.major); c != 0 {
		return c, true
	}
	if c := cmpInt(pa.minor, pb.minor); c != 0 {
		return c, true
	}
	if c := cmpInt(pa.patch, pb.patch); c != 0 {
		return c, true
	}
	// Prerelease precedence: empty > non-empty (a release tag beats a
	// prerelease of the same M.m.p).
	switch {
	case pa.prerelease == "" && pb.prerelease == "":
		return 0, true
	case pa.prerelease == "":
		return 1, true
	case pb.prerelease == "":
		return -1, true
	}
	return comparePrerelease(pa.prerelease, pb.prerelease), true
}

func comparePrerelease(a, b string) int {
	af := strings.Split(a, ".")
	bf := strings.Split(b, ".")
	for i := 0; i < len(af) && i < len(bf); i++ {
		ai, aIsNum := toInt(af[i])
		bi, bIsNum := toInt(bf[i])
		switch {
		case aIsNum && bIsNum:
			if c := cmpInt(ai, bi); c != 0 {
				return c
			}
		case aIsNum:
			// numeric < alphanumeric per SemVer
			return -1
		case bIsNum:
			return 1
		default:
			if c := strings.Compare(af[i], bf[i]); c != 0 {
				return c
			}
		}
	}
	return cmpInt(len(af), len(bf))
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func toInt(s string) (int, bool) {
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}
