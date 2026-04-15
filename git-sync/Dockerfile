FROM registry.k8s.io/git-sync/git-sync:v4.4.0
USER root
RUN apt-get update && apt-get install -y --no-install-recommends rsync && rm -rf /var/lib/apt/lists/*
USER 65533
