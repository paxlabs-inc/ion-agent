# Railway template

The Railway template uses three dedicated services. Only `ion` is public.
`ion-computer` and `onlyoffice` communicate with Ion over Railway private
networking and must not receive public domains or TCP proxies.

## Published images

The Docker workflow publishes these images on pushes to the default branch and
on version tags:

| Service | Image | Platforms |
| --- | --- | --- |
| `ion` | `ghcr.io/paxlabs-inc/ion-agent` | `linux/amd64`, `linux/arm64` |
| `ion-computer` | `ghcr.io/paxlabs-inc/ion-computer` | `linux/amd64` |

Each image receives a branch tag, a short commit tag, semantic-version tags
when applicable, and `latest` on the default branch. The template uses the
upstream version-pinned `onlyoffice/documentserver:9.2.0.1` image directly
instead of republishing third-party binaries through GHCR.

After the first successful workflow run, set both GHCR packages to public in
the organization package settings so Railway can pull them without registry
credentials.

## Template service sources

Create `ion` and `ion-computer` from
`https://github.com/paxlabs-inc/ion-agent/tree/main`, then set each custom
configuration path:

| Service | Source | Config path | Private port | Public |
| --- | --- | --- | --- | --- |
| `ion` | GitHub | `/deploy/railway/ion/railway.toml` | `8080` | HTTP domain |
| `ion-computer` | GitHub | `/deploy/railway/ion-computer/railway.toml` | `8081` | No |
| `onlyoffice` | `onlyoffice/documentserver:9.2.0.1` | Settings mirror: `/deploy/railway/onlyoffice/railway.toml` | `80` | No |

The same topology can use the published GHCR images instead, but Railway does
not read repository config-as-code files for image-backed services. When using
GHCR or ONLYOFFICE image sources, copy the health and restart settings from the
corresponding `railway.toml` into the template editor.

## Volumes

Attach one required volume to `ion` at `/data`. Run exactly one Ion replica
because this is a single-writer volume.

For Personal Computer persistence, attach an optional second volume to
`ion-computer` at `/home/ion/.ion-computer`. Clean Computer mode does not use a
persistent workspace volume. ONLYOFFICE is stateless in this topology because
Ion owns the canonical encrypted document versions.

## Variables

Add these exact variables to `ion`:

```text
PORT=8080
ION_DATA_DIR=/data
ION_WEB_LISTEN=[::]:8080
ION_WEB_ORIGIN=https://${{RAILWAY_PUBLIC_DOMAIN}}
ION_AUTH_USERNAME=operator
ION_AUTH_PASSWORD=${{secret(32)}}
ION_COMPUTER_URL=http://${{ion-computer.RAILWAY_PRIVATE_DOMAIN}}:8081
ION_COMPUTER_AUTH_KEY=${{secret(64)}}
ION_OFFICE_ENABLED=true
ION_OFFICE_INTERNAL_URL=http://${{onlyoffice.RAILWAY_PRIVATE_DOMAIN}}:80
ION_OFFICE_PUBLIC_PATH=/office-engine/
ION_OFFICE_CALLBACK_ORIGIN=https://${{RAILWAY_PUBLIC_DOMAIN}}
ION_OFFICE_JWT_SECRET=${{secret(64)}}
ION_OFFICE_MAX_FILE_BYTES=104857600
ION_OFFICE_MAX_VERSIONS=100
ION_AUTO_INIT=0
TAVILY_API_KEY=
```

Add these exact variables to `ion-computer`:

```text
PORT=8081
ION_COMPUTER_LISTEN=[::]:8081
ION_COMPUTER_AUTH_KEY=${{ion.ION_COMPUTER_AUTH_KEY}}
ION_COMPUTER_HOST_ID=${{secret(8, "0123456789abcdef")}}-${{secret(4, "0123456789abcdef")}}-4${{secret(3, "0123456789abcdef")}}-${{secret(1, "89ab")}}${{secret(3, "0123456789abcdef")}}-${{secret(12, "0123456789abcdef")}}
ION_COMPUTER_MODE=personal
ION_COMPUTER_HOME=/home/ion
ION_COMPUTER_STATE_ROOT=/home/ion/.ion-computer/state
ION_COMPUTER_WORKSPACE_ROOT=/home/ion/.ion-computer/workspaces
ION_COMPUTER_BROWSER_CONTAINMENT=sandboxed
```

Add these exact variables to `onlyoffice`:

```text
PORT=80
JWT_ENABLED=true
JWT_SECRET=${{ion.ION_OFFICE_JWT_SECRET}}
JWT_HEADER=Authorization
JWT_IN_BODY=true
```

Set the Ion public networking target port to `8080`. Do not enable public
networking for the other services.

## Current release gate

Do not publish the one-click template yet. The current Ion runtime still
requires a production-safe Railway vault key source, Railway-compatible
non-loopback binding, and non-root volume ownership initialization. Keeping
`ION_AUTO_INIT=0` makes that limitation explicit rather than silently using the
development file KEK. Those runtime changes must land before a fresh template
deployment can pass onboarding.
