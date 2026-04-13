---
name: release
description: Create a new GitHub release with auto-generated notes from commits and closed issues. Trigger when the user says "create a release", "cut a release", "release a new version", "tag a release", or "run release".
version: 1.0.0
---

# release

This is a leaf skill. It determines the next version, generates release notes from the delta since the last release, confirms with the user, and publishes a GitHub release (which also creates the tag and triggers the CI build).

## Instructions

**Step 1: Determine the current version.**

```bash
git tag --sort=-version:refname | head -1
```

If no tags exist, the current version is `0.0.0`.

**Step 2: Determine the next version.**

If the user specified a version (e.g. "release v1.2.0") or a bump type (e.g. "minor release", "patch release"), use that. Otherwise ask: patch, minor, or major?

Apply semver rules:
- **patch** — bug fixes and risk mitigations only (x.y.Z)
- **minor** — new features or gaps filled, backwards compatible (x.Y.0)
- **major** — breaking changes (X.0.0)

**Step 3: Gather the delta.**

Get all commits since the last tag:

```bash
git log <last-tag>..HEAD --oneline
```

Get all issues closed since the last tag was created:

```bash
gh issue list --state closed --json number,title,labels,closedAt \
  --jq '[.[] | select(.closedAt > "<last-tag-date>")]'
```

Get the last tag date with:

```bash
git log -1 --format=%aI <last-tag>
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
- The new version number
- The release notes draft
- The images that will be built (all five: nyx-agent, a2-claude, a2-codex, a2-gemini, ui)

Ask the user to confirm before proceeding.

**Step 6: Create the GitHub release.**

```bash
gh release create <new-version> \
  --title "<new-version>" \
  --notes "<release-notes>" \
  --verify-tag=false
```

This creates the tag and the release page in one step, which also triggers the CI release workflow.

**Step 7: Report.**

Return:
- The release URL: `https://github.com/<repo>/releases/tag/<new-version>`
- The Actions URL to watch the image builds: `https://github.com/<repo>/actions`
