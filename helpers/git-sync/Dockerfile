FROM registry.k8s.io/git-sync/git-sync:v4.4.0 AS gitsync
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates git rsync && rm -rf /var/lib/apt/lists/*
COPY --from=gitsync /git-sync /git-sync
COPY --from=gitsync /etc/passwd /etc/passwd
COPY --from=gitsync /etc/group /etc/group
# HOME defaults to /tmp at image level so the binary is runnable standalone
# (local `docker run` smoke tests), but the chart overrides HOME to
# /git/.home via an env entry in the pod spec (#1265). Doing both avoids
# sharing the pod-wide `witwave-tmp` emptyDir with application containers —
# git-sync's /tmp is mounted from a dedicated `witwave-tmp-gitsync` volume by
# charts/witwave/templates/deployment.yaml.
ENV HOME=/tmp
USER 65533
ENTRYPOINT ["/git-sync"]
