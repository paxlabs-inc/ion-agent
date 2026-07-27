# Railway

Railway runs the complete product as one service from the repository root:

- Build: `/Dockerfile`
- Configuration: `/railway.toml`
- Public port: Railway's injected `PORT`
- Health check: `/readyz`
- Volume: one volume mounted at `/data`

The image contains and supervises Ion, Ion Computer, Chromium, and ONLYOFFICE.
There are no companion Railway services and no public Computer or document
server ports.

## Create the service

1. Create one Railway service from this repository.
2. Attach one volume at `/data`.
3. Generate a public domain.
4. Add the required variables below.

```text
ION_AUTH_USERNAME=operator
ION_AUTH_PASSWORD=<generated password>
ION_VAULT_KEK=<base64 encoded 32-byte key>
```

Generate the vault KEK locally with:

```bash
openssl rand -base64 32
```

`ION_WEB_ORIGIN` is derived automatically from `RAILWAY_PUBLIC_DOMAIN`.
The appliance generates and persists its own Computer authentication key,
Office JWT secret, and Computer host identity.

Provider variables such as `PROVIDER_API_KEY`, `LLM_MODEL`, `TAVILY_API_KEY`,
and `NOVITA_API_KEY` remain optional runtime inputs.

## Storage

The single `/data` volume contains:

```text
/data/ion
/data/computer
/data/appliance
```

Run one replica because Ion's encrypted databases and Personal Computer state
are single-writer. Back up `/data` together with the separately protected
`ION_VAULT_KEK`; neither is sufficient to decrypt Ion state alone.

ONLYOFFICE operational caches are disposable. Ion owns the encrypted canonical
document versions and reconciles editor sessions after an appliance restart.

## Capacity

The appliance combines ONLYOFFICE's document engine with a graphical Chromium
desktop. Start with at least 8 GB RAM, 4 vCPU, and a 40 GB volume, then size from
observed readiness, document conversion, and Computer workload measurements.

Railway's container boundary is the browser containment boundary for this
single-service layout. Chromium runs as the dedicated, non-root Computer UID
with its daemon credential consumed from a private file; its own namespace
sandbox is disabled because nested namespaces are unavailable on Railway.

Do not add public TCP routes. The only public route is the Railway domain
targeting the injected `PORT`.
