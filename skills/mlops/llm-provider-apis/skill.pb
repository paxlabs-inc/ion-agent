meta {
  name: "llm-provider-apis"
  version: "1.0.0"
  summary: "Call configured LLM providers via HTTP (OpenAI-compatible endpoints) — credentials from .env"
  author: "community"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "llm api"
  keywords: "provider api"
  keywords: "openai compatible"
  keywords: "direct http"
  keywords: "api call"
  keywords: "llm endpoint"
  keywords: "base url"
  keywords: "api key"
  intents: "call_llm_api"
  intents: "smoke_test_provider"
  intents: "llm_http_request"
  patterns: "(call|hit|query) .*(llm|api|provider|endpoint)"
  patterns: "(openai|compatible) .*(api|endpoint|http)"
  patterns: "(smoke.?test|test) .*(provider|api|llm)"
}

requires {
  tools {
    name: "terminal"
    required: true
  }
  tools {
    name: "webfetch"
    required: false
  }
  binaries: "python3"
}

provides {
  capabilities: "llm_api_calls"
  capabilities: "provider_smoke_testing"
  capabilities: "structured_llm_decisions"
}

actions {
  id: "read_credentials"
  description: "Read LLM provider credentials from the .env store"
  trigger_phrases: "read api keys"
  trigger_phrases: "check providers"
  trigger_phrases: "list credentials"
  trigger_phrases: "get provider config"
    rules {
      text: "The read_file tool REFUSES /data/.ion/.env — use terminal (cat/Python) instead"
      priority: CRITICAL
    }
    rules {
      text: "Never print full API keys into replies — confirm presence (key set: True) and masked prefixes only"
      priority: CRITICAL
    }
    rules {
      text: "Credentials stored as *_API_KEY / *_BASE_URL pairs with _MODEL_<KEYNAME> mappings"
      priority: HIGH
    }
    data {
      key: "credential_location"
      string_value: "/data/.ion/.env"
    }
    data {
      key: "credential_format"
      string_value: "*_API_KEY, *_BASE_URL pairs with _MODEL_<KEYNAME> mappings"
    }
}
actions {
  id: "smoke_test"
  description: "Test an LLM provider endpoint with a simple request"
  trigger_phrases: "smoke test"
  trigger_phrases: "test api"
  trigger_phrases: "test provider"
  trigger_phrases: "check if api works"
    rules {
      text: "No SDK needed — use stdlib urllib.request for OpenAI-compatible endpoints"
      priority: CRITICAL
    }
    rules {
      text: "Works for MiMo (Xiaomi), DashScope, Kimi, xAI, and any custom provider"
      priority: HIGH
    }
    rules {
      text: "Use max_tokens >= 512 for smoke tests to avoid token starvation"
      priority: HIGH
    }
    examples {
      label: "stdlib smoke test"
      language: "python"
      code: "import json, urllib.request\nreq = urllib.request.Request(\n    f\"{base}/chat/completions\",\n    data=json.dumps({\"model\": model,\n                     \"messages\": [{\"role\": \"user\", \"content\": \"Say OK\"}],\n                     \"max_tokens\": 512}).encode(),\n    headers={\"Authorization\": f\"Bearer {key}\", \"Content-Type\": \"application/json\"},\n)\nwith urllib.request.urlopen(req, timeout=60) as r:\n    body = json.loads(r.read())\ncontent = body[\"choices\"][0][\"message\"][\"content\"]"
    }
}
actions {
  id: "reasoning_models"
  description: "Handle reasoning models that share max_tokens with thinking"
  trigger_phrases: "reasoning model"
  trigger_phrases: "empty content"
  trigger_phrases: "token starvation"
  trigger_phrases: "reasoning tokens"
    rules {
      text: "Reasoning models (e.g. MiMo mimo-v2.5-pro-ultraspeed) spend reasoning tokens OUT OF max_tokens budget"
      priority: CRITICAL
    }
    rules {
      text: "A small max_tokens cap (10-50) yields HTTP 200 with EMPTY content — looks broken, is actually token starvation"
      priority: CRITICAL
    }
    rules {
      text: "Use max_tokens >= 512 for smoke tests"
      priority: HIGH
    }
    rules {
      text: "Check usage.completion_tokens_details.reasoning_tokens when output is suspiciously short"
      priority: HIGH
    }
    data {
      key: "reasoning_content_field"
      string_value: "reasoning_content (separate field for MiMo models)"
    }
    data {
      key: "usage_field"
      string_value: "usage.completion_tokens_details.reasoning_tokens"
    }
}
actions {
  id: "structured_decisions"
  description: "Use LLM as a component in deterministic systems"
  trigger_phrases: "structured output"
  trigger_phrases: "llm as component"
  trigger_phrases: "json from llm"
  trigger_phrases: "deterministic llm"
    rules {
      text: "Prompt for exactly one JSON object with available actions enumerated"
      priority: CRITICAL
    }
    rules {
      text: "Parse defensively: strip markdown fences, extract first {...} span, fall back to safe default on garbage"
      priority: HIGH
    }
    rules {
      text: "Inject transport behind an interface so tests use scripted fakes"
      priority: HIGH
    }
    examples {
      label: "structured decision pattern"
      language: "text"
      code: "Prompt: respond with {\"kind\": \"done\"|\"act\"|\"decompose\", ...}\nParse: strip fences, extract first {...}, default on garbage\nInterface: thin marked layer hits live API, tests use scripted fake"
    }
}

guardrails {
  text: "Never print full API keys — only masked prefixes"
  scope: AUTH_OPS
}

guardrails {
  text: "Use terminal (not read_file) to access /data/.ion/.env"
  scope: ALWAYS
}

guardrails {
  text: "Reasoning models need max_tokens >= 512 to avoid empty output"
  scope: ALWAYS
}
