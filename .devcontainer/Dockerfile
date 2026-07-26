# Base image for the Ion dev container.
# Go, Node, and Rust toolchains are added by devcontainer features; this image
# provides the system packages the build and browser acceptance tests need.
FROM mcr.microsoft.com/devcontainers/base:bookworm

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        build-essential \
        ca-certificates \
        chromium \
        curl \
        git \
        jq \
        pkg-config \
        sqlite3 \
    && rm -rf /var/lib/apt/lists/*

# Chromium for browser acceptance tests (Playwright can also manage its own).
ENV CHROME_BIN=/usr/bin/chromium
