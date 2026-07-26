meta {
  name: "testing-llm-components"
  version: "1.0.0"
  summary: "Test strategies for LLM-backed and nondeterministic components — fake-transport layering, outcome-based assertions, live-test markers, flaky-test triage."
  author: "Ion Agent"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "llm testing"
  keywords: "test llm"
  keywords: "nondeterministic testing"
  keywords: "fake transport"
  keywords: "flaky test"
  keywords: "llm test"
  intents: "test_llm_component"
  intents: "handle_flaky_tests"
  intents: "setup_fake_transport"
  patterns: "(test|testing) .*(llm|model|nondeterministic|ai)"
  patterns: "fake.transport"
  patterns: "flaky .*(test|assertion)"
  patterns: "live.test .*(marker|skip)"
}

requires {
  tools {
    name: "terminal"
    required: true
  }
  tools {
    name: "write_file"
    required: true
  }
}

provides {
  capabilities: "llm_testing"
  capabilities: "nondeterministic_testing"
}

actions {
  id: "layer_tests"
  description: "Set up fake transport layering for LLM component tests"
  trigger_phrases: "test llm component"
  trigger_phrases: "set up fake transport"
  trigger_phrases: "mock llm"
    rules {
      text: "Inject the transport — model call lives behind an interface (transport.chat(messages) -> str); tests wire a FakeTransport returning canned replies"
      priority: CRITICAL
    }
    rules {
      text: "Unit-test everything deterministic against the fake — parsing, fallback, markdown-fence stripping, prompt contents, state transitions, retries"
      priority: CRITICAL
    }
    rules {
      text: "Keep live-API tests few and outcome-focused — two or three tests proving real model drives the loop end-to-end, no more"
      priority: HIGH
    }
    rules {
      text: "Live tests are slow, cost tokens, and are inherently probabilistic — minimize them"
      priority: NORMAL
    }
    examples {
      label: "fake transport pattern"
      language: "python"
      code: "class FakeTransport:\n    def __init__(self, responses):\n        self.responses = responses\n        self.calls = []\n    def chat(self, messages):\n        self.calls.append(messages)\n        return self.responses.pop(0)"
    }
}
actions {
  id: "assert_outcomes"
  description: "Assert on outcomes, not on model's decision path"
  trigger_phrases: "assert on outcome"
  trigger_phrases: "test llm outcome"
  trigger_phrases: "don't assert path"
    rules {
      text: "Assert on resulting state (files written, receipts recorded, goal marked complete) — NOT on exact decision sequence"
      priority: CRITICAL
    }
    rules {
      text: "A failing path assertion with a valid outcome means THE TEST IS WRONG, not the code"
      priority: CRITICAL
    }
    rules {
      text: "A model may decompose a goal into subgoals OR act directly — both can satisfy the task"
      priority: HIGH
    }
    rules {
      text: "Structural invariants are OK to assert: at least one ACT step, no budget violations"
      priority: NORMAL
    }
    examples {
      label: "correct outcome assertion"
      language: "python"
      code: "# GOOD: assert on outcome\nassert file_exists(\"output.txt\")\nassert receipt_count >= 1\nassert goal_status == \"completed\"\n\n# BAD: assert on path\nassert len(model_calls) == 3  # model might do it in 1 call"
    }
}
actions {
  id: "flaky_triage"
  description: "Triage flaky tests caused by model path variance"
  trigger_phrases: "flaky llm test"
  trigger_phrases: "test fails sometimes"
  trigger_phrases: "nondeterministic failure"
    rules {
      text: "One failure where model took a different-but-valid route is a flaky assertion, not a regression"
      priority: HIGH
    }
    rules {
      text: "Before touching code: print decision trace, confirm OUTCOME was wrong (not just path), re-run 2-3 times"
      priority: HIGH
    }
    rules {
      text: "A real regression fails consistently; a path variance passes on retry"
      priority: NORMAL
    }
    data {
      key: "triage_steps"
      list_value {
        items {
          string_value: "1. Print the model's decision trace for the failing run"
        }
        items {
          string_value: "2. Confirm the OUTCOME was actually wrong (not just the path)"
        }
        items {
          string_value: "3. Only then tighten code or the test"
        }
        items {
          string_value: "4. Re-run 2-3 times — real regression fails consistently"
        }
      }
    }
}
actions {
  id: "reduce_nondeterminism"
  description: "Prompt-side levers to reduce model nondeterminism"
  trigger_phrases: "reduce nondeterminism"
  trigger_phrases: "make llm deterministic"
  trigger_phrases: "constrain model output"
    rules {
      text: "Demand exactly one JSON object; enumerate the legal actions in the prompt"
      priority: HIGH
    }
    rules {
      text: "Constrain with physics, not hope — let runtime reject invalid decisions (unknown tool, budget exceeded)"
      priority: HIGH
    }
    rules {
      text: "Give the model structured world state (file maps, receipt counts), not prose — structured in, structured out"
      priority: NORMAL
    }
    data {
      key: "llm_live_marker"
      string_value: "[tool.pytest.ini_options]\nmarkers = [\"llm_live: tests that call the live LLM API (deselect with -m 'not llm_live')\"]\n"
    }
}

guardrails {
  text: "Never assert on model's decision path when outcome is what matters"
  scope: ALWAYS
}

guardrails {
  text: "Live LLM tests must be marked with llm_live pytest marker"
  scope: ALWAYS
}
