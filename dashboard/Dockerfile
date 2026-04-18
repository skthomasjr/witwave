# Multi-stage build for the nyx dashboard (#470).
# Stage 1 compiles the Vite bundle; stage 2 serves the static artefact via
# nginx with a /api/* reverse proxy to the harness service. This mirrors how
# the vite dev server proxies in development (see vite.config.ts) so the
# dashboard has one network model for dev and prod.

FROM node:20-bookworm-slim AS build
WORKDIR /build
# Copy the package.json and the checked-in lockfile together so `npm ci`
# can run. Using `npm ci` instead of `npm install` (#741) enforces the
# package-lock.json resolution verbatim — no caret-range drift into the
# v-html render path (marked/dompurify/primevue/vue are the risky ones).
# `--ignore-scripts` keeps install-time postinstall hooks from executing
# in the build stage; `--no-audit --no-fund` just trims noise.
COPY dashboard/package.json dashboard/package-lock.json ./
RUN npm ci --no-audit --no-fund --ignore-scripts
COPY dashboard/ ./
RUN npm run build

FROM nginxinc/nginx-unprivileged:1.27-bookworm
# nginx-unprivileged runs as uid 101 (nginx) and can bind any non-privileged
# port (>=1024). We listen on 3000 — the de facto "web dashboard" port —
# so the pod runs with runAsNonRoot: true without capability tricks and
# the default doesn't collide with the 80xx backend range.
# Writeable runtime paths (/tmp, /var/cache/nginx) are already owned by
# the nginx user in the base image.
# The image ships a baseline default.conf.template that serves the static
# SPA and returns 404 for /api/*. In Kubernetes the Helm chart overrides
# this with a ConfigMap that adds per-agent /api/agents/<name>/ routes
# templated from .Values.agents (see charts/nyx/templates/
# configmap-dashboard-nginx.yaml).
COPY --chown=nginx:nginx dashboard/nginx.conf /etc/nginx/templates/default.conf.template
COPY --from=build --chown=nginx:nginx /build/dist /usr/share/nginx/html
EXPOSE 3000
CMD ["nginx", "-g", "daemon off;"]
