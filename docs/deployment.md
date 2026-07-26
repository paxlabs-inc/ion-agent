# Deployment

This page summarizes how to run Ion beyond your workstation. The deployment
assets live in [`deploy/`](../deploy/) and [`docker/`](../docker/).

## The constraint that shapes everything

Ion's web operator serves **plain HTTP on loopback only**, enforced in the
binary. A non-loopback plain-HTTP listen address is rejected. Every remote
deployment therefore terminates TLS in a proxy that reaches Ion over loopback.

A Docker bridge port map (`-p 4174:4174`) cannot reach a process bound to
`127.0.0.1` inside the container and will not work. Use one of the supported
patterns below.

## Single host with TLS (Docker Compose)

A Caddy proxy runs in Ion's network namespace, terminates TLS, and forwards to
`127.0.0.1:4174`.

```bash
cd deploy/compose
cp .env.example .env      # set ION_SITE_ADDRESS to your domain or "localhost"
docker compose run --rm ion init
docker compose up -d
```

See [deploy/compose](../deploy/compose/).

## Kubernetes

A Caddy sidecar in the same Pod forwards to `127.0.0.1:4174`; the Ingress
terminates TLS. Ion is a single persistent identity backed by a `ReadWriteOnce`
volume, so the Deployment runs one replica with the `Recreate` strategy.

```bash
kubectl apply -k deploy/kubernetes
```

See [deploy/kubernetes](../deploy/kubernetes/).

## Helm

```bash
helm install ion deploy/helm/ion \
  --namespace ion --create-namespace \
  --set ingress.enabled=true \
  --set ingress.hosts[0].host=ion.example.com \
  --set ingress.tls[0].secretName=ion-tls \
  --set ingress.tls[0].hosts[0]=ion.example.com
```

See [deploy/helm/ion](../deploy/helm/ion/).

## Bare metal (systemd)

Run Ion as a hardened systemd service and put a reverse proxy in front for TLS.
See [deploy/systemd](../deploy/systemd/).

## Local development stack

For a non-TLS local stack on Linux, use host networking:

```bash
docker compose -f docker/docker-compose.yml up --build
# open http://127.0.0.1:4174
```

See [docker/README.md](../docker/README.md).

## Operator responsibilities

- Provision and protect the vault key source (not the development file KEK).
- Provision TLS certificates and enforce HTTPS.
- Run exactly one instance against the persistent volume.
- Back up the data directory / volume regularly.
- Control host and cluster network egress.

Read [SECURITY.md](../SECURITY.md) before exposing Ion to a network.
