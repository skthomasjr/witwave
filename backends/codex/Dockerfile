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
    libnss3 \
    libatk1.0-0 \
    libatk-bridge2.0-0 \
    libcups2 \
    libdrm2 \
    libxkbcommon0 \
    libxcomposite1 \
    libxdamage1 \
    libxfixes3 \
    libxrandr2 \
    libgbm1 \
    libasound2 \
    && rm -rf /var/lib/apt/lists/*
# Note: iptables was removed (#1260). The container runs without NET_ADMIN
# (PSS-restricted drops ALL caps in charts/nyx/templates/deployment.yaml),
# so the binary couldn't modify kernel tables anyway — it was just dead
# attack surface.

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
