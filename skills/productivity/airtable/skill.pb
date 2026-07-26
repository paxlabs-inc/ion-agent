meta {
  name: "airtable"
  version: "1.1.0"
  summary: "Airtable REST API via curl — records CRUD, filters, upserts"
  author: "community"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "airtable"
  keywords: "base"
  keywords: "table"
  keywords: "record"
  keywords: "airtable api"
  keywords: "filterByFormula"
  intents: "airtable_read"
  intents: "airtable_write"
  intents: "airtable_query"
  intents: "airtable_upsert"
  intents: "airtable_delete"
  patterns: "(read|write|create|update|delete|query|filter) .*(airtable|base|table|record)"
  patterns: "airtable .*(record|table|base|filter|upsert)"
}

requires {
  env_all: "AIRTABLE_API_KEY"
  tools {
    name: "terminal"
    required: true
  }
  binaries: "curl"
  binaries: "python3"
}

provides {
  capabilities: "airtable_read"
  capabilities: "airtable_write"
  capabilities: "airtable_query"
  capabilities: "airtable_upsert"
  capabilities: "airtable_batch"
  capabilities: "airtable_pagination"
  output_types: ".json"
}

actions {
  id: "setup"
  description: "First-time Airtable setup — generate PAT and configure access"
  trigger_phrases: "set up airtable"
  trigger_phrases: "configure airtable"
  trigger_phrases: "airtable setup"
    rules {
      text: "Generate a Personal Access Token (PAT) at https://airtable.com/create/tokens — tokens start with 'pat...'"
      priority: CRITICAL
    }
    rules {
      text: "Assign scopes: data.records:read, data.records:write, schema.bases:read"
      priority: CRITICAL
    }
    rules {
      text: "Add every target base to the token's Access list — a valid token on wrong base yields 403"
      priority: CRITICAL
    }
    rules {
      text: "Legacy 'key...' API keys retired Feb 2024 — only PATs and OAuth accepted"
      priority: HIGH
    }
    rules {
      text: "Store token in ${ION_HOME:-~/.ion}/.env as AIRTABLE_API_KEY=pat_your_token_here"
      priority: NORMAL
    }
    data {
      key: "auth_header"
      string_value: "Authorization: Bearer $AIRTABLE_API_KEY"
    }
    data {
      key: "base_endpoint"
      string_value: "https://api.airtable.com/v0"
    }
    data {
      key: "rate_limit"
      string_value: "5 requests/sec/base"
      unit: "requests/sec"
    }
}
actions {
  id: "list_bases"
  description: "List all bases visible to the token"
  trigger_phrases: "list airtable bases"
  trigger_phrases: "show my bases"
  trigger_phrases: "get bases"
    rules {
      text: "GET /v0/meta/bases — requires schema.bases:read scope"
      priority: HIGH
    }
    rules {
      text: "Always pipe through python3 -m json.tool for formatted output"
      priority: NORMAL
    }
    data {
      key: "endpoint"
      string_value: "GET https://api.airtable.com/v0/meta/bases"
    }
    examples {
      label: "list all bases"
      language: "bash"
      code: "curl -s \"https://api.airtable.com/v0/meta/bases\" \\\n  -H \"Authorization: Bearer $AIRTABLE_API_KEY\" | python3 -m json.tool"
    }
}
actions {
  id: "list_tables"
  description: "List tables and schema for a base"
  trigger_phrases: "list airtable tables"
  trigger_phrases: "show table schema"
  trigger_phrases: "get table fields"
    rules {
      text: "Run BEFORE any writes — confirms exact field names, IDs, and select options"
      priority: CRITICAL
    }
    rules {
      text: "GET /v0/meta/bases/{base_id}/tables"
      priority: HIGH
    }
    data {
      key: "endpoint"
      string_value: "GET https://api.airtable.com/v0/meta/bases/{base_id}/tables"
    }
}
actions {
  id: "read_records"
  description: "Read or filter records from a table"
  trigger_phrases: "list airtable records"
  trigger_phrases: "get records"
  trigger_phrases: "filter airtable records"
  trigger_phrases: "query airtable"
    rules {
      text: "filterByFormula requires URL encoding — use python3 urllib.parse.quote, never hand-encode"
      priority: CRITICAL
    }
    rules {
      text: "Max 100 records per page — use offset pagination for more"
      priority: HIGH
    }
    rules {
      text: "Include -s on curl to suppress progress bars"
      priority: HIGH
    }
    rules {
      text: "Empty fields are absent from responses — missing key means blank value, not missing field"
      priority: NORMAL
    }
    data {
      key: "endpoint"
      string_value: "GET https://api.airtable.com/v0/{base_id}/{table}"
    }
    data {
      key: "formula_patterns"
      map_value {
        entries {
          key: "exact_match"
          string_value: "{Field}='value'"
        }
        entries {
          key: "contains"
          string_value: "FIND('text', LOWER({Field}))"
        }
        entries {
          key: "and"
          string_value: "AND({A}='x', {B}='y')"
        }
        entries {
          key: "or"
          string_value: "OR({A}='x', {B}='y')"
        }
        entries {
          key: "not_empty"
          string_value: "NOT({Field}='')"
        }
        entries {
          key: "date_after"
          string_value: "IS_AFTER({Date}, TODAY())"
        }
      }
    }
    examples {
      label: "filter records with URL-encoded formula"
      language: "bash"
      code: "FORMULA=\"{Status}='Todo'\"\nENC=$(python3 -c 'import sys, urllib.parse; print(urllib.parse.quote(sys.argv[1], safe=\"\"))' \"$FORMULA\")\ncurl -s \"https://api.airtable.com/v0/$BASE_ID/$TABLE?filterByFormula=$ENC&maxRecords=20\" \\\n  -H \"Authorization: Bearer $AIRTABLE_API_KEY\" | python3 -m json.tool"
    }
}
actions {
  id: "create_records"
  description: "Create one or more records in a table"
  trigger_phrases: "create airtable record"
  trigger_phrases: "add record to airtable"
  trigger_phrases: "insert airtable row"
    rules {
      text: "Batch max is 10 records per request — iterate with pause for larger inserts"
      priority: CRITICAL
    }
    rules {
      text: "Use typecast: true to auto-coerce values and create new select options"
      priority: HIGH
    }
    rules {
      text: "POST /v0/{base_id}/{table} with {fields: {...}} or {records: [{fields: {...}}]}"
      priority: HIGH
    }
    examples {
      label: "create a single record"
      language: "bash"
      code: "curl -s -X POST \"https://api.airtable.com/v0/$BASE_ID/$TABLE\" \\\n  -H \"Authorization: Bearer $AIRTABLE_API_KEY\" \\\n  -H \"Content-Type: application/json\" \\\n  -d '{\"fields\":{\"Name\":\"New task\",\"Status\":\"Todo\",\"Priority\":\"High\"}}' | python3 -m json.tool"
    }
    examples {
      label: "batch create up to 10 records"
      language: "bash"
      code: "curl -s -X POST \"https://api.airtable.com/v0/$BASE_ID/$TABLE\" \\\n  -H \"Authorization: Bearer $AIRTABLE_API_KEY\" \\\n  -H \"Content-Type: application/json\" \\\n  -d '{\"typecast\":true,\"records\":[{\"fields\":{\"Name\":\"A\",\"Status\":\"Todo\"}},{\"fields\":{\"Name\":\"B\",\"Status\":\"Done\"}}]}' | python3 -m json.tool"
    }
}
actions {
  id: "update_records"
  description: "Update or upsert records"
  trigger_phrases: "update airtable record"
  trigger_phrases: "patch airtable"
  trigger_phrases: "upsert airtable"
    rules {
      text: "PATCH merges fields (preserves omitted). PUT overwrites and clears omitted fields — default to PATCH"
      priority: CRITICAL
    }
    rules {
      text: "For upsert: use performUpsert with fieldsToMergeOn — inserts on new merge-field values, patches on matches"
      priority: HIGH
    }
    rules {
      text: "Resolve record IDs via filterByFormula first — never guess IDs"
      priority: NORMAL
    }
    data {
      key: "endpoints"
      map_value {
        entries {
          key: "single"
          string_value: "PATCH /v0/{base_id}/{table}/{record_id}"
        }
        entries {
          key: "batch"
          string_value: "PATCH /v0/{base_id}/{table}"
        }
        entries {
          key: "upsert"
          string_value: "PATCH /v0/{base_id}/{table} with performUpsert"
        }
      }
    }
    examples {
      label: "upsert by merge field"
      language: "bash"
      code: "curl -s -X PATCH \"https://api.airtable.com/v0/$BASE_ID/$TABLE\" \\\n  -H \"Authorization: Bearer $AIRTABLE_API_KEY\" \\\n  -H \"Content-Type: application/json\" \\\n  -d '{\"performUpsert\":{\"fieldsToMergeOn\":[\"Email\"]},\"records\":[{\"fields\":{\"Email\":\"user@example.com\",\"Status\":\"Active\"}}]}' | python3 -m json.tool"
    }
}
actions {
  id: "delete_records"
  description: "Delete one or more records"
  trigger_phrases: "delete airtable record"
  trigger_phrases: "remove airtable row"
    rules {
      text: "Deletions are irreversible via API — confirm count and filter with user before executing"
      priority: CRITICAL
    }
    rules {
      text: "Batch delete max 10 records per request using records[] query param"
      priority: HIGH
    }
    examples {
      label: "delete a single record"
      language: "bash"
      code: "curl -s -X DELETE \"https://api.airtable.com/v0/$BASE_ID/$TABLE/$RECORD_ID\" \\\n  -H \"Authorization: Bearer $AIRTABLE_API_KEY\" | python3 -m json.tool"
    }
}
actions {
  id: "pagination"
  description: "Paginate through all records in a table"
  trigger_phrases: "get all airtable records"
  trigger_phrases: "paginate airtable"
  trigger_phrases: "list all rows"
    rules {
      text: "Max 100 records per page — loop with offset until field is absent"
      priority: HIGH
    }
    rules {
      text: "Pagination is per-page, not global — no way to increase the 100-record cap"
      priority: NORMAL
    }
    examples {
      label: "pagination loop"
      language: "bash"
      code: "OFFSET=\"\"\nwhile :; do\n  URL=\"https://api.airtable.com/v0/$BASE_ID/$TABLE?pageSize=100\"\n  [ -n \"$OFFSET\" ] && URL=\"$URL&offset=$OFFSET\"\n  RESP=$(curl -s \"$URL\" -H \"Authorization: Bearer $AIRTABLE_API_KEY\")\n  echo \"$RESP\" | python3 -c 'import json,sys; d=json.load(sys.stdin); [print(r[\"id\"], r[\"fields\"].get(\"Name\",\"\")) for r in d[\"records\"]]'\n  OFFSET=$(echo \"$RESP\" | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d.get(\"offset\",\"\"))')\n  [ -z \"$OFFSET\" ] && break\ndone"
    }
}

guardrails {
  text: "Always use curl via terminal tool — never web_extract (no auth headers) or browser_navigate"
  scope: ALWAYS
}

guardrails {
  text: "AIRTABLE_API_KEY auto-loaded from ${ION_HOME:-~/.ion}/.env — no need to re-export"
  scope: ALWAYS
}

guardrails {
  text: "Run schema check (list tables) before any writes to confirm field names"
  scope: WRITE_OPS
}

guardrails {
  text: "Confirm destructive deletions with user — show filter + record count first"
  scope: WRITE_OPS
}

guardrails {
  text: "Rate limit 5 req/sec/base — watch Retry-After header on 429"
  scope: ALWAYS
}

guardrails {
  text: "Read the errors array on non-2xx — structured codes pinpoint exact issues"
  scope: ALWAYS
}
