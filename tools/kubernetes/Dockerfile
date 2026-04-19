FROM python:3.12-slim-bookworm

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1

RUN useradd -m -u 1000 tool
USER tool
WORKDIR /home/tool

COPY --chown=tool:tool tools/kubernetes/requirements.txt /home/tool/requirements.txt
RUN pip install --no-cache-dir --user -r /home/tool/requirements.txt

ENV PATH="/home/tool/.local/bin:$PATH"

COPY --chown=tool:tool tools/kubernetes/ /home/tool/
# Shared helpers (#637, #643) — both mounted under /home/tool/shared so
# server.py can `from otel import ...` and
# `from metrics_server import start_metrics_server_in_thread`.
COPY --chown=tool:tool shared/otel.py /home/tool/shared/otel.py
COPY --chown=tool:tool shared/metrics_server.py /home/tool/shared/metrics_server.py
# Bearer-token auth middleware (#771) for the FastMCP streamable-http
# listener. Imported opportunistically by server.py — the server falls
# back to mcp.run() when the module is absent.
COPY --chown=tool:tool shared/mcp_auth.py /home/tool/shared/mcp_auth.py
# Prometheus tool-call metrics helper (#851). Imported opportunistically
# by server.py — degrades to no-op context manager when absent.
COPY --chown=tool:tool shared/mcp_metrics.py /home/tool/shared/mcp_metrics.py

# FastMCP streamable-http server (#644). Advertise the port so `docker run
# -P` and Kubernetes Service targetPort discovery work the same way they
# do for the agent containers. Override with MCP_PORT env var if needed.
EXPOSE 8000
# Dedicated Prometheus metrics listener (#643, #649). Served on 9000 by
# default; override with METRICS_PORT env var. Only active when
# METRICS_ENABLED is set.
EXPOSE 9000

CMD ["python", "/home/tool/server.py"]

# #1413: Docker HEALTHCHECK parity with harness + backends. Kubernetes
# uses its own probes; this helps docker-compose / swarm operators see
# tool health in `docker ps`.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD python3 -c "import os,urllib.request as u; u.urlopen(f'http://localhost:{os.environ.get(\"MCP_PORT\",\"8000\")}/health', timeout=3)" || exit 1
