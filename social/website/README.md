# witwave website source

This directory is the source for the public witwave website. In the main `witwave` repository it lives at
`social/website/`; in the dedicated GitHub Pages repository it is copied to the repository root. The source of truth for
content strategy and page copy remains the `witwave` repository.

## Goals

- Make the two foundational whitepapers prominent:
  - `social/papers/three-phases-of-ai-adoption.md`
  - `social/papers/anatomy-of-an-agentic-team.md`
- Provide a clear project entry point that explains the framework and links to the GitHub repository.
- Provide a home for long-form blog entries derived from `social/posts/` and future essays.
- Hold lightweight marketing/positioning copy for the public site.
- Keep the site easy for AI agents to maintain: simple files, explicit content maps, clear publishing rules.

## Current shape

```text
social/website/
├── README.md
├── .nojekyll                    # carry through when mirrored to GitHub Pages
├── CNAME                         # GitHub Pages custom domain: witwave.ai
├── index.html                    # homepage
├── project/
│   └── index.html                # project overview + GitHub entry point
├── quickstart/
│   └── index.html                # prominent ww CLI + Kubernetes first-run path
├── assets/
│   ├── styles.css                # shared visual system
│   ├── js/
│   │   ├── blog.js               # dynamic Markdown blog loader
│   │   ├── markdown-reader.js    # buildless Markdown renderer for papers
│   │   └── quickstart-copy.js    # copy controls for Quick Start commands
│   └── images/                   # copied site assets for publish repo portability
├── whitepapers/
│   └── index.html                # whitepaper hub
├── reader/
│   └── index.html                # clickable Markdown whitepaper reader
├── team/
│   └── index.html                # public working-team roster with avatars and roles
├── blog/
│   ├── index.html                # dynamic Markdown blog index
│   └── post/
│       └── index.html            # Markdown blog reader
└── content/
    ├── README.md                 # content operation notes
    ├── whitepapers.json          # source-of-truth map for paper cards
    ├── team.json                 # working-team roster source for the Team page
    ├── whitepapers/              # symlinks to canonical markdown sources
    ├── blog/
    │   └── README.md
    └── marketing/
        └── positioning.md
```

Adjacent canonical source content:

```text
social/posts/
├── posts.json                    # browser-fetchable blog manifest
└── yyyy-mm-dd-title-goes-here.md # Markdown posts with frontmatter
```

## Publishing model

This is a static HTML/CSS/JS site that is mirrored into the dedicated GitHub Pages repository. The whitepaper reader
fetches Markdown from `content/whitepapers/`, which is symlinked back to the canonical files under `social/papers/` in
this repo. The blog reader is intentionally different: canonical posts and their manifest live under `social/posts/`,
and the browser resolves the latest `main` commit SHA from the public `witwave-ai/witwave` repository before fetching
commit-pinned raw Markdown. That means blog content changes need a normal source-repo commit and push, but they do not
require a website-repo publish unless the site shell changes.

The publishing workflow copies this site into another repository with symlinks resolved so whitepaper Markdown content
is materialised as normal files. Later, if the site needs generated pages from Markdown, this folder can grow into an
Astro, Vite, or other static-site build without changing the content model.

Publishing is automated from this repository by `.github/workflows/publish-social-website.yml`. The workflow copies
`social/website/` to `witwave-ai/witwave-ai.github.io` using `scripts/sync-social-website.sh`, resolves symlinks, and
copies the source-controlled `CNAME` file for the GitHub Pages custom domain. During high-iteration website work, the
workflow runs on every push to `main`; if the published site has no material changes, the publisher exits without
creating a commit in `witwave-ai.github.io`.

Required setup before the workflow can push:

1. Add a write-enabled deploy key to `witwave-ai/witwave-ai.github.io`.
2. Save the private key as a secret named `WITWAVE_AI_GITHUB_IO_DEPLOY_KEY` in this repository.
3. Configure GitHub Pages in `witwave-ai/witwave-ai.github.io` to serve from `main` / root with the custom domain
   `witwave.ai`.

If organization policy disables deploy keys, create a fine-grained token with `Contents: Read and write` access to
`witwave-ai/witwave-ai.github.io` and save it as `WITWAVE_AI_GITHUB_IO_TOKEN` instead. The workflow supports either
secret and exits cleanly without publishing when neither is configured.

## AI maintenance rules

- Treat `content/whitepapers.json` as the card/catalog source for whitepapers.
- Treat `content/team.json` as the roster source for the public Team page, including `members` for active seats and
  `futureMembers` for visible placeholder roles.
- Treat `social/posts/posts.json` as the browser-visible blog discovery manifest; GitHub Pages does not expose folder
  listings, so Markdown posts must be listed there before the website can load them.
- Keep the compact "Join the conversation" strips pointed at GitHub Discussion category URLs unless the community
  surface changes.
- Keep the homepage focused: one thesis, one clear project entry point, two foundational papers, and one path to
  blog/updates.
- Do not bury the whitepapers behind a generic resources page.
- Keep marketing claims grounded in the two papers unless a source is added.
- Prefer small static changes over introducing a build system until the publishing repo requires it.
- Keep `quickstart/index.html` short, command-first, and grounded in `clients/ww/README.md` plus
  `clients/ww/WALKTHROUGH.md`; it should always name Kubernetes cluster access as a prerequisite.
- If a paper title or slug changes, update `index.html`, `whitepapers/index.html`, `reader/index.html` links, and
  `content/whitepapers.json` in the same change.
- Name blog post files `yyyy-mm-dd-title-goes-here.md`.
- If a blog slug changes, update its frontmatter, `social/posts/posts.json`, and any social `published_urls` together.

## Planned content improvements

- **Wording posture.** Keep public copy in external-reader terms such as "working team" instead of internal repo terms
  such as "self team." Keep the adoption thesis framed as "agents provide immediate value; lifecycle-native integration
  is the compounding step."

## Local preview

From this repository root:

```bash
python3 -m http.server 8080 --directory .
```

Then open `http://localhost:8080/social/website/`.

Serving only `social/website/` is fine for layout checks, but the blog will read public content from GitHub in that mode.
Serve the repository root when previewing unpublished local blog posts.
