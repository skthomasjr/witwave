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

# Node.js 20
RUN curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
    && apt-get install -y nodejs \
    && rm -rf /var/lib/apt/lists/*

# Claude Code CLI
RUN npm install -g @anthropic-ai/claude-code@2.1.91

RUN useradd -m -s /usr/bin/zsh -u 1000 agent

USER agent
WORKDIR /home/agent

RUN python3 -m venv .venv
ENV PATH="/home/agent/.venv/bin:$PATH"

COPY --chown=agent:agent backends/a2-claude/requirements.txt /home/agent/a2-claude/requirements.txt
RUN pip install --no-cache-dir -r /home/agent/a2-claude/requirements.txt

COPY --chown=agent:agent backends/a2-claude/ /home/agent/a2-claude/
COPY --chown=agent:agent shared/ /home/agent/shared/

ENV PYTHONPATH="/home/agent/shared"

RUN mkdir -p /home/agent/logs /home/agent/.claude/memory
WORKDIR /home/agent/workspace

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD curl -f http://localhost:${BACKEND_PORT:-8000}/health || exit 1

EXPOSE 8000

CMD ["python3", "/home/agent/a2-claude/main.py"]
