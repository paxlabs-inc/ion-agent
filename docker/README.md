# Ion container image

This directory builds and runs Ion as a container.

- [`Dockerfile`](Dockerfile) — multi-stage build (operator artifacts → static Go
  binary → minimal Debian runtime, non-root).
- [`entrypoint.sh`](entrypoint.sh) — optional first-run init, then runs the
  requested subcommand (defaults to `dashboard`).
- [`docker-compose.yml`](docker-compose.yml) — local single-host stack using host
  networking.
- [`.env.example`](.env.example) — copy to `.env` and adjust.

## Build

From the repository root:

```bash
docker build -f docker/Dockerfile -t ion:local .
```

Or with Compose:

```bash
docker compose -f docker/docker-compose.yml build
```

## Run locally

```bash
docker compose -f docker/docker-compose.yml up
# open http://127.0.0.1:4174
```

The stack uses host networking so the container's loopback listener is reachable
on the host.

## Why host networking?

Ion's web operator serves **plain HTTP on loopback only** (enforced in the
binary). A normal Docker bridge port map (`-p 4174:4174`) cannot reach a process
bound to `127.0.0.1` inside the container, so it would not work. Two supported
patterns:

| Pattern | Where | Use case |
|---|---|---|
| Host networking | this `docker-compose.yml` | Local development on a Linux host. |
| Same-namespace TLS reverse proxy | [`deploy/compose`](../deploy/compose/) | Production-like access, including remote, with TLS termination. |

On **Docker Desktop (macOS/Windows)**, host networking does not expose the
container's loopback to the host the same way it does on Linux. Use the
[`deploy/compose`](../deploy/compose/) TLS stack, which runs a reverse proxy in
the same network namespace as Ion and publishes an HTTPS port.

## First-run initialization

`entrypoint.sh` initializes the data directory only when `ION_AUTO_INIT=1` and
the directory looks empty. The compose file enables this together with
`ION_DEV_FILE_KEK=1` for convenience.

> The development file KEK is **not** a production mechanism. For production, set
> `ION_AUTO_INIT=0`, mount a persistent data volume, and run
> `docker compose run --rm ion init` against a host-protected key source.

## Environment variables

See [`.env.example`](.env.example). The most important are `ION_DATA_DIR`,
`ION_WEB_LISTEN`, `ION_AUTO_INIT`, and `ION_DEV_FILE_KEK`.

## Image details

- Runs as UID/GID `10001:10001`.
- Persistent state lives in the `/data` volume.
- Built `CGO_ENABLED=0`; the SQLite driver is pure Go, so no libc is required at
  runtime beyond `ca-certificates`.
