# blog content

Blog entries are authored as Markdown files under `social/posts/` with YAML-style frontmatter. The browser cannot list a
directory on GitHub Pages, so `social/posts/posts.json` is the manifest that tells the website which Markdown files are
eligible to load.

The website does not keep a symlinked mirror of the posts in this folder. In production, the blog JavaScript fetches the
manifest and Markdown from the public `witwave-ai/witwave` repository on `main`. That keeps the website repository
stable while allowing post changes to go live after a normal source-repo commit and push.

## Files

```text
social/posts/
├── posts.json              # manifest of Markdown files the browser can fetch
└── <slug>.md               # Markdown post with frontmatter
```

## Frontmatter contract

```yaml
---
title: "Post title"
slug: "post-slug"
status: "published" # draft | scheduled | published | archived
display: true
sample: false
published_at: "2026-05-13"
summary: "Short card and reader summary."
tags: ["agentic-ai", "software-engineering"]
surfaces: ["blog", "twitter", "linkedin"]
published_urls:
  blog: "https://witwave.ai/blog/post/?post=post-slug"
  twitter: null
  linkedin: null
---
```

Only posts with `status: "published"` and `display: true` appear on the public blog index and reader. Drafts can remain
in the manifest for local checks without appearing publicly.

## Local preview

To preview unpublished local post changes, serve the repository root and open the site through its repo path:

```bash
python3 -m http.server 8080 --directory .
```

Then open `http://localhost:8080/social/website/blog/`.

Serving only `social/website/` is still useful for layout checks, but the blog will fetch public content from GitHub in
that mode.

## Empty state

Open `/social/website/blog/?empty=1` during repo-root preview, or `/blog/?empty=1` on the published site, to force the
no-posts state even when sample or published posts exist.

## Distribution links

Use `published_urls` to add links after a post is shared elsewhere. Keep unavailable surfaces as `null`; the website
only renders links with real URLs.
