# Ion Office Studio

Ion Office Studio provides an encrypted document library and integrated DOCX,
XLSX, PPTX, and PDF editing or viewing powered by self-hosted ONLYOFFICE Docs.

## Architecture

```text
Browser
  -> Ion /office application
  -> Ion /office-engine/* authenticated same-origin reverse proxy
  -> private ONLYOFFICE Docs service

ONLYOFFICE Docs
  -> short-lived signed source-document endpoint in Ion
  -> short-lived scoped callback URL plus ONLYOFFICE outbox JWT
  -> exact allowlisted save-download URL

Ion
  -> encrypted immutable document versions
  -> actor-scoped SQLite metadata
  -> agent tools and verified work artifacts
```

Ion is the canonical document manager. ONLYOFFICE is a replaceable editing
engine and does not become the source of record.

## Configuration

| Variable | Default | Description |
|---|---|---|
| `ION_OFFICE_ENABLED` | `false` | Enable the editor engine |
| `ION_OFFICE_INTERNAL_URL` | none | Private ONLYOFFICE URL, such as `http://onlyoffice:80` |
| `ION_OFFICE_PUBLIC_PATH` | `/office-engine/` | Authenticated same-origin proxy path |
| `ION_OFFICE_CALLBACK_ORIGIN` | `ION_WEB_ORIGIN` | Exact public Ion origin used by the engine |
| `ION_OFFICE_JWT_SECRET` | none | Shared JWT secret; at least 32 bytes |
| `ION_OFFICE_JWT_SECRET_FILE` | none | Root-only file containing the shared JWT secret; mutually exclusive with `ION_OFFICE_JWT_SECRET` |
| `ION_OFFICE_MAX_FILE_BYTES` | `104857600` | Maximum imported or saved file size |
| `ION_OFFICE_MAX_VERSIONS` | `100` | Maximum immutable versions per document |
| `ION_OFFICE_IMAGE` | `onlyoffice/documentserver:9.2.0.1` | Compose image override; keep it pinned |

Generate and seal the shared secret:

```bash
openssl rand -hex 32
```

The exact same value must be presented to Ion and ONLYOFFICE. Never place the
populated secret in source control, image layers, or the browser bundle.

When Office is disabled, the navigation remains visible with an honest setup
state, creation and editing return `503`, and Ion does not connect to an engine.

## Compose

Set `ION_WEB_ORIGIN`, operator authentication, and
`ION_OFFICE_JWT_SECRET` in `deploy/compose/.env`, then use both files:

```bash
docker compose \
  -f deploy/compose/docker-compose.yml \
  -f deploy/compose/docker-compose.office.yml \
  up -d
```

The ONLYOFFICE service has no published port. Ion remains the sole public
application entry point.

## Health

After signing in, open `/office`. The engine card distinguishes unconfigured,
configured-but-unavailable, and available states. The authenticated
`GET /v1/office/status` endpoint exposes the same projection:

```json
{
  "configured": true,
  "available": true,
  "engine": "ONLYOFFICE",
  "message": "ONLYOFFICE Docs Community Edition is running.",
  "version": "community",
  "public_path": "/office-engine/"
}
```

## Storage, Backups, and Recovery

Office data is under the Ion data directory:

- `office/office.db` contains actor-scoped document, version, session, callback,
  and template metadata.
- `office/documents/` contains encrypted version blobs.
- `office/documents/templates/` contains complete native OOXML blank templates
  derived from the pinned ONLYOFFICE Document Server distribution.

Stop Ion or use a coordinated SQLite backup before copying the complete
`office/` directory. Store the vault key source separately. A backup without
the key source cannot decrypt versions; the key source without the data cannot
reconstruct them.

Version restore never rewrites history. It decrypts the selected historical
blob, verifies its digest, and commits the same content as a new immutable
current version.

Do not replace the ONLYOFFICE service while users are editing. Close editors
and confirm the latest committed versions in Ion first. Automated daemon
shutdown force-save and disconnect orchestration is not yet implemented.

## Agent and Artifact Boundaries

When the engine is available, Ion registers bounded tools to:

- list document metadata;
- inspect version history without returning document bytes;
- create blank native documents;
- rename or archive documents;
- restore a historical version;
- explicitly register the current version as verified work evidence.

Artifact registration is an explicit action. It materializes the selected
decrypted version under the configured Ion workspace, records it against an
existing outcome contract, and asks the work service to hash and verify the
regular file. That workspace artifact is plaintext by design and follows the
workspace's access and retention policy.

## Security Boundaries

- Browser API mutations require an authenticated Ion actor, exact Origin, and
  the session-bound CSRF value.
- The engine proxy requires the authenticated SameSite Ion browser session and
  exact Origin for stateful requests and WebSocket upgrades.
- Source downloads and callbacks use separate purpose-derived HMAC keys,
  bounded tokens, exact document/session bindings, and expiry.
- Callback requests must also carry a valid, unexpired ONLYOFFICE outbox JWT
  whose key and status match the opened callback body.
- Save URLs and every redirect target must stay on the configured engine origin
  or the configured Ion proxy path.
- OOXML imports are decompressed and read within entry, path, count, and total
  limits. Symlinks, traversal, macros, embedded OLE/ActiveX, external
  relationships, extension mismatches, empty files, and oversize files fail
  closed.
- Every committed version is encrypted with Ion Vault, content-hashed,
  immutable, and bounded.
- Cross-actor access is denied by actor-scoped document lookups.

## Deployment

- Compose: `deploy/compose/docker-compose.office.yml`
- Kubernetes: `deploy/kubernetes/office-deployment.yaml`
- Helm: `deploy/helm/ion` with `-f deploy/helm/values-office.yaml`
- Railway: the root appliance image runs ONLYOFFICE inside the single Ion
  service. Its engine port remains internal and the appliance generates the
  shared JWT.

The current packaging reserves approximately 2 GiB and limits ONLYOFFICE to
4 GiB. Size it for the selected edition and workload.

## Community Licensing

ONLYOFFICE's current Community licensing FAQ says Community Edition is offered
under AGPL v3, has a 20-connection editing limit, lacks mobile web editing, and
requires original branding and copyright notices to remain. Review the license
and the
[official Community licensing FAQ](https://helpcenter.onlyoffice.com/docs/faq/docs-community.aspx)
with counsel before a commercial or partner deployment.

The engine contract allows a later Developer or hosted adapter without
migrating Ion's canonical document data.

## Current Verification Boundary

The source currently supports native DOCX, XLSX, PPTX, and PDF import. Office
format conversion, PDF forms, alternate-format export, mobile fallback,
automated shutdown force-save, control-plane Office RPC operations, and live
ONLYOFFICE/browser acceptance remain pending. Static compilation does not
establish those production behaviors.
