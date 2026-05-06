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
# Note: iptables was removed (#1260). PSS-restricted drops ALL caps
# including NET_ADMIN, so the binary couldn't modify kernel tables at
# runtime — it was just dead attack surface.

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

# Node.js 20
RUN curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
    && apt-get install -y nodejs \
    && rm -rf /var/lib/apt/lists/*

# Claude Code CLI
RUN npm install -g @anthropic-ai/claude-code@2.1.91

# Code-hygiene tooling for nova (used by her code-format / code-verify /
# code-document skills). Shared in the claude image so every agent has
# the binaries available; iris and kira don't invoke them from their
# skills, but the cost is one image build, not per-agent images.

# shellcheck — diagnostic linter for shell scripts (nova code-format).
RUN apt-get update && apt-get install -y shellcheck \
    && rm -rf /var/lib/apt/lists/*

# Go toolchain — needed for gofmt + goimports (nova code-format Go
# pass). Pin the version so image rebuilds are reproducible. We install
# goimports via `go install` then keep the whole toolchain because
# gofmt is part of it (no point in stripping back to fragments).
ARG GO_VERSION=1.23.4
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
RUN go install "golang.org/x/tools/cmd/goimports@${GOIMPORTS_VERSION}" \
    && cp "$GOPATH/bin/goimports" /usr/local/bin/goimports \
    && rm -rf "$GOPATH"

# shfmt — shell-script formatter (nova code-format auto-fix path).
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
# Hadolint releases name binaries with kernel-arch (x86_64 / arm64)
# rather than dpkg-arch (amd64 / arm64), so a small translation is
# needed.
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

# Helm — chart linting + diff inspection (nova code-format helm-lint
# step; also useful for any agent that needs to introspect rendered
# manifests). Official install script auto-detects arch.
RUN curl -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 \
      | DESIRED_VERSION=v3.16.3 USE_SUDO=false HELM_INSTALL_DIR=/usr/local/bin bash

RUN useradd -m -s /usr/bin/zsh -u 1000 agent

USER agent
WORKDIR /home/agent

RUN python3 -m venv .venv
ENV PATH="/home/agent/.venv/bin:$PATH"

COPY --chown=agent:agent backends/claude/requirements.txt /home/agent/claude/requirements.txt
RUN pip install --no-cache-dir -r /home/agent/claude/requirements.txt

# Code-hygiene Python tooling for nova (ruff = formatter+lint, yamllint
# = YAML semantic linter). Installed in the agent venv alongside
# claude's runtime deps so the same PATH resolution works. Pinned
# because tooling drift could change auto-fix behaviour silently.
RUN pip install --no-cache-dir 'ruff==0.6.9' 'yamllint==1.35.1'

COPY --chown=agent:agent backends/claude/ /home/agent/claude/
COPY --chown=agent:agent shared/ /home/agent/shared/

ENV PYTHONPATH="/home/agent/shared"

RUN mkdir -p /home/agent/logs /home/agent/.claude/memory
WORKDIR /home/agent/workspace

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD curl -f http://localhost:${BACKEND_PORT:-8000}/health || exit 1

EXPOSE 8000
# Dedicated Prometheus metrics listener (#643). Served on 9000 by default;
# override with METRICS_PORT env var. Only active when METRICS_ENABLED is set.
EXPOSE 9000

CMD ["python3", "/home/agent/claude/main.py"]
