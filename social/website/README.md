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
├── blog/
│   └── index.html                # long-form blog placeholder
├── positioning/
│   └── index.html                # marketing / narrative page
└── content/
    ├── README.md                 # content operation notes
    ├── whitepapers.json          # source-of-truth map for paper cards
    ├── whitepapers/              # symlinks to canonical markdown sources
    ├── blog/
    │   └── README.md
    └── marketing/
        └── positioning.md
```

## Publishing model

For now this is a static HTML/CSS/JS scaffold. When the dedicated website repository is ready, the initial publish can
copy this directory as-is. The current whitepaper reader fetches Markdown from `content/whitepapers/`, which is
symlinked back to the canonical files under `social/papers/` in this repo. If the publishing workflow copies this site
into another repository, use a symlink-resolving copy such as `rsync -aL social/website/ <publish-repo>/` so the paper
content is materialised as normal files. Later, if the site needs generated pages from markdown, this folder can grow
into an Astro, Vite, or other static-site build without changing the content model.

Publishing is automated from this repository by `.github/workflows/publish-social-website.yml`. The workflow copies
`social/website/` to `witwave-ai/witwave-ai.github.io` using `scripts/sync-social-website.sh`, resolves symlinks, and
preserves a target-repo `CNAME` file if GitHub Pages custom-domain settings create one later.

Required setup before the workflow can push:

1. Add a write-enabled deploy key to `witwave-ai/witwave-ai.github.io`.
2. Save the private key as a secret named `WITWAVE_AI_GITHUB_IO_DEPLOY_KEY` in this repository.
3. Configure GitHub Pages in `witwave-ai/witwave-ai.github.io` to serve from `main` / root.

## AI maintenance rules

- Treat `content/whitepapers.json` as the card/catalog source for whitepapers.
- Keep the homepage focused: one thesis, two foundational papers, one clear path to blog/updates.
- Do not bury the whitepapers behind a generic resources page.
- Keep marketing claims grounded in the two papers unless a source is added.
- Prefer small static changes over introducing a build system until the publishing repo requires it.
- If a paper title or slug changes, update `index.html`, `whitepapers/index.html`, `reader/index.html` links, and
  `content/whitepapers.json` in the same change.

## Local preview

From this repository root:

```bash
python3 -m http.server 8080 --directory social/website
```

Then open `http://localhost:8080`.
