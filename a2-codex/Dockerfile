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

RUN useradd -m -s /usr/bin/zsh -u 1000 agent

USER agent
WORKDIR /home/agent

RUN python3 -m venv .venv
ENV PATH="/home/agent/.venv/bin:$PATH"

COPY --chown=agent:agent a2-codex/requirements.txt /home/agent/a2-codex/requirements.txt
RUN pip install --no-cache-dir -r /home/agent/a2-codex/requirements.txt

COPY --chown=agent:agent a2-codex/ /home/agent/a2-codex/

RUN mkdir -p /home/agent/logs /home/agent/memory
WORKDIR /home/agent/workspace

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD curl -f http://localhost:${BACKEND_PORT:-8080}/health || exit 1

EXPOSE 8080

CMD ["python3", "/home/agent/a2-codex/main.py"]
