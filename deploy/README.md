# Deploying Ion

Production-oriented deployment assets for Ion. Read this page and
[SECURITY.md](../SECURITY.md) before exposing Ion beyond loopback.

## The one rule that shapes every deployment

Ion's web operator serves **plain HTTP on loopback only**. This is enforced in
the binary: a non-loopback plain-HTTP listen address is rejected. Every remote
deployment therefore terminates TLS in a proxy that reaches Ion over loopback:

- **Compose / bare metal:** a proxy in the same network namespace (or on the
  same host) forwards to `127.0.0.1:4174`.
- **Kubernetes:** a proxy sidecar in the same Pod forwards to `127.0.0.1:4174`;
  the Ingress terminates TLS.

A standard Docker bridge port map (`-p 4174:4174`) cannot reach a loopback-bound
listener and will not work.

Every remote deployment must also set:

- `ION_WEB_ORIGIN` to the exact public HTTPS origin.
- `ION_AUTH_USERNAME` to the operator username.
- Exactly one of `ION_AUTH_PASSWORD` or `ION_AUTH_PASSWORD_HASH`.

Ion refuses partial credentials, remote unauthenticated origins, plain-HTTP
remote origins, and Railway environments without deployment authentication.
Keep passwords in the platform's protected or sealed variable store. Do not
commit populated environment files.

## Options

| Target | Path | Best for |
|---|---|---|
| Docker Compose (TLS) | [`compose/`](compose/) | A single host with automatic HTTPS via Caddy. |
| Kubernetes (Kustomize) | [`kubernetes/`](kubernetes/) | Clusters; plain manifests you can read and edit. |
| Helm chart | [`helm/ion/`](helm/ion/) | Clusters; parameterized, repeatable installs. |
| systemd | [`systemd/`](systemd/) | Bare-metal Linux hosts. |

For a local, non-TLS developer stack on Linux, use
[`docker/docker-compose.yml`](../docker/docker-compose.yml) instead.

## Quick starts

Docker Compose with TLS:

```bash
cd deploy/compose
cp .env.example .env      # set site, origin, username, and one password
docker compose run --rm ion init
docker compose up -d
```

Kubernetes with Kustomize:

```bash
kubectl apply -k deploy/kubernetes
```

Helm:

```bash
kubectl create secret generic ion-operator-auth \
  --from-literal=username=operator \
  --from-literal=password='replace-with-a-generated-password'
helm install ion deploy/helm/ion \
  --namespace ion --create-namespace \
  --set ion.origin=https://ion.example.com \
  --set ion.auth.existingSecret=ion-operator-auth \
  --set ingress.enabled=true \
  --set ingress.hosts[0].host=ion.example.com
```

## Railway variables

Railway uses the root `Dockerfile` and root `railway.toml` to run Ion, Ion
Computer, and ONLYOFFICE as one service. Attach one volume at `/data` and define
`ION_AUTH_USERNAME`, one of `ION_AUTH_PASSWORD` or
`ION_AUTH_PASSWORD_HASH`, and a base64-encoded 32-byte `ION_VAULT_KEK` as
protected variables. The appliance derives `ION_WEB_ORIGIN` from
`RAILWAY_PUBLIC_DOMAIN` and generates its internal Computer and Office
credentials. See [`deploy/railway`](railway/) for the complete setup.

## Operator responsibilities

- **Key source.** Choose and protect the vault key source. The development file
  KEK is not a production mechanism.
- **TLS.** Provision certificates and enforce HTTPS at the proxy or ingress.
- **Persistence.** Ion is a single persistent identity backed by a
  `ReadWriteOnce` volume; run exactly one instance.
- **Network egress.** Ion applies SSRF and private-network controls, but you
  remain responsible for host and cluster egress policy.
- **Backups.** Back up the data directory / volume; it holds encrypted session,
  memory, work, and audit state.
