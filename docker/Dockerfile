# syntax=docker/dockerfile:1.7

# ==============================================================================
# Ion container image
#
# Multi-stage build:
#   1. web    — build the deterministic embedded web and terminal artifacts
#   2. build  — compile the static Go binary with the artifacts embedded
#   3. runtime — minimal Debian slim image running as a non-root user
#
# Build from the repository root:
#   docker build -f docker/Dockerfile -t ion:local .
# ==============================================================================

# ------------------------------------------------------------------------------
# Stage 1: operator client artifacts
# ------------------------------------------------------------------------------
FROM node:22.22-bookworm AS web

WORKDIR /src/ui
COPY ui/package.json ui/package-lock.json ./
COPY ui/shared/package.json ./shared/
COPY ui/web/package.json ./web/
COPY ui/tui/package.json ./tui/
RUN npm ci --ignore-scripts

COPY assets/ /src/assets/
COPY ui/ ./
RUN npm run build && npm run check:budgets

# ------------------------------------------------------------------------------
# Stage 2: Go binary with embedded artifacts
# ------------------------------------------------------------------------------
FROM golang:1.26-bookworm AS build

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_TIME=unknown

WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
# Bring in the freshly built operator artifacts so they are embedded in the binary.
COPY --from=web /src/ui/web/dist ./ui/web/dist
COPY --from=web /src/ui/tui/dist ./ui/tui/dist

RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.buildTime=${BUILD_TIME}" \
    -o /out/ion ./cmd/ion

# ------------------------------------------------------------------------------
# Stage 3: runtime
# ------------------------------------------------------------------------------
FROM debian:bookworm-slim AS runtime

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates tini \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 10001 ion \
    && useradd --uid 10001 --gid 10001 --create-home --home-dir /home/ion ion \
    && install -d -o ion -g ion -m 0700 /data

ENV ION_DATA_DIR=/data \
    ION_WEB_LISTEN=127.0.0.1:4174

COPY --from=build /out/ion /usr/local/bin/ion
COPY docker/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

USER 10001:10001
WORKDIR /home/ion
VOLUME ["/data"]

# The operator serves plain HTTP on loopback only. Publish it through a
# same-network-namespace TLS reverse proxy (see deploy/compose) or host
# networking (see docker/docker-compose.yml) rather than a bridge port map.
EXPOSE 4174

ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/entrypoint.sh"]
CMD ["dashboard"]

LABEL org.opencontainers.image.title="Ion" \
      org.opencontainers.image.description="Persistent general agent runtime from MatrixMCL" \
      org.opencontainers.image.url="https://ion.matrixmcl.com" \
      org.opencontainers.image.source="https://github.com/paxlabs-inc/ion-agent" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.vendor="MatrixMCL"
