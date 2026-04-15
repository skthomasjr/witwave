FROM registry.k8s.io/git-sync/git-sync:v4.4.0 AS gitsync
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends git rsync && rm -rf /var/lib/apt/lists/*
COPY --from=gitsync /git-sync /git-sync
ENTRYPOINT ["/git-sync"]
