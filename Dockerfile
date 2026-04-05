FROM debian:bookworm-20260316-slim

SHELL ["/bin/bash", "-o", "pipefail", "-c"]

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y \
    zsh \
    git \
    curl \
    jq \
    ca-certificates \
    gnupg \
    procps \
    iptables \
    python3 \
    python3-pip \
    python3-venv \
    && rm -rf /var/lib/apt/lists/*

# Node.js 20
RUN curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
    && apt-get install -y nodejs \
    && rm -rf /var/lib/apt/lists/*

# GitHub CLI
RUN curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg | gpg --dearmor -o /usr/share/keyrings/githubcli-archive-keyring.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" | tee /etc/apt/sources.list.d/github-cli.list \
    && apt-get update && apt-get install -y gh \
    && rm -rf /var/lib/apt/lists/*

# Claude Code CLI and linting tools
RUN npm install -g @anthropic-ai/claude-code@2.1.91 markdownlint-cli@0.44.0 prettier@3.5.3

RUN useradd -m -s /usr/bin/zsh -u 1000 agent

USER agent
WORKDIR /home/agent

RUN python3 -m venv .venv
ENV PATH="/home/agent/.venv/bin:$PATH"

RUN pip install --no-cache-dir \
    claude-agent-sdk==0.1.55 \
    a2a-sdk==0.3.25 \
    croniter==6.2.2 \
    prometheus-client==0.24.1 \
    uvicorn==0.43.0 \
    watchfiles==1.1.1 \
    yamllint==1.37.0 \
    ruff==0.11.2

COPY --chown=agent:agent agent/ /home/agent/agent/

RUN mkdir -p /home/agent/workspace
WORKDIR /home/agent/workspace

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD curl -f http://localhost:${AGENT_PORT:-8000}/health/live || exit 1

EXPOSE 8000

CMD ["python3", "/home/agent/agent/main.py"]
