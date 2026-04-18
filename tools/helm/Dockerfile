FROM python:3.12-slim-bookworm

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    HELM_VERSION=v3.16.3

RUN apt-get update && apt-get install -y --no-install-recommends \
      curl \
      ca-certificates \
    && curl -fsSL "https://get.helm.sh/helm-${HELM_VERSION}-linux-amd64.tar.gz" \
       | tar -xz -C /tmp \
    && mv /tmp/linux-amd64/helm /usr/local/bin/helm \
    && rm -rf /tmp/linux-amd64 \
    && apt-get purge -y curl \
    && apt-get autoremove -y \
    && rm -rf /var/lib/apt/lists/*

RUN useradd -m -u 1000 tool
USER tool
WORKDIR /home/tool

COPY --chown=tool:tool tools/helm/requirements.txt /home/tool/requirements.txt
RUN pip install --no-cache-dir --user -r /home/tool/requirements.txt

ENV PATH="/home/tool/.local/bin:$PATH"

COPY --chown=tool:tool tools/helm/ /home/tool/
# Shared helpers (#637, #643) — both mounted under /home/tool/shared so
# server.py can `from otel import ...` and
# `from metrics_server import start_metrics_server_in_thread`.
COPY --chown=tool:tool shared/otel.py /home/tool/shared/otel.py
COPY --chown=tool:tool shared/metrics_server.py /home/tool/shared/metrics_server.py

# FastMCP streamable-http server (#644). Advertise the port so `docker run
# -P` and Kubernetes Service targetPort discovery work the same way they
# do for the agent containers. Override with MCP_PORT env var if needed.
EXPOSE 8000
# Dedicated Prometheus metrics listener (#643, #650). Served on 9000 by
# default; override with METRICS_PORT env var. Only active when
# METRICS_ENABLED is set.
EXPOSE 9000

CMD ["python", "/home/tool/server.py"]
