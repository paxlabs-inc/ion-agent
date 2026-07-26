meta {
  name: "test-driven-development"
  version: "1.1.0"
  summary: "TDD discipline — enforce RED-GREEN-REFACTOR cycle, tests before code."
  author: "Ion Agent"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "tdd"
  keywords: "test driven development"
  keywords: "red green refactor"
  keywords: "write test first"
  keywords: "test first"
  intents: "tdd_implementation"
  intents: "write_test_first"
  intents: "red_green_refactor"
  patterns: "(tdd|test.driven|red.green.refactor)"
  patterns: "(write|create) .*(test|test first)"
  patterns: "test.driven .*(development|design)"
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
  capabilities: "tdd_workflow"
}

actions {
  id: "tdd_cycle"
  description: "Execute the RED-GREEN-REFACTOR TDD cycle"
  trigger_phrases: "use tdd"
  trigger_phrases: "test driven development"
  trigger_phrases: "write test first"
  trigger_phrases: "red green refactor"
    rules {
      text: "NO PRODUCTION CODE WITHOUT A FAILING TEST FIRST — write code before the test? Delete it. Start over."
      priority: CRITICAL
    }
    rules {
      text: "MANDATORY: Watch the test fail before implementing — test passes immediately means you're testing existing behavior"
      priority: CRITICAL
    }
    rules {
      text: "One behavior per test — clear descriptive name, real code not mocks (unless unavoidable)"
      priority: CRITICAL
    }
    rules {
      text: "GREEN: write simplest code to pass — don't add features, refactor, or improve beyond the test"
      priority: HIGH
    }
    rules {
      text: "REFACTOR: only after green — remove duplication, improve names, extract helpers. Keep tests green."
      priority: HIGH
    }
    rules {
      text: "Do NOT write all tests first then all implementation (horizontal slices) — use vertical tracer bullets: test1→impl1, test2→impl2"
      priority: HIGH
    }
    rules {
      text: "Cheating is OK in GREEN: hardcode returns, copy-paste, duplicate code, skip edge cases — fix in REFACTOR"
      priority: NORMAL
    }
    rules {
      text: "Commit after every task: git add [files] && git commit -m 'type: description'"
      priority: NORMAL
    }
    data {
      key: "cycle"
      list_value {
        items {
          string_value: "RED — Write one failing test showing what should happen"
        }
        items {
          string_value: "VERIFY RED — Run test, confirm it fails for expected reason"
        }
        items {
          string_value: "GREEN — Write minimal code to pass the test"
        }
        items {
          string_value: "VERIFY GREEN — Run test, confirm pass, run full suite for regressions"
        }
        items {
          string_value: "REFACTOR — Clean up while keeping tests green"
        }
      }
    }
    data {
      key: "red_flags"
      list_value {
        items {
          string_value: "Code before test"
        }
        items {
          string_value: "Test after implementation"
        }
        items {
          string_value: "Test passes immediately on first run"
        }
        items {
          string_value: "Rationalizing 'just this once'"
        }
        items {
          string_value: "'I already manually tested it'"
        }
        items {
          string_value: "'Deleting X hours is wasteful' — sunk cost fallacy"
        }
        items {
          string_value: "'TDD is dogmatic, I'm being pragmatic' — TDD IS pragmatic"
        }
      }
    }
    examples {
      label: "RED-GREEN-REFACTOR cycle"
      language: "python"
      code: "# RED — write failing test\ndef test_retries_failed_operations_3_times():\n    attempts = 0\n    def operation():\n        nonlocal attempts\n        attempts += 1\n        if attempts < 3:\n            raise Exception('fail')\n        return 'success'\n    result = retry_operation(operation)\n    assert result == 'success'\n    assert attempts == 3\n\n# GREEN — minimal code\ndef retry_operation(operation, max_retries=3):\n    for i in range(max_retries):\n        try:\n            return operation()\n        except Exception:\n            if i == max_retries - 1:\n                raise"
    }
}
actions {
  id: "verify_tdd"
  description: "Verify TDD discipline was followed"
  trigger_phrases: "check tdd"
  trigger_phrases: "verify tests"
  trigger_phrases: "tdd checklist"
    rules {
      text: "Checklist: every function has test, watched each fail, each failed for expected reason, wrote minimal code, all tests pass, output pristine"
      priority: HIGH
    }
    rules {
      text: "Tests use real code (mocks only if unavoidable), edge cases and errors covered"
      priority: NORMAL
    }
    data {
      key: "verification_checklist"
      list_value {
        items {
          string_value: "Every new function/method has a test"
        }
        items {
          string_value: "Watched each test fail before implementing"
        }
        items {
          string_value: "Each test failed for expected reason"
        }
        items {
          string_value: "Wrote minimal code to pass each test"
        }
        items {
          string_value: "All tests pass"
        }
        items {
          string_value: "Output pristine (no errors, warnings)"
        }
        items {
          string_value: "Tests use real code (mocks only if unavoidable)"
        }
        items {
          string_value: "Edge cases and errors covered"
        }
      }
    }
}

guardrails {
  text: "No production code without a failing test first"
  scope: ALWAYS
}

guardrails {
  text: "If you didn't watch the test fail, you don't know if it tests the right thing"
  scope: ALWAYS
}
