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

FROM nginx:1.27-bookworm
COPY dashboard/nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /build/dist /usr/share/nginx/html
EXPOSE 8080
# The nginx image runs as root by default but drops privileges to the
# `nginx` user for worker processes. Binding to 8080 (not 80) avoids needing
# NET_BIND_SERVICE capabilities when the pod's securityContext runs the
# container as a non-root UID.
CMD ["nginx", "-g", "daemon off;"]
