# Ion appliance

The root [`Dockerfile`](../Dockerfile) builds one image containing Ion, Ion
Computer, Chromium, and ONLYOFFICE. The container exposes one Ion ingress and
supervises every internal process.

## Build

```bash
docker build -t ion:local .
```

## Run locally

```bash
docker compose -f docker/docker-compose.yml up --build
```

Open `http://127.0.0.1:8080`.

The local stack mounts one volume at `/data`, publishes one loopback port, and
uses the explicit development file KEK. Production must supply
`ION_VAULT_KEK` instead.

## Runtime layout

```text
/data/ion        encrypted Ion state and canonical Office versions
/data/computer   Personal Computer home, state, and workspaces
/data/appliance  generated internal service credentials and host identity
```

The appliance generates its internal Computer authentication key, Office JWT
secret, and Computer host identity. Remote deployments must provide the exact
public origin, operator credentials, and a base64-encoded 32-byte
`ION_VAULT_KEK`.

Chromium runs as the dedicated non-root Computer UID. In this single-container
layout it uses the appliance container as its containment boundary because
ordinary Docker and Railway runtimes do not allow Chromium to create a nested
namespace sandbox.
