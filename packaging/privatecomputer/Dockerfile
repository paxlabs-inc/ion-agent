FROM golang@sha256:1ecb7edf62a0408027bd5729dfd6b1b8766e578e8df93995b225dfd0944eb651 AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ion-computer ./cmd/ion-computer
COPY internal/privatecomputer ./internal/privatecomputer
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ion-computer ./cmd/ion-computer

FROM debian@sha256:fac46bff2e02f51425b6e33b0e1169f55dfb053d83511ca28aa50c09fd5ed7a4

ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        chromium \
        fonts-dejavu-core \
        openbox \
        python3 \
        python3-pip \
        scrot \
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
    && rm -rf /tmp/cua-driver /var/lib/apt/lists/*
RUN groupadd --gid 10001 ion \
    && useradd --uid 10001 --gid 10001 --create-home --home-dir /home/ion ion \
    && install -d -o ion -g ion -m 0700 /home/ion/.cache /home/ion/.config

COPY --from=build /out/ion-computer /usr/local/bin/ion-computer
COPY packaging/privatecomputer/cua_bridge.py /usr/local/libexec/ion-cua-bridge.py

USER 10001:10001
WORKDIR /home/ion
EXPOSE 8081
ENTRYPOINT ["/usr/local/bin/ion-computer"]
