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

CMD ["python", "/home/tool/server.py"]
