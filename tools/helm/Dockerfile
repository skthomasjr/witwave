FROM python:3.12-slim-bookworm

# Helm CLI version. Keep at or above v3.20.2 to carry the CERT-Bund
# WID-SEC-2026-1048 fixes (file manipulation / security-control bypass /
# potential RCE, CVSS 8.6) — see #769. Refresh this pin at least quarterly
# and whenever a new Helm security advisory lands; the MCP tool shells
# out to this binary on every helm.* call, so every caller inherits the
# bundled CLI.
ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    HELM_VERSION=v3.20.2 \
    KUBECTL_VERSION=v1.31.0

# kubectl ships alongside helm in this image so diff_manifest (#1127)
# can compute a server-side diff of a raw manifest against the live
# cluster. The binary is only used for ``kubectl diff -f -`` — we do
# not shell out to kubectl for any mutating operation. Pin the
# version so rebuilds are reproducible; refresh in lockstep with
# apiserver compat windows.
RUN apt-get update && apt-get install -y --no-install-recommends \
      curl \
      ca-certificates \
    && curl -fsSL "https://get.helm.sh/helm-${HELM_VERSION}-linux-amd64.tar.gz" \
       | tar -xz -C /tmp \
    && mv /tmp/linux-amd64/helm /usr/local/bin/helm \
    && curl -fsSL -o /usr/local/bin/kubectl \
         "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/amd64/kubectl" \
    && chmod 0755 /usr/local/bin/kubectl \
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

# helm-diff plugin (#854, #947) — powers the `diff()` tool, which shows a
# unified diff of what `helm upgrade` WOULD change. Pinned to a
# known-good version AND sha256 to defend against a compromised upstream
# release tag (#947). The previous `helm plugin install <git-url>` flow
# resolved the tag at build time and would silently accept whatever
# content lived behind the tag; we now download the release tarball and
# verify its digest before installation. Bump both HELM_DIFF_VERSION and
# HELM_DIFF_SHA256 in lockstep, and always re-compute the digest from the
# upstream release artifact — never copy it from a downstream mirror.
ARG HELM_DIFF_VERSION=v3.9.13
ARG HELM_DIFF_SHA256=f8418ed04be8d69b35147040e18bc875964b3dd782c1fb6aaf321f667c09ae3f
USER root
RUN apt-get update && apt-get install -y --no-install-recommends curl ca-certificates \
    && curl -fsSL -o /tmp/helm-diff.tgz \
         "https://github.com/databus23/helm-diff/releases/download/${HELM_DIFF_VERSION}/helm-diff-linux-amd64.tgz" \
    && echo "${HELM_DIFF_SHA256}  /tmp/helm-diff.tgz" | sha256sum -c - \
    && mkdir -p /home/tool/.local/share/helm/plugins/helm-diff \
    && tar -xz -C /home/tool/.local/share/helm/plugins/helm-diff -f /tmp/helm-diff.tgz --strip-components=1 \
    && chown -R tool:tool /home/tool/.local \
    && rm -f /tmp/helm-diff.tgz \
    && apt-get purge -y curl \
    && apt-get autoremove -y \
    && rm -rf /var/lib/apt/lists/*
USER tool

COPY --chown=tool:tool tools/helm/ /home/tool/
# Shared helpers (#637, #643) — both mounted under /home/tool/shared so
# server.py can `from otel import ...` and
# `from metrics_server import start_metrics_server_in_thread`.
COPY --chown=tool:tool shared/otel.py /home/tool/shared/otel.py
COPY --chown=tool:tool shared/metrics_server.py /home/tool/shared/metrics_server.py
# Bearer-token auth middleware (#771) — same as mcp-kubernetes.
COPY --chown=tool:tool shared/mcp_auth.py /home/tool/shared/mcp_auth.py
# Prometheus tool-call metrics helper (#851). Imported opportunistically
# by server.py — degrades to no-op context manager when absent.
COPY --chown=tool:tool shared/mcp_metrics.py /home/tool/shared/mcp_metrics.py

# FastMCP streamable-http server (#644). Advertise the port so `docker run
# -P` and Kubernetes Service targetPort discovery work the same way they
# do for the agent containers. Override with MCP_PORT env var if needed.
EXPOSE 8000
# Dedicated Prometheus metrics listener (#643, #650). Served on 9000 by
# default; override with METRICS_PORT env var. Only active when
# METRICS_ENABLED is set.
EXPOSE 9000

CMD ["python", "/home/tool/server.py"]
