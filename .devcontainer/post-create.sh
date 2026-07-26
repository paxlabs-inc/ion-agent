#!/usr/bin/env bash
# Provision developer tooling that is pinned to specific versions.
set -euo pipefail

echo "==> Verifying toolchains"
go version
node --version
npm --version
rustc --version || true

echo "==> Installing golangci-lint 2.12.2"
GOBIN="$(go env GOPATH)/bin"
curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh \
    | sh -s -- -b "${GOBIN}" v2.12.2

echo "==> Installing air (live reload)"
go install github.com/air-verse/air@latest

echo "==> Downloading Go modules"
go mod download

echo "==> Installing UI workspace dependencies"
if [ -d ui ]; then
    (cd ui && npm ci --ignore-scripts)
    echo "==> Installing Playwright Chromium"
    (cd ui && npx playwright install chromium) || true
fi

echo
echo "Dev container ready. Common commands:"
echo "  make build      # build the operator release"
echo "  make run        # run the web operator on http://127.0.0.1:4174"
echo "  make ci         # full CI pipeline"
