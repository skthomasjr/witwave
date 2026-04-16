# Multi-stage build for the nyx dashboard (#470).
# Stage 1 compiles the Vite bundle; stage 2 serves the static artefact via
# nginx with a /api/* reverse proxy to the harness service. This mirrors how
# the vite dev server proxies in development (see vite.config.ts) so the
# dashboard has one network model for dev and prod.

FROM node:20-bookworm-slim AS build
WORKDIR /build
COPY dashboard/package.json ./
RUN npm install --no-audit --no-fund --ignore-scripts
COPY dashboard/ ./
RUN npm run build

FROM nginxinc/nginx-unprivileged:1.27-bookworm
# nginx-unprivileged runs as uid 101 (nginx) and binds on 8080 by default,
# so the pod runs with runAsNonRoot: true without capability tricks.
# Writeable runtime paths (/tmp, /var/cache/nginx) are already owned by
# the nginx user in the base image.
COPY --chown=nginx:nginx dashboard/nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build --chown=nginx:nginx /build/dist /usr/share/nginx/html
EXPOSE 8080
CMD ["nginx", "-g", "daemon off;"]
