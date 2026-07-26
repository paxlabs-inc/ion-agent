---
name: airtable
description: Airtable REST API via curl. Records CRUD, filters, upserts.
version: 1.1.0
author: community
license: MIT
platforms: [linux, macos, windows]
prerequisites:
  env_vars: [AIRTABLE_API_KEY]
  commands: [curl]
metadata:
  ion:
    tags: [Airtable, Productivity, Database, API]
    homepage: https://airtable.com/developers/web/api/introduction
---

# Airtable — Bases, Tables & Records

Interact with Airtable's REST API using `curl` through the `terminal` tool. No MCP server, no OAuth handshake, no Python SDK required — just `curl` paired with a personal access token.

## Prerequisites

1. Generate a **Personal Access Token (PAT)** at https://airtable.com/create/tokens (these tokens begin with `pat...`).
2. Assign at least these scopes:
   - `data.records:read` — retrieve rows
   - `data.records:write` — add / modify / remove rows
   - `schema.bases:read` — enumerate bases and tables
3. **Critical:** within the same token configuration page, add every base you intend to access under the token's **Access** list. PATs are restricted per-base — a valid token targeting the wrong base yields `403`.
4. Save the token to `${ION_HOME:-~/.ion}/.env` (or through `ion setup`):
   ```
   AIRTABLE_API_KEY=pat_your_token_here
   ```

> Note: the legacy `key...` API keys were retired in Feb 2024. Only PATs and OAuth tokens are accepted now.

## API Basics

- **Endpoint:** `https://api.airtable.com/v0`
- **Auth header:** `Authorization: Bearer $AIRTABLE_API_KEY`
- **Every request** expects JSON (`Content-Type: application/json` for any POST/PATCH/PUT body).
- **Object identifiers:** bases `app...`, tables `tbl...`, records `rec...`, fields `fld...`. IDs are immutable; names can change. Prefer IDs in automations.
- **Rate limit:** 5 requests/sec/base. A `429` response means you should back off. Rapid bursts against a single base will be throttled.

Base curl pattern:
```bash
curl -s "https://api.airtable.com/v0/$BASE_ID/$TABLE?maxRecords=5" \
  -H "Authorization: Bearer $AIRTABLE_API_KEY" | python3 -m json.tool
```

Include `-s` on every call to silence curl's progress bar — this keeps tool output clean for Ion. Always pipe through `python3 -m json.tool` (guaranteed available) or `jq` (if present) for formatted JSON.

## Field Types (request body shapes)

| Field type | Write shape |
|---|---|
| Single line text | `"Name": "hello"` |
| Long text | `"Notes": "multi\nline"` |
| Number | `"Score": 42` |
| Checkbox | `"Done": true` |
| Single select | `"Status": "Todo"` (the name must already exist unless `typecast: true`) |
| Multi-select | `"Tags": ["urgent", "bug"]` |
| Date | `"Due": "2026-04-01"` |
| DateTime (UTC) | `"At": "2026-04-01T14:30:00.000Z"` |
| URL / Email / Phone | `"Link": "https://…"` |
| Attachment | `"Files": [{"url": "https://…"}]` (Airtable downloads and rehosts) |
| Linked record | `"Owner": ["recXXXXXXXXXXXXXX"]` (array of record IDs) |
| User | `"AssignedTo": {"id": "usrXXXXXXXXXXXXXX"}` |

Include `"typecast": true` at the top level of a create/update payload to let Airtable automatically coerce values (e.g. dynamically create a new select option, convert `"42"` → `42`).

## Common Queries

### List bases visible to the token
```bash
curl -s "https://api.airtable.com/v0/meta/bases" \
  -H "Authorization: Bearer $AIRTABLE_API_KEY" | python3 -m json.tool
```

### List tables + schema for a base
```bash
curl -s "https://api.airtable.com/v0/meta/bases/$BASE_ID/tables" \
  -H "Authorization: Bearer $AIRTABLE_API_KEY" | python3 -m json.tool
```
Run this BEFORE making any changes — it confirms exact field names and IDs, reveals `options.choices` for select fields, and shows primary-field names.

### List records (first 10)
```bash
curl -s "https://api.airtable.com/v0/$BASE_ID/$TABLE?maxRecords=10" \
  -H "Authorization: Bearer $AIRTABLE_API_KEY" | python3 -m json.tool
```

