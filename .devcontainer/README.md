# Ion dev container

A ready-to-code environment with the exact toolchains Ion targets: Go 1.26.5,
Node 22.22, Rust 1.78.0, plus Chromium, `golangci-lint` 2.12.2, and `air`.

## Use it

- **VS Code:** open the repo and run **Dev Containers: Reopen in Container**.
- **GitHub Codespaces:** create a codespace on the repository.

On first start, [`post-create.sh`](post-create.sh) installs pinned tooling,
downloads Go modules, and installs the UI workspace dependencies and Playwright
Chromium.

## After it starts

```bash
make build      # build the operator release (web + TUI + Go binary)
make run        # run the web operator on http://127.0.0.1:4174
make ci         # format, vet, lint, test, build
```

Port `4174` is forwarded automatically. The container writes its data directory
to `.ion/` inside the workspace and uses the development file KEK path where a
host-protected key source is unavailable.

## Files

- [`devcontainer.json`](devcontainer.json) — container definition, features,
  ports, and VS Code customizations.
- [`Dockerfile`](Dockerfile) — base image and system packages.
- [`post-create.sh`](post-create.sh) — one-time provisioning.
