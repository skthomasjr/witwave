FROM debian:bookworm-20260316-slim

SHELL ["/bin/bash", "-o", "pipefail", "-c"]

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y \
    ca-certificates \
    curl \
    python3 \
    python3-pip \
    python3-venv \
    && rm -rf /var/lib/apt/lists/*

RUN useradd -m -s /bin/bash -u 1000 agent

USER agent
WORKDIR /home/agent

RUN python3 -m venv .venv
ENV PATH="/home/agent/.venv/bin:$PATH"

COPY --chown=agent:agent backends/echo/requirements.txt /home/agent/echo/requirements.txt
RUN pip install --no-cache-dir -r /home/agent/echo/requirements.txt

COPY --chown=agent:agent backends/echo/ /home/agent/echo/
COPY --chown=agent:agent shared/ /home/agent/shared/

# Resolves both `import shared.foo` (future use) and the bare `import
# metrics_server` pattern the shared helpers use internally.
ENV PYTHONPATH="/home/agent/shared:/home/agent/echo"

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD curl -f http://localhost:${BACKEND_PORT:-8000}/health || exit 1

EXPOSE 8000
# Dedicated Prometheus metrics listener (#643). Served on 9000 by default;
# override with METRICS_PORT env var. Only active when METRICS_ENABLED is set.
EXPOSE 9000

# WORKDIR alignment — match harness, claude, codex, gemini. Echo had
# previously stayed at /home/agent (the initial build-time WORKDIR);
# every other container in a witwave agent pod runs at
# /home/agent/workspace, so a `pwd` from any container's shell now
# returns the same path. Workspace volume mounts (when present) live
# at /workspaces/<workspace>/<volume> regardless — this WORKDIR change
# only affects the container's default working directory.
RUN mkdir -p /home/agent/workspace
WORKDIR /home/agent/workspace

CMD ["python3", "/home/agent/echo/main.py"]
