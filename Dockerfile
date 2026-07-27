# syntax=docker/dockerfile:1.7

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

FROM golang:1.26-bookworm AS build

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_TIME=unknown

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/ui/web/dist ./ui/web/dist
COPY --from=web /src/ui/tui/dist ./ui/tui/dist
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.buildTime=${BUILD_TIME}" \
      -o /out/ion ./cmd/ion \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" \
      -o /out/ion-computer ./cmd/ion-computer \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" \
      -o /out/ion-appliance ./cmd/ion-appliance

FROM debian@sha256:7b140f374b289a7c2befc338f42ebe6441b7ea838a042bbd5acbfca6ec875818 AS computer-runtime

RUN apt-get update \
    && apt-get install -y --no-install-recommends chromium chromium-sandbox pax-utils \
    && install -d /out/chromium-libs \
    && cp -a /usr/lib/chromium /out/chromium \
    && lddtree -l /usr/lib/chromium/chromium \
      | sort -u \
      | while read -r dependency; do \
          if [ -f "${dependency}" ]; then \
            cp -L "${dependency}" /out/chromium-libs/; \
          fi; \
        done \
    && rm -rf /var/lib/apt/lists/*

FROM onlyoffice/documentserver:9.2.0.1@sha256:a2990166ef19f780fd1a287f174084f967f3d4e97ccfd340229c05238e68006d AS appliance-root

ARG TARGETARCH
RUN test "${TARGETARCH}" = "amd64" \
    && apt-get update \
    && apt-get install -y --no-install-recommends \
      ca-certificates \
      fonts-dejavu-core \
      openbox \
      python3 \
      python3-pip \
      scrot \
      tini \
      xdotool \
      xterm \
      xvfb \
    && python3 -m pip download \
      --no-deps \
      --dest /tmp/cua-driver \
      cua-driver==0.12.5 \
    && echo "1abf9ecefbb22e9c258b4a7b1a00b5314d34c14d46f17bdcb428a75ad2512bb8  /tmp/cua-driver/cua_driver-0.12.5-py3-none-manylinux_2_31_x86_64.whl" \
      | sha256sum -c - \
    && python3 -m pip install \
      --break-system-packages \
      --no-cache-dir \
      --no-deps \
      /tmp/cua-driver/cua_driver-0.12.5-py3-none-manylinux_2_31_x86_64.whl \
    && rm -rf /tmp/cua-driver /var/lib/apt/lists/* \
    && groupadd --gid 10001 ion \
    && useradd --uid 10001 --gid 10001 --create-home --home-dir /home/ion ion \
    && groupadd --gid 10002 ioncomputer \
    && useradd --uid 10002 --gid 10002 --create-home --home-dir /home/ioncomputer ioncomputer \
    && install -d -o root -g root -m 0700 /data \
    && sed -ri 's/listen[[:space:]]+80;/listen 127.0.0.1:80;/' \
      /etc/onlyoffice/documentserver/nginx/ds.conf.tmpl \
    && sed -ri '/listen[[:space:]]+\[::\]:80;/d' \
      /etc/onlyoffice/documentserver/nginx/ds.conf.tmpl

COPY --from=build /out/ion /usr/local/bin/ion
COPY --from=build /out/ion-computer /usr/local/bin/ion-computer
COPY --from=build /out/ion-appliance /usr/local/bin/ion-appliance
COPY --from=computer-runtime /out/chromium /opt/ion/chromium
COPY --from=computer-runtime /out/chromium-libs /opt/ion/chromium-libs
COPY packaging/appliance/chromium /usr/local/bin/chromium
COPY packaging/privatecomputer/cua_bridge.py /usr/local/libexec/ion-cua-bridge.py
RUN for dependency in /opt/ion/chromium-libs/*; do \
      name="$(basename "${dependency}")"; \
      if [ -e "/lib/x86_64-linux-gnu/${name}" ] \
        || [ -e "/usr/lib/x86_64-linux-gnu/${name}" ] \
        || [ -e "/lib64/${name}" ]; then \
        rm -f "${dependency}"; \
      fi; \
    done \
    && chmod 4755 /opt/ion/chromium/chrome-sandbox \
    && chmod 0555 \
    /usr/local/bin/ion \
    /usr/local/bin/ion-computer \
    /usr/local/bin/ion-appliance \
    /usr/local/bin/chromium \
    /usr/local/libexec/ion-cua-bridge.py

FROM scratch

COPY --from=appliance-root / /

ENV ION_APPLIANCE_DATA_ROOT=/data \
    ION_AUTO_INIT=1 \
    ION_COMPUTER_BROWSER_CONTAINMENT=appliance_boundary \
    ION_COMPUTER_MODE=personal \
    ION_OFFICE_ENABLED=true \
    ION_OFFICE_INTERNAL_URL=http://127.0.0.1:80 \
    ION_OFFICE_PUBLIC_PATH=/office-engine/ \
    PORT=8080

EXPOSE 8080
STOPSIGNAL SIGTERM
HEALTHCHECK --interval=30s --timeout=10s --start-period=10m --retries=3 \
  CMD curl --fail --silent --show-error http://127.0.0.1:${PORT}/readyz || exit 1

ENTRYPOINT ["/usr/bin/tini", "-g", "--", "/usr/local/bin/ion-appliance"]

LABEL org.opencontainers.image.title="Ion appliance" \
      org.opencontainers.image.description="Ion, Ion Computer, and ONLYOFFICE in one Railway service" \
      org.opencontainers.image.source="https://github.com/paxlabs-inc/ion-agent" \
      org.opencontainers.image.licenses="MIT AND AGPL-3.0-only" \
      org.opencontainers.image.vendor="MatrixMCL"
