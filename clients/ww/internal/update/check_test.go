package update

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCompareSemver(t *testing.T) {
	cases := []struct {
		a, b string
		want int
		ok   bool
	}{
		{"v0.4.0", "v0.4.1", -1, true},
		{"v0.4.1", "v0.4.0", 1, true},
		{"v0.4.0", "v0.4.0", 0, true},
		{"0.4.0", "v0.4.0", 0, true}, // leading-v optional on either side
		{"v0.4.0-beta.5", "v0.4.0", -1, true},
		{"v0.4.0", "v0.4.0-beta.5", 1, true},
		{"v0.4.0-beta.5", "v0.4.0-beta.6", -1, true},
		{"v0.4.0-beta.10", "v0.4.0-beta.2", 1, true}, // numeric, not lexical
		{"v0.4.0-rc.1", "v0.4.0-beta.5", 1, true},    // alphabetic compare: rc > beta
		{"v0.4.0-beta.5", "v0.4.0-beta", 1, true},    // longer prerelease > shorter
		{"dev", "v0.4.0", 0, false},                  // unparseable current
		{"v0.4.0", "garbage", 0, false},              // unparseable latest
		{"v0.4.0+build1", "v0.4.0+build2", 0, true},  // build metadata ignored
		{"v1.0.0-alpha.1", "v1.0.0-alpha.beta", -1, true},
	}
	for _, tc := range cases {
		got, ok := compareSemver(tc.a, tc.b)
		if ok != tc.ok {
			t.Errorf("compareSemver(%q, %q) ok=%v, want %v", tc.a, tc.b, ok, tc.ok)
			continue
		}
		if tc.ok && got != tc.want {
			t.Errorf("compareSemver(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestChecker_Check_StableChannel_NewerAvailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/releases/latest") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(Release{
			TagName: "v0.5.0",
			HTMLURL: "https://github.com/witwave-ai/witwave/releases/tag/v0.5.0",
		})
	}))
	defer srv.Close()

	tmp := t.TempDir()
	c := &Checker{
		CurrentVersion: "v0.4.0",
		Channel:        ChannelStable,
		Interval:       DefaultInterval,
		Owner:          "witwave-ai",
		Repo:           "witwave",
		APIBase:        srv.URL,
		HTTPClient:     srv.Client(),
		CachePath:      filepath.Join(tmp, "cache.json"),
	}
	notice := c.Check(context.Background())
	if notice == nil {
		t.Fatal("expected non-nil notice for newer stable")
	}
	if notice.LatestTag != "v0.5.0" {
		t.Errorf("latest tag = %q, want v0.5.0", notice.LatestTag)
	}
	// Cache file must exist post-check.
	if _, err := os.Stat(c.CachePath); err != nil {
		t.Errorf("cache not written: %v", err)
	}
}

func TestChecker_Check_NoNewerReturnsNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(Release{
			TagName: "v0.4.0",
			HTMLURL: "https://example.com",
		})
	}))
	defer srv.Close()

	tmp := t.TempDir()
	c := &Checker{
		CurrentVersion: "v0.4.0",
		Channel:        ChannelStable,
		Interval:       DefaultInterval,
		APIBase:        srv.URL,
		HTTPClient:     srv.Client(),
		CachePath:      filepath.Join(tmp, "cache.json"),
	}
	if notice := c.Check(context.Background()); notice != nil {
		t.Errorf("expected nil notice for same version, got %+v", notice)
	}
}

func TestChecker_Check_BetaChannelIncludesPrereleases(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/releases") || strings.HasSuffix(r.URL.Path, "/latest") {
			t.Errorf("beta channel should hit list endpoint, got %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode([]Release{
			{TagName: "v0.5.0-beta.1", HTMLURL: "https://example.com", Prerelease: true},
			{TagName: "v0.4.0", HTMLURL: "https://example.com/v0.4.0"},
		})
	}))
	defer srv.Close()

	tmp := t.TempDir()
	c := &Checker{
		CurrentVersion: "v0.4.0",
		Channel:        ChannelBeta,
		Interval:       DefaultInterval,
		APIBase:        srv.URL,
		HTTPClient:     srv.Client(),
		CachePath:      filepath.Join(tmp, "cache.json"),
	}
	notice := c.Check(context.Background())
	if notice == nil {
		t.Fatal("expected notice for newer beta")
	}
	if notice.LatestTag != "v0.5.0-beta.1" {
		t.Errorf("beta channel should surface prerelease, got %q", notice.LatestTag)
	}
}

func TestChecker_Check_CachedResult_NoAPICall(t *testing.T) {
	apiHits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiHits++
		_ = json.NewEncoder(w).Encode(Release{TagName: "v0.9.0", HTMLURL: "https://example.com"})
	}))
	defer srv.Close()

	tmp := t.TempDir()
	cachePath := filepath.Join(tmp, "cache.json")

	// Pre-seed cache: fresh stable hit from 1 minute ago.
	cache := Cache{
		Channel:   ChannelStable,
		CheckedAt: time.Now().Add(-1 * time.Minute),
		LatestTag: "v0.5.0",
		LatestURL: "https://example.com/v0.5.0",
	}
	if err := writeCache(cachePath, cache); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	c := &Checker{
		CurrentVersion: "v0.4.0",
		Channel:        ChannelStable,
		Interval:       DefaultInterval,
		APIBase:        srv.URL,
		HTTPClient:     srv.Client(),
		CachePath:      cachePath,
	}
	notice := c.Check(context.Background())
	if notice == nil || notice.LatestTag != "v0.5.0" {
		t.Errorf("expected cached v0.5.0, got %+v", notice)
	}
	if apiHits != 0 {
		t.Errorf("cache hit should not touch the API, got %d hits", apiHits)
	}
}

