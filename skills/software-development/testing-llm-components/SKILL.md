---
name: testing-llm-components
description: Test strategies for LLM-backed and nondeterministic components — fake-transport layering, outcome-based assertions, live-test markers, flaky-test triage.
---

# Testing LLM-Backed and Nondeterministic Components

Companion to the TDD skill (which covers the red-green-refactor cycle itself). This skill covers what changes when the system under test includes a live model — an LLM planner, classifier, router, or generator whose outputs are valid across a RANGE of behaviors, not a single string.

## Layer the tests: fake transport, thin live layer

1. **Inject the transport.** The model call lives behind an interface (e.g. `transport.chat(messages) -> str`). Production wires the real HTTP client; tests wire a `FakeTransport` returning canned replies and recording requests.
2. **Unit-test everything deterministic against the fake.** Parsing, fallback-on-garbage, markdown-fence stripping, prompt contents, state transitions, retries — all of it, fast and offline.
3. **Keep live-API tests few and outcome-focused.** Two or three tests proving the real model can drive the loop end-to-end. No more — they are slow, cost tokens, and are inherently probabilistic.

## Assert on outcomes, not on the model's path

A model may decompose a goal into subgoals OR act directly — both can satisfy the task. Assert on resulting state:

- files written, receipts recorded, goal marked complete
- structural invariants (at least one ACT step, no budget violations)

Do NOT assert the exact decision sequence unless the path itself is the requirement. A failing path assertion with a valid outcome means THE TEST IS WRONG, not the code. (Learned 2026-07-23: MiMo passed a compound-goal test on first run by decomposing, then "failed" on re-run by acting directly — the assertion on file existence was correct; an assertion on decomposition would have been flaky-by-design. Verify the outcome survived, not the route taken.)

## Mark live tests

Register a pytest marker in `pyproject.toml` so offline/CI runs deselect:

```toml
[tool.pytest.ini_options]
markers = [
    "llm_live: tests that call the live LLM API (deselect with -m 'not llm_live')",
]
```

## Flaky-test triage for model-driven tests

One failure where the model took a different-but-valid route is a flaky assertion, not a regression. Before touching code:

1. Print the model's decision trace for the failing run.
2. Confirm the OUTCOME was actually wrong (not just the path).
3. Only then tighten code or the test.
4. Re-run the live test 2-3 times — a real regression fails consistently; a path variance passes on retry.

## Prompt-side levers that reduce nondeterminism

- Demand exactly one JSON object; enumerate the legal actions in the prompt.
- Constrain with physics, not hope: let the runtime reject invalid decisions (unknown tool, budget exceeded) rather than trusting the model to comply.
- Give the model structured world state (file maps, receipt counts), not prose — structured in, structured out.
