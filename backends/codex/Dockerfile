FROM debian:bookworm-20260316-slim

SHELL ["/bin/bash", "-o", "pipefail", "-c"]

ENV DEBIAN_FRONTEND=noninteractive \
    GH_PROMPT_DISABLED=1

RUN apt-get update && apt-get install -y \
    zsh \
    git \
    curl \
    jq \
    ca-certificates \
    gnupg \
    procps \
    python3 \
    python3-pip \
    python3-venv \
    && rm -rf /var/lib/apt/lists/*
# #1336: chromium runtime libs (libnss3, libatk*, libcups2, libdrm2,
# libxkbcommon0, libx*, libgbm1, libasound2) removed from this apt
# list — `playwright install chromium --with-deps` below invokes
# apt-get for the version-matched set playwright depends on. The
# upfront list duplicated that work, wasted build time, and drifted
# silently when playwright updated its dep set.
# Note: iptables was removed (#1260). The container runs without NET_ADMIN
# (PSS-restricted drops ALL caps in charts/witwave/templates/deployment.yaml),
# so the binary couldn't modify kernel tables anyway — it was just dead
# attack surface.

# GitHub CLI (gh) — used by the release skill to verify CI is green
# before tagging. The GH_PROMPT_DISABLED env var (set above) was
# already in place in anticipation of this install.
RUN curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
      | gpg --dearmor -o /usr/share/keyrings/githubcli-archive-keyring.gpg \
    && chmod go+r /usr/share/keyrings/githubcli-archive-keyring.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
      > /etc/apt/sources.list.d/github-cli.list \
    && apt-get update && apt-get install -y gh \
    && rm -rf /var/lib/apt/lists/*

# Code-hygiene tooling for nova (used by her code-format / code-verify /
# code-document skills). Shared across backend images so any agent has
# the binaries available; iris and kira don't invoke them but the cost
# is one image build, not per-agent images.

# Node.js 20 — used by evan's bug-work scan of `clients/dashboard`
# (TS/Vue project; runs `vue-tsc --noEmit` for bug-class type errors).
# Also generally useful for any agent that wants to run `npm`/`npx`
# tooling against the dashboard subproject.
RUN curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
    && apt-get install -y nodejs \
    && rm -rf /var/lib/apt/lists/*

# shellcheck — diagnostic linter for shell scripts (nova code-format).
RUN apt-get update && apt-get install -y shellcheck \
    && rm -rf /var/lib/apt/lists/*

# Go toolchain — needed for gofmt + goimports (nova code-format Go
# pass), evan's bug-class analyzers (staticcheck SA-rules, errcheck,
# ineffassign, controller-gen drift check), and evan's risk-class
# scanners (govulncheck, gosec). Every version pinned for
# reproducibility.
ARG GO_VERSION=1.26.3
RUN ARCH=$(dpkg --print-architecture) \
    && case "$ARCH" in \
        amd64) GO_ARCH=amd64 ;; \
        arm64) GO_ARCH=arm64 ;; \
        *) echo "Unsupported arch: $ARCH" >&2 && exit 1 ;; \
       esac \
    && curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${GO_ARCH}.tar.gz" \
         -o /tmp/go.tar.gz \
    && tar -C /usr/local -xzf /tmp/go.tar.gz \
    && rm /tmp/go.tar.gz
ENV PATH="/usr/local/go/bin:/usr/local/bin:$PATH" \
    GOPATH="/usr/local/gopath" \
    GOFLAGS="-mod=mod"
ARG GOIMPORTS_VERSION=v0.27.0
ARG STATICCHECK_VERSION=2026.1
ARG ERRCHECK_VERSION=v1.10.0
ARG INEFFASSIGN_VERSION=v0.1.0
ARG CONTROLLER_GEN_VERSION=v0.16.5
ARG GOVULNCHECK_VERSION=v1.1.4
ARG GOSEC_VERSION=v2.26.1
RUN go install "golang.org/x/tools/cmd/goimports@${GOIMPORTS_VERSION}" \
    && go install "honnef.co/go/tools/cmd/staticcheck@${STATICCHECK_VERSION}" \
    && go install "github.com/kisielk/errcheck@${ERRCHECK_VERSION}" \
    && go install "github.com/gordonklaus/ineffassign@${INEFFASSIGN_VERSION}" \
    && go install "sigs.k8s.io/controller-tools/cmd/controller-gen@${CONTROLLER_GEN_VERSION}" \
    && go install "golang.org/x/vuln/cmd/govulncheck@${GOVULNCHECK_VERSION}" \
    && go install "github.com/securego/gosec/v2/cmd/gosec@${GOSEC_VERSION}" \
    && cp "$GOPATH/bin/"* /usr/local/bin/ \
    && rm -rf "$GOPATH"

# shfmt — shell-script formatter (nova code-format auto-fix).
ARG SHFMT_VERSION=3.10.0
RUN ARCH=$(dpkg --print-architecture) \
    && curl -fsSL "https://github.com/mvdan/sh/releases/download/v${SHFMT_VERSION}/shfmt_v${SHFMT_VERSION}_linux_${ARCH}" \
         -o /usr/local/bin/shfmt \
    && chmod +x /usr/local/bin/shfmt

# actionlint — GitHub Actions workflow linter (nova code-format).
ARG ACTIONLINT_VERSION=1.7.7
RUN ARCH=$(dpkg --print-architecture) \
    && curl -fsSL "https://github.com/rhysd/actionlint/releases/download/v${ACTIONLINT_VERSION}/actionlint_${ACTIONLINT_VERSION}_linux_${ARCH}.tar.gz" \
         | tar -xz -C /usr/local/bin actionlint \
    && chmod +x /usr/local/bin/actionlint

# hadolint — Dockerfile linter (nova code-format Dockerfile pass).
# Hadolint releases use kernel-arch (x86_64 / arm64) rather than
# dpkg-arch (amd64 / arm64).
ARG HADOLINT_VERSION=2.12.0
RUN ARCH=$(dpkg --print-architecture) \
    && case "$ARCH" in \
        amd64) HADOLINT_ARCH=x86_64 ;; \
        arm64) HADOLINT_ARCH=arm64 ;; \
        *) echo "Unsupported arch: $ARCH" >&2 && exit 1 ;; \
       esac \
    && curl -fsSL "https://github.com/hadolint/hadolint/releases/download/v${HADOLINT_VERSION}/hadolint-Linux-${HADOLINT_ARCH}" \
         -o /usr/local/bin/hadolint \
    && chmod +x /usr/local/bin/hadolint

# gitleaks — secret-in-source detection (evan risk-work).
ARG GITLEAKS_VERSION=8.30.1
RUN ARCH=$(dpkg --print-architecture) \
    && case "$ARCH" in \
        amd64) GITLEAKS_ARCH=x64 ;; \
        arm64) GITLEAKS_ARCH=arm64 ;; \
        *) echo "Unsupported arch: $ARCH" >&2 && exit 1 ;; \
       esac \
    && curl -fsSL "https://github.com/gitleaks/gitleaks/releases/download/v${GITLEAKS_VERSION}/gitleaks_${GITLEAKS_VERSION}_linux_${GITLEAKS_ARCH}.tar.gz" \
         | tar -xz -C /usr/local/bin gitleaks \
    && chmod +x /usr/local/bin/gitleaks

# trivy — filesystem CVE scan + image scan (evan risk-work).
ARG TRIVY_VERSION=0.70.0
RUN ARCH=$(dpkg --print-architecture) \
    && case "$ARCH" in \
        amd64) TRIVY_ARCH=64bit ;; \
        arm64) TRIVY_ARCH=ARM64 ;; \
        *) echo "Unsupported arch: $ARCH" >&2 && exit 1 ;; \
       esac \
    && curl -fsSL "https://github.com/aquasecurity/trivy/releases/download/v${TRIVY_VERSION}/trivy_${TRIVY_VERSION}_Linux-${TRIVY_ARCH}.tar.gz" \
         | tar -xz -C /usr/local/bin trivy \
    && chmod +x /usr/local/bin/trivy

# Helm — chart linting + manifest introspection.
RUN curl -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 \
      | DESIRED_VERSION=v3.16.3 USE_SUDO=false HELM_INSTALL_DIR=/usr/local/bin bash

# witwave CLI (`ww`) — build from source so agents speak the same surface
# humans do. Stays in lockstep with main: built from the in-repo source
# tree at image build. Phase 1 of `ww` agent embedding extension to
# codex (2026-05-11) — binary lands in PATH now; no skill takes a
# runtime dependency yet. Mirrors the same block already in the claude
# backend Dockerfile.
#
# The Go toolchain installed above (matches clients/ww/go.mod's
# `go 1.26.3`) is reused for this build. GOPATH/GOCACHE are scoped to
# /tmp and cleaned after the build so the layer adds only the binary
# (~12 MB), not module cache.
COPY clients/ww/go.mod clients/ww/go.sum /tmp/ww-src/
RUN cd /tmp/ww-src \
    && GOPATH=/tmp/gopath GOCACHE=/tmp/gocache go mod download
COPY clients/ww/ /tmp/ww-src/
RUN cd /tmp/ww-src \
    && GOPATH=/tmp/gopath GOCACHE=/tmp/gocache CGO_ENABLED=0 \
       go build -trimpath -ldflags="-s -w" -o /usr/local/bin/ww . \
    && rm -rf /tmp/ww-src /tmp/gopath /tmp/gocache \
    && /usr/local/bin/ww version

RUN useradd -m -s /usr/bin/zsh -u 1000 agent

# Playwright browser install must run as root (#1259): `playwright install
# --with-deps` calls apt-get to pull chromium's runtime libs. Running it
# after `USER agent` silently fell back to the non-privileged branch
# (2>/dev/null suppressed the permission errors) and left the image
# without the expected browser-launch baseline. Install into a shared
# world-readable location (PLAYWRIGHT_BROWSERS_PATH) so the non-root
# `agent` user at runtime can launch chromium without needing its own
# per-home copy.
ENV PLAYWRIGHT_BROWSERS_PATH=/opt/ms-playwright
RUN python3 -m venv /home/agent/.venv && chown -R agent:agent /home/agent/.venv
ENV PATH="/home/agent/.venv/bin:$PATH"

COPY --chown=agent:agent backends/codex/requirements.txt /home/agent/codex/requirements.txt
RUN pip install --no-cache-dir -r /home/agent/codex/requirements.txt \
    && playwright install chromium --with-deps \
    && chmod -R a+rX "${PLAYWRIGHT_BROWSERS_PATH}"

# Code-hygiene Python tooling for nova (ruff = formatter+lint, yamllint
# = YAML semantic linter). Installed in the agent venv alongside
# codex's runtime deps. Pinned for reproducibility.
RUN pip install --no-cache-dir 'ruff==0.6.9' 'yamllint==1.35.1'

# Bug-class verification tooling for evan. pytest + pytest-asyncio run
# the project's scoped tests locally before evan commits a fix (Step 5
# of bug-work). httpx and the python kubernetes client are the test
# dependencies CI installs ad-hoc per ci-charts.yml's mcp-tools-pytest
# job; pinning them in the image so evan's local-test runs match what
# CI runs. Pinned for reproducibility.
RUN pip install --no-cache-dir \
    'pytest==8.3.3' \
    'pytest-asyncio==0.24.0' \
    'httpx==0.27.2' \
    'kubernetes==31.0.0'

# Risk-class scanners for evan's risk-work skill. pip-audit checks
# requirements.txt for CVEs; bandit runs Python security-class lints.
# semgrep deliberately not included yet — large dep, deferred.
RUN pip install --no-cache-dir \
    'pip-audit==2.10.0' \
    'bandit==1.9.4'

USER agent
WORKDIR /home/agent

COPY --chown=agent:agent backends/codex/ /home/agent/codex/
COPY --chown=agent:agent shared/ /home/agent/shared/

ENV PYTHONPATH="/home/agent/shared"

RUN mkdir -p /home/agent/logs /home/agent/.codex/memory
WORKDIR /home/agent/workspace

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD curl -f http://localhost:${BACKEND_PORT:-8000}/health || exit 1

EXPOSE 8000
# Dedicated Prometheus metrics listener (#643). Served on 9000 by default;
# override with METRICS_PORT env var. Only active when METRICS_ENABLED is set.
EXPOSE 9000

CMD ["python3", "/home/agent/codex/main.py"]