### Get a single record
```bash
curl -s "https://api.airtable.com/v0/$BASE_ID/$TABLE/$RECORD_ID" \
  -H "Authorization: Bearer $AIRTABLE_API_KEY" | python3 -m json.tool
```

### Filter records (filterByFormula)
Airtable formulas need URL encoding. Delegate this to Python stdlib — never encode by hand:
```bash
FORMULA="{Status}='Todo'"
ENC=$(python3 -c 'import sys, urllib.parse; print(urllib.parse.quote(sys.argv[1], safe=""))' "$FORMULA")
curl -s "https://api.airtable.com/v0/$BASE_ID/$TABLE?filterByFormula=$ENC&maxRecords=20" \
  -H "Authorization: Bearer $AIRTABLE_API_KEY" | python3 -m json.tool
```

Useful formula patterns:
- Exact match: `{Email}='user@example.com'`
- Contains: `FIND('bug', LOWER({Title}))`
- Multiple conditions: `AND({Status}='Todo', {Priority}='High')`
- Or: `OR({Owner}='alice', {Owner}='bob')`
- Not empty: `NOT({Assignee}='')`
- Date comparison: `IS_AFTER({Due}, TODAY())`

### Sort + select specific fields
```bash
curl -s "https://api.airtable.com/v0/$BASE_ID/$TABLE?sort%5B0%5D%5Bfield%5D=Priority&sort%5B0%5D%5Bdirection%5D=asc&fields%5B%5D=Name&fields%5B%5D=Status" \
  -H "Authorization: Bearer $AIRTABLE_API_KEY" | python3 -m json.tool
```
Square brackets in query parameters must be percent-encoded (`%5B` / `%5D`).

### Use a named view
```bash
curl -s "https://api.airtable.com/v0/$BASE_ID/$TABLE?view=Grid%20view&maxRecords=50" \
  -H "Authorization: Bearer $AIRTABLE_API_KEY" | python3 -m json.tool
```
Views apply their stored filter + sort on the server side.

## Common Mutations

### Create a record
```bash
curl -s -X POST "https://api.airtable.com/v0/$BASE_ID/$TABLE" \
  -H "Authorization: Bearer $AIRTABLE_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"fields":{"Name":"New task","Status":"Todo","Priority":"High"}}' | python3 -m json.tool
```

### Create up to 10 records in one call
```bash
curl -s -X POST "https://api.airtable.com/v0/$BASE_ID/$TABLE" \
  -H "Authorization: Bearer $AIRTABLE_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "typecast": true,
    "records": [
      {"fields": {"Name": "Task A", "Status": "Todo"}},
      {"fields": {"Name": "Task B", "Status": "In progress"}}
    ]
  }' | python3 -m json.tool
```
Batch endpoints accept a maximum of **10 records per request**. For bigger inserts, iterate in batches of 10 with a brief pause to stay within the 5 req/sec/base limit.

### Update a record (PATCH — merges, preserves unchanged fields)
```bash
curl -s -X PATCH "https://api.airtable.com/v0/$BASE_ID/$TABLE/$RECORD_ID" \
  -H "Authorization: Bearer $AIRTABLE_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"fields":{"Status":"Done"}}' | python3 -m json.tool
```

### Upsert by a merge field (no ID needed)
```bash
curl -s -X PATCH "https://api.airtable.com/v0/$BASE_ID/$TABLE" \
  -H "Authorization: Bearer $AIRTABLE_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "performUpsert": {"fieldsToMergeOn": ["Email"]},
    "records": [
      {"fields": {"Email": "user@example.com", "Status": "Active"}}
    ]
  }' | python3 -m json.tool
```
`performUpsert` inserts records when the merge-field value is new, and patches existing records when the merge-field value matches. Ideal for idempotent synchronization.

### Delete a record
```bash
curl -s -X DELETE "https://api.airtable.com/v0/$BASE_ID/$TABLE/$RECORD_ID" \
  -H "Authorization: Bearer $AIRTABLE_API_KEY" | python3 -m json.tool
```

### Delete up to 10 records in one call
```bash
curl -s -X DELETE "https://api.airtable.com/v0/$BASE_ID/$TABLE?records%5B%5D=rec1&records%5B%5D=rec2" \
  -H "Authorization: Bearer $AIRTABLE_API_KEY" | python3 -m json.tool
```

