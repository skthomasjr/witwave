FROM debian:bookworm-20260316-slim

SHELL ["/bin/bash", "-o", "pipefail", "-c"]

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y \
    curl \
    ca-certificates \
    python3 \
    python3-pip \
    python3-venv \
    && rm -rf /var/lib/apt/lists/*

RUN useradd -m -s /bin/bash -u 1000 agent

USER agent
WORKDIR /home/agent

RUN python3 -m venv .venv
ENV PATH="/home/agent/.venv/bin:$PATH"

RUN pip install --no-cache-dir \
    "a2a-sdk[http-server]==0.3.25" \
    croniter==6.2.2 \
    httpx==0.28.1 \
    prometheus-client==0.24.1 \
    uvicorn==0.43.0 \
    watchfiles==1.1.1 \
    pyyaml==6.0.2 \
    opentelemetry-api==1.30.0 \
    opentelemetry-sdk==1.30.0 \
    opentelemetry-exporter-otlp-proto-http==1.30.0

COPY --chown=agent:agent harness/ /home/agent/harness/
COPY --chown=agent:agent shared/ /home/agent/shared/

ENV PYTHONPATH="/home/agent/shared"

RUN mkdir -p /home/agent/.nyx /home/agent/workspace
WORKDIR /home/agent/workspace

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD curl -f http://localhost:${AGENT_PORT:-8000}/health/live || exit 1

EXPOSE 8000

CMD ["python3", "/home/agent/harness/main.py"]
