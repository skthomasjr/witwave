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

# Code-hygiene tooling for nova (used by her code-format / code-verify /
# code-document skills). Shared across backend images so any agent has
# the binaries available; iris and kira don't invoke them but the cost
# is one image build, not per-agent images.

# shellcheck — diagnostic linter for shell scripts (nova code-format).
RUN apt-get update && apt-get install -y shellcheck \
    && rm -rf /var/lib/apt/lists/*

# Go toolchain — needed for gofmt + goimports (nova code-format Go
# pass), evan's bug-class analyzers (staticcheck SA-rules, errcheck,
# ineffassign, controller-gen drift check), and evan's risk-class
# scanners (govulncheck, gosec). Every version pinned for
# reproducibility.
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
ARG STATICCHECK_VERSION=2024.1.1
ARG ERRCHECK_VERSION=v1.7.0
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

RUN useradd -m -s /usr/bin/zsh -u 1000 agent

USER agent
WORKDIR /home/agent

RUN python3 -m venv .venv
ENV PATH="/home/agent/.venv/bin:$PATH"

COPY --chown=agent:agent backends/gemini/requirements.txt /home/agent/gemini/requirements.txt
RUN pip install --no-cache-dir -r /home/agent/gemini/requirements.txt

# Code-hygiene Python tooling for nova (ruff = formatter+lint, yamllint
# = YAML semantic linter). Installed in the agent venv alongside
# gemini's runtime deps. Pinned for reproducibility.
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

COPY --chown=agent:agent backends/gemini/ /home/agent/gemini/
COPY --chown=agent:agent shared/ /home/agent/shared/

ENV PYTHONPATH="/home/agent/shared"

RUN mkdir -p /home/agent/logs /home/agent/.gemini/memory
WORKDIR /home/agent/workspace

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD curl -f http://localhost:${BACKEND_PORT:-8000}/health || exit 1

EXPOSE 8000
# Dedicated Prometheus metrics listener (#643). Served on 9000 by default;
# override with METRICS_PORT env var. Only active when METRICS_ENABLED is set.
EXPOSE 9000

CMD ["python3", "/home/agent/gemini/main.py"]