func TestChecker_Check_CurrentMatchesCachedLatest_ReFetches(t *testing.T) {
	// When the on-disk cache says "latest = v0.5.0" and we're running
	// v0.5.0 ourselves, the cache tells us nothing actionable. Bypass
	// it and hit the API so a fresh release cut after we upgraded to
	// v0.5.0 is visible on the next run, not up to Interval later.
	apiHits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiHits++
		_ = json.NewEncoder(w).Encode(Release{TagName: "v0.5.1", HTMLURL: "https://example.com/v0.5.1"})
	}))
	defer srv.Close()

	tmp := t.TempDir()
	cachePath := filepath.Join(tmp, "cache.json")

	// Seed: cached latest equals our running version (we upgraded to
	// whatever was "latest" at check-time, so the cache is telling us
	// "you're current" but a new release may have since shipped).
	cache := Cache{
		Channel:   ChannelStable,
		CheckedAt: time.Now().Add(-1 * time.Minute),
		LatestTag: "v0.5.0",
		LatestURL: "https://example.com/v0.5.0",
	}
	if err := writeCache(cachePath, cache); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	c := &Checker{
		CurrentVersion: "v0.5.0", // same as cached latest — cache is "useless"
		Channel:        ChannelStable,
		Interval:       DefaultInterval,
		APIBase:        srv.URL,
		HTTPClient:     srv.Client(),
		CachePath:      cachePath,
	}
	notice := c.Check(context.Background())
	if apiHits != 1 {
		t.Errorf("current==cached_latest should trigger a fresh fetch, got %d hits", apiHits)
	}
	if notice == nil || notice.LatestTag != "v0.5.1" {
		t.Errorf("expected fresh v0.5.1 notice, got %+v", notice)
	}
}

func TestChecker_Check_StaleCache_Refreshes(t *testing.T) {
	apiHits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiHits++
		_ = json.NewEncoder(w).Encode(Release{TagName: "v0.5.0", HTMLURL: "https://example.com/v0.5.0"})
	}))
	defer srv.Close()

	tmp := t.TempDir()
	cachePath := filepath.Join(tmp, "cache.json")

	// Seed cache with a stale entry (older than the interval).
	cache := Cache{
		Channel:   ChannelStable,
		CheckedAt: time.Now().Add(-48 * time.Hour),
		LatestTag: "v0.2.0",
	}
	if err := writeCache(cachePath, cache); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	c := &Checker{
		CurrentVersion: "v0.4.0",
		Channel:        ChannelStable,
		Interval:       24 * time.Hour,
		APIBase:        srv.URL,
		HTTPClient:     srv.Client(),
		CachePath:      cachePath,
	}
	notice := c.Check(context.Background())
	if apiHits != 1 {
		t.Errorf("stale cache should refresh once, got %d hits", apiHits)
	}
	if notice == nil || notice.LatestTag != "v0.5.0" {
		t.Errorf("expected refreshed v0.5.0, got %+v", notice)
	}
}

func TestChecker_Check_ChannelSwitch_RefreshesCache(t *testing.T) {
	apiHits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiHits++
		// This test uses the list endpoint (beta channel).
		_ = json.NewEncoder(w).Encode([]Release{
			{TagName: "v0.5.0-beta.1", HTMLURL: "https://example.com", Prerelease: true},
		})
	}))
	defer srv.Close()

	tmp := t.TempDir()
	cachePath := filepath.Join(tmp, "cache.json")

	// Seed cache with a fresh entry, but on the OTHER channel. The
	// check should refuse the cached entry because channel mismatches.
	cache := Cache{
		Channel:   ChannelStable,
		CheckedAt: time.Now().Add(-1 * time.Minute),
		LatestTag: "v0.5.0",
	}
	if err := writeCache(cachePath, cache); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	c := &Checker{
		CurrentVersion: "v0.4.0",
		Channel:        ChannelBeta,
		Interval:       DefaultInterval,
		APIBase:        srv.URL,
		HTTPClient:     srv.Client(),
		CachePath:      cachePath,
	}
	_ = c.Check(context.Background())
	if apiHits != 1 {
		t.Errorf("channel mismatch should trigger fresh API call, got %d hits", apiHits)
	}
}

func TestChecker_Check_SilentOnAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	tmp := t.TempDir()
	c := &Checker{
		CurrentVersion: "v0.4.0",
		Channel:        ChannelStable,
		Interval:       DefaultInterval,
		APIBase:        srv.URL,
		HTTPClient:     srv.Client(),
		CachePath:      filepath.Join(tmp, "cache.json"),
	}
	// Must not panic, must return nil, must not record anything.
	if notice := c.Check(context.Background()); notice != nil {
		t.Errorf("expected nil on API 5xx, got %+v", notice)
	}
}

func TestChecker_Check_DevVersion_NoNotify(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(Release{TagName: "v0.5.0", HTMLURL: "https://example.com"})
	}))
	defer srv.Close()

	tmp := t.TempDir()
	c := &Checker{
		CurrentVersion: "dev",
		Channel:        ChannelStable,
		Interval:       DefaultInterval,
		APIBase:        srv.URL,
		HTTPClient:     srv.Client(),
		CachePath:      filepath.Join(tmp, "cache.json"),
	}
	if notice := c.Check(context.Background()); notice != nil {
		t.Errorf("dev build should not notify, got %+v", notice)
	}
}
