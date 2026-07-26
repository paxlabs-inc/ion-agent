# MiMo (Xiaomi) Endpoint Notes

Configured in `/data/.ion/.env` as the custom provider:

- `CUSTOM_PROVIDER_BASE_URL=https://api.xiaomimimo.com/v1`
- `CUSTOM_PROVIDER_API_KEY=sk-s5b...` (masked in .env; real key present)
- `CUSTOM_PROVIDER_NAME=Xiaomi`
- `_MODEL_CUSTOM_PROVIDER_API_KEY=mimo-v2.5-pro-ultraspeed`

## Behavior

- OpenAI-compatible `POST /v1/chat/completions`. Bearer auth. Works with stdlib `urllib.request` — no SDK.
- **Reasoning model**: responses include `reasoning_content` (visible chain) alongside `content`, and `usage.completion_tokens_details.reasoning_tokens`.
- **Token budget pitfall (verified 2026-07-23)**: `max_tokens=10` returned HTTP 200 with `finish_reason: "stop"` and EMPTY `content` — all 22 completion tokens were spent on reasoning. With `max_tokens=50` the same prompt returned content fine. Symptom "API returns blank replies" = raise max_tokens, not a broken endpoint. Use >=512 for real work.
- Handles structured-output prompts well: with a system prompt demanding exactly one JSON decision object (done/act/decompose), it complied reliably across repeated runs and drove a recursive agent loop correctly (decomposed a two-file task into subgoals, issued one tool call each).
- Observed latency ~5-8s per decision call at 485 tok/s decode.

## Full response shape (2026-07-23)

```json
{
  "choices": [{
    "finish_reason": "stop",
    "message": {
      "content": "OK! \ud83d\udc4b",
      "reasoning_content": "The user is asking me to say \"OK\"...",
      "role": "assistant"
    }
  }],
  "model": "mimo-v2.5-pro-ultraspeed",
  "usage": {
    "completion_tokens": 22,
    "prompt_tokens": 251,
    "completion_tokens_details": {"reasoning_tokens": 18},
    "prompt_tokens_details": {"cached_tokens": 128}
  }
}
```

## Working integration example

`/data/projects/world-runtime/world_runtime/planner_mimo.py` — `MiMoTransport.from_env()` loads credentials from the .env, `MiMoPlanner` adapts the model to a Planner protocol with defensive JSON parsing (fence-stripping, brace extraction, garbage fallback). Live tests: `/data/projects/world-runtime/tests/test_mimo_live.py` (marked `mimo_live`).
