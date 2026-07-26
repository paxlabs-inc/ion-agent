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

For a Railway service or template, define `ION_AUTH_USERNAME` and
`ION_AUTH_PASSWORD` as deployment variables, seal the password, and set
`ION_WEB_ORIGIN` to `https://${{RAILWAY_PUBLIC_DOMAIN}}`. Railway's injected
environment identifiers make Ion fail closed if authentication is absent.
Template authors should use a generated value for the password rather than a
literal default. The variables are runtime inputs only; they are never compiled
into the web bundle.

Office Studio on Railway requires a second private ONLYOFFICE service plus
`ION_OFFICE_ENABLED=true`, `ION_OFFICE_INTERNAL_URL`,
`ION_OFFICE_CALLBACK_ORIGIN`, and one shared sealed
`ION_OFFICE_JWT_SECRET`. ONLYOFFICE must not receive a public TCP route. The
callback origin remains Ion's public HTTPS origin so the engine can fetch
short-lived source URLs and send signed save callbacks.

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
