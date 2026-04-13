---
name: release
description: Create a new GitHub release with auto-generated notes from commits and closed issues. Trigger when the user says "create a release", "cut a release", "release a new version", "tag a release", "run release", "cut a beta", "create a beta", "release a beta", "cut a patch release", "cut a minor release", or "cut a major release".
version: 1.1.1
---

# release

This is a leaf skill. It determines the next version, generates release notes from the delta since the last release, confirms with the user, and publishes a GitHub release (which also creates the tag and triggers the CI build).

## Release types

- **beta** — pre-release for deployment and observation in a test environment. Tagged `vX.Y.Z-beta.N`. Marked as pre-release on GitHub. Same images built as a full release.
- **full release** — production-ready. Tagged `vX.Y.Z`. Promoted from beta or cut directly.

When the user says "beta" or "cut a beta", use the beta flow. Otherwise use the full release flow.

## Instructions

**Step 1: Determine the current version.**

```bash
git tag --sort=-version:refname | head -1
```

If no tags exist, the current version is `0.0.0`.

For beta releases, also check if a beta already exists for the target version:

```bash
git tag --sort=-version:refname | grep "^v<target>-beta\."
```

If a beta exists (e.g. `v0.1.0-beta.1`), the next beta is `v0.1.0-beta.2`. If none exists, start at `v0.1.0-beta.1`.

**Step 2: Determine the next version.**

If the user specified a version or bump type, use that. Otherwise ask: patch, minor, or major?

Apply semver rules:
- **patch** — bug fixes and risk mitigations only (x.y.Z)
- **minor** — new features or gaps filled, backwards compatible (x.Y.0)
- **major** — breaking changes (X.0.0)

For the delta baseline, use the last **full release** tag (not a beta tag) when determining what changed. This ensures beta and full release notes cover the same delta.

**Step 3: Gather the delta.**

Get all commits since the last full release tag:

```bash
git log <last-full-release-tag>..HEAD --oneline
```

Get all issues closed since the last full release tag was created:

```bash
gh issue list --state closed --json number,title,labels,closedAt \
  --jq '[.[] | select(.closedAt > "<last-tag-date>")]'
```

Get the last full release tag date with:

```bash
git log -1 --format=%aI <last-full-release-tag>
```

**Step 4: Generate release notes.**

Summarize the delta into sections. Only include sections that have content:

```
## What's Changed

### Features
- <feature title> (#number)

### Gaps Closed
- <gap title> (#number)

### Bugs Fixed
- <bug title> (#number)

### Risks Mitigated
- <risk title> (#number)

### Other Changes
- <commit subject> (<short-sha>)
```

Derive each item's section from its issue labels (`feature`, `gap`, `bug`, `risk`). Commits that don't correspond to a closed issue go under "Other Changes". Do not pad with noise — omit empty sections.

**Step 5: Confirm with the user.**

Show:
- The new version number and release type (beta or full)
- The release notes draft
- The images that will be built (all five: nyx-agent, a2-claude, a2-codex, a2-gemini, ui)

Ask the user to confirm before proceeding.

**Step 6: Create the GitHub release.**

For a full release:

```bash
gh release create <new-version> \
  --title "<new-version>" \
  --notes "<release-notes>" \
  --verify-tag=false
```

For a beta release:

```bash
gh release create <new-version> \
  --title "<new-version>" \
  --notes "<release-notes>" \
  --prerelease \
  --verify-tag=false
```

This creates the tag and the release page in one step, which also triggers the CI release workflow.

**Step 7: Report.**

Return:
- The release URL: `https://github.com/<repo>/releases/tag/<new-version>`
- The Actions URL to watch the image builds: `https://github.com/<repo>/actions`
- For beta releases, note that this is a pre-release and will not be shown as the latest release on the repo home page.
