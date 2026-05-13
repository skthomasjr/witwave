# witwave website source

This directory is the source for the public witwave website. In the main `witwave` repository it lives at
`social/website/`; in the dedicated GitHub Pages repository it is copied to the repository root. The source of truth for
content strategy and page copy remains the `witwave` repository.

## Goals

- Make the two foundational whitepapers prominent:
  - `social/papers/three-phases-of-ai-adoption.md`
  - `social/papers/anatomy-of-an-agentic-team.md`
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
├── assets/
│   ├── styles.css                # shared visual system
│   ├── js/
│   │   └── markdown-reader.js    # buildless Markdown renderer for papers
│   └── images/                   # copied site assets for publish repo portability
├── whitepapers/
│   └── index.html                # whitepaper hub
├── reader/
│   └── index.html                # clickable Markdown whitepaper reader
├── team/
│   └── index.html                # public self-team roster with avatars and roles
├── blog/
│   ├── index.html                # dynamic Markdown blog index
│   └── post/
│       └── index.html            # Markdown blog reader
├── positioning/
│   └── index.html                # marketing / narrative page
└── content/
    ├── README.md                 # content operation notes
    ├── whitepapers.json          # source-of-truth map for paper cards
    ├── team.json                 # self-team roster source for the Team page
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

For now this is a static HTML/CSS/JS scaffold. When the dedicated website repository is ready, the initial publish can
copy this directory as-is. The current whitepaper reader fetches Markdown from `content/whitepapers/`, which is
symlinked back to the canonical files under `social/papers/` in this repo. The blog reader is intentionally different:
canonical posts and their manifest live under `social/posts/`, and the browser resolves the latest `main` commit SHA
from the public `witwave-ai/witwave` repository before fetching commit-pinned raw Markdown. That means blog content
changes need a normal source-repo commit and push, but they do not require a website-repo publish unless the site shell
changes.

If the publishing workflow copies this site into another repository, use a symlink-resolving copy such as
`rsync -aL social/website/ <publish-repo>/` so whitepaper Markdown content is materialised as normal files. Later, if the
site needs generated pages from markdown, this folder can grow into an Astro, Vite, or other static-site build without
changing the content model.

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
- Treat `content/team.json` as the roster source for the public Team page.
- Treat `social/posts/posts.json` as the browser-visible blog discovery manifest; GitHub Pages does not expose folder
  listings, so Markdown posts must be listed there before the website can load them.
- Keep the homepage focused: one thesis, two foundational papers, one clear path to blog/updates.
- Do not bury the whitepapers behind a generic resources page.
- Keep marketing claims grounded in the two papers unless a source is added.
- Prefer small static changes over introducing a build system until the publishing repo requires it.
- If a paper title or slug changes, update `index.html`, `whitepapers/index.html`, `reader/index.html` links, and
  `content/whitepapers.json` in the same change.
- Name blog post files `yyyy-mm-dd-title-goes-here.md`.
- If a blog slug changes, update its frontmatter, `social/posts/posts.json`, and any social `published_urls` together.

## Local preview

From this repository root:

```bash
python3 -m http.server 8080 --directory .
```

Then open `http://localhost:8080/social/website/`.

Serving only `social/website/` is fine for layout checks, but the blog will read public content from GitHub in that mode.
Serve the repository root when previewing unpublished local blog posts.
