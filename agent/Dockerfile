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
    iptables \
    python3 \
    python3-pip \
    python3-venv \
    && rm -rf /var/lib/apt/lists/*

# GitHub CLI
RUN curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg | gpg --dearmor -o /usr/share/keyrings/githubcli-archive-keyring.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" | tee /etc/apt/sources.list.d/github-cli.list \
    && apt-get update && apt-get install -y gh \
    && rm -rf /var/lib/apt/lists/*

RUN useradd -m -s /usr/bin/zsh -u 1000 agent

USER agent
WORKDIR /home/agent

RUN python3 -m venv .venv
ENV PATH="/home/agent/.venv/bin:$PATH"

RUN pip install --no-cache-dir \
    a2a-sdk==0.3.25 \
    croniter==6.2.2 \
    prometheus-client==0.24.1 \
    uvicorn==0.43.0 \
    watchfiles==1.1.1 \
    yamllint==1.37.0 \
    ruff==0.11.2 \
    pyyaml==6.0.2

COPY --chown=agent:agent agent/ /home/agent/agent/

RUN mkdir -p /home/agent/.nyx /home/agent/workspace
WORKDIR /home/agent/workspace

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD curl -f http://localhost:${AGENT_PORT:-8000}/health/live || exit 1

EXPOSE 8000

CMD ["python3", "/home/agent/agent/main.py"]