## Pagination

List endpoints return a maximum of **100 records per page**. When the response includes `"offset": "..."`, supply that value in the next request. Continue looping until the offset field disappears:

```bash
OFFSET=""
while :; do
  URL="https://api.airtable.com/v0/$BASE_ID/$TABLE?pageSize=100"
  [ -n "$OFFSET" ] && URL="$URL&offset=$OFFSET"
  RESP=$(curl -s "$URL" -H "Authorization: Bearer $AIRTABLE_API_KEY")
  echo "$RESP" | python3 -c 'import json,sys; d=json.load(sys.stdin); [print(r["id"], r["fields"].get("Name","")) for r in d["records"]]'
  OFFSET=$(echo "$RESP" | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d.get("offset",""))')
  [ -z "$OFFSET" ] && break
done
```

## Typical Ion Workflow

1. **Verify auth.** `curl -s -o /dev/null -w "%{http_code}\n" https://api.airtable.com/v0/meta/bases -H "Authorization: Bearer $AIRTABLE_API_KEY"` — expect `200`.
2. **Locate the base.** List bases (see above) OR ask the user directly for the `app...` ID if the token doesn't have `schema.bases:read`.
3. **Examine the schema.** `GET /v0/meta/bases/$BASE_ID/tables` — locally cache the exact field names and primary-field name before performing any writes.
4. **Read before writing.** For "update X where Y", use `filterByFormula` first to resolve the `rec...` ID, then `PATCH /v0/$BASE_ID/$TABLE/$RECORD_ID`. Never guess record IDs.
5. **Batch your writes.** Group related creates into a single 10-record POST to stay within the 5 req/sec budget.
6. **Destructive operations.** Deletions cannot be reversed via API. When the user says "delete all Xs", echo back the filter + record count and get confirmation before executing.

## Pitfalls

- **`filterByFormula` requires URL encoding.** Field names containing spaces or non-ASCII characters also need encoding (`{My Field}` → `%7BMy%20Field%7D`). Use Python stdlib (see pattern above) — never hand-escape.
- **Empty fields are absent from responses.** A missing `"Assignee"` key doesn't indicate the field doesn't exist — it means this record's value is blank. Consult the schema (step 3) before assuming a field is missing.
- **PATCH vs PUT.** `PATCH` merges the supplied fields into the existing record. `PUT` overwrites the entire record and clears any field you omitted. Default to `PATCH`.
- **Single-select values must pre-exist.** Writing `"Status": "Shipping"` when `Shipping` isn't in the field's option list triggers `INVALID_MULTIPLE_CHOICE_OPTIONS` unless you include `"typecast": true` (which auto-creates the option).
- **Per-base token scoping.** A `403` on one base while others work means the token's Access list doesn't cover that base — not a scope or auth problem. Direct the user to https://airtable.com/create/tokens to add it.
- **Rate limits are per base, not per token.** 5 req/sec on `baseA` and 5 req/sec on `baseB` is fine; 6 req/sec on just `baseA` triggers throttling. Watch the `Retry-After` header on `429`.

## Important Notes for Ion

- **Always invoke `curl` via the `terminal` tool.** Do NOT use `web_extract` (it cannot supply auth headers) or `browser_navigate` (requires UI auth and is slow).
- **`AIRTABLE_API_KEY` is automatically available** from `${ION_HOME:-~/.ion}/.env` in the subprocess when this skill loads — no need to re-export it before each `curl` call.
- **Handle curly braces in formulas carefully.** In a heredoc body, `{Status}` is literal. In a shell argument, `{Status}` is safe outside brace-expansion context — but route dynamic strings through `python3 urllib.parse.quote` before embedding in a URL.
- **Pretty-print with `python3 -m json.tool`** (always available) rather than `jq` (optional). Only reach for `jq` when you need filtering or projection.
- **Pagination is per-page, not global.** The 100-record cap is a hard limit; there's no way to increase it. Loop with `offset` until the field is absent.
- **Read the `errors` array** on non-2xx responses — Airtable returns structured error codes like `AUTHENTICATION_REQUIRED`, `INVALID_PERMISSIONS`, `MODEL_ID_NOT_FOUND`, `INVALID_MULTIPLE_CHOICE_OPTIONS` that pinpoint the exact issue.
