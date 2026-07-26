# Troubleshooting

## `plain HTTP may only listen on loopback`

Ion rejects a non-loopback plain-HTTP listen address by design.

- Keep `--listen` on `127.0.0.1` (or another loopback address).
- For remote access, terminate TLS in a reverse proxy that forwards to
  `127.0.0.1:4174`. See [deployment](deployment.md).

## I mapped a container port but cannot reach the operator

A Docker bridge port map cannot reach a process bound to `127.0.0.1` inside the
container. Use host networking (local Linux, see
[docker/README.md](../docker/README.md)) or a same-network-namespace proxy
(production, see [deploy/](../deploy/)).

## The build fails in the UI step

`make build` runs `npm ci` and a deterministic UI build. Ensure:

- Node.js is on the 22.22+ (Node 22) line and npm is version 11.
- You ran the build from the repository root.
- The generated protocol is in sync (`cd ui && npm run check:generated`).

## `init` cannot find a protected key source

On a machine without a supported host key source (for example, a headless
container), initialize with the explicit development fallback:

```bash
./bin/ion init --dev-file-kek
```

Do not use the development file KEK in production.

## Tests fail with a race or timeout

Unit tests run with the race detector and a timeout:

```bash
make test-unit
```

If a single package is slow, run it directly with `-run` to narrow the scope.
Do not disable the race detector; it is part of the acceptance bar.

## An operation reports "unavailable"

Ion projects subsystems it cannot back with a real implementation as
**unavailable** rather than inventing data. If a capability is unavailable, its
runtime implementation is not wired in this build. This is expected for
capabilities behind acceptance boundaries in
[`spec/ion_spec/spec.kvx`](../spec/ion_spec/spec.kvx).

## Where are the logs and data?

State lives under the data directory (`--data-dir`, default `~/.ion`). Under
systemd, see `journalctl -u ion.service`. Under Docker, see
`docker compose logs`.

## Still stuck?

Search [issues](https://github.com/paxlabs-inc/ion-agent/issues) and
[discussions](https://github.com/paxlabs-inc/ion-agent/discussions), then open a
[bug report](https://github.com/paxlabs-inc/ion-agent/issues/new?template=bug_report.yml)
with your `ion version`, OS, and full error output. See [SUPPORT.md](../SUPPORT.md).
