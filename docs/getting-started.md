# Getting started

This guide takes you from a clean checkout to a running Ion web operator.

## 1. Prerequisites

| Tool | Version |
|---|---|
| Go | 1.26.5 |
| Node.js | 22.22+ (Node 22 line) |
| npm | 11 |
| Rust | 1.78.0 (optional HNSW service) |
| Chromium | for native browser acceptance tests |

If you prefer not to install these locally, use the
[dev container](../.devcontainer/) or [Docker](../docker/).

## 2. Clone and build

```bash
git clone https://github.com/paxlabs-inc/ion-agent.git
cd ion-agent
make build
```

`make build` compiles the deterministic web and terminal artifacts and embeds
them into `bin/ion`.

## 3. Initialize

Production initialization uses the host's protected key source:

```bash
./bin/ion init
```

On a headless development machine without a supported key source, opt into the
development-only file KEK:

```bash
./bin/ion init --dev-file-kek
```

> The development file KEK is not a production deployment mechanism.

## 4. Run

```bash
# Web operator (http://127.0.0.1:4174)
./bin/ion dashboard

# Terminal operator with a supervised local runtime
./bin/ion tui

# Attach the terminal operator to a running dashboard
./bin/ion tui --attach
```

Open <http://127.0.0.1:4174>. Plain HTTP is loopback-only; remote access
requires an operator-managed TLS reverse proxy.

## 5. Verify your setup

```bash
make test-unit
make vet
make spec-validate
```

## Next steps

- [Configuration](configuration.md)
- [Deployment](deployment.md)
- [Operator guide](operator.md)
