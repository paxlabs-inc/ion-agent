# Installation

Ion runs as a single local `ion` process. Choose the method that fits your
environment.

## From source

The canonical method. Produces `bin/ion` with embedded operator artifacts.

```bash
git clone https://github.com/paxlabs-inc/ion-agent.git
cd ion-agent
make build          # web + TUI + Go binary
# or
make build-all      # also build the Rust HNSW sidecar
```

Requirements: Go 1.26.5, Node.js 22.22+, npm 11, and (for the sidecar) Rust
1.78.0. See [getting started](getting-started.md).

## Docker

```bash
docker build -t ion:local .
```

Or run the local stack:

```bash
docker compose -f docker/docker-compose.yml up --build
```

The root image contains Ion, Ion Computer, and ONLYOFFICE. See
[docker/README.md](../docker/README.md).

## Kubernetes and Helm

For clusters, use the [Kubernetes manifests](../deploy/kubernetes/) or the
[Helm chart](../deploy/helm/ion/). See [deployment](deployment.md).

## systemd (bare metal)

Install the binary and run it as a hardened service. See
[deploy/systemd/README.md](../deploy/systemd/README.md).

## Verifying a build

```bash
./bin/ion version
```

This prints the version, commit, and build time embedded at link time.

## Uninstalling

Ion keeps all state under its data directory (default `~/.ion`, or the
`--data-dir` you chose). Remove the binary and the data directory to uninstall.
The data directory contains encrypted session, memory, work, and audit state —
back it up first if you may need it.
