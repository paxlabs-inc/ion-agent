meta {
  name: "systematic-debugging"
  version: "1.1.0"
  summary: "4-phase root cause debugging — understand bugs before fixing."
  author: "Ion Agent"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "debug"
  keywords: "debugging"
  keywords: "root cause"
  keywords: "troubleshoot"
  keywords: "investigate bug"
  keywords: "systematic debug"
  intents: "debug_issue"
  intents: "investigate_bug"
  intents: "root_cause_analysis"
  patterns: "(debug|investigate|troubleshoot) .*(bug|issue|error|failure|crash)"
  patterns: "root cause .*(analysis|investigation)"
  patterns: "(why|how) .*(fail|crash|break|error)"
}

requires {
  tools {
    name: "terminal"
    required: true
  }
  tools {
    name: "write_file"
    required: false
  }
}

provides {
  capabilities: "systematic_debugging"
  capabilities: "root_cause_analysis"
}

actions {
  id: "systematic_debug"
  description: "Follow the 4-phase debugging methodology"
  trigger_phrases: "debug this"
  trigger_phrases: "investigate this bug"
  trigger_phrases: "find root cause"
  trigger_phrases: "troubleshoot"
    rules {
      text: "ALWAYS find root cause before attempting fixes — symptom fixes are failure"
      priority: CRITICAL
    }
    rules {
      text: "NO FIXES WITHOUT ROOT CAUSE INVESTIGATION FIRST — if you haven't completed Phase 1, you cannot propose fixes"
      priority: CRITICAL
    }
    rules {
      text: "Build a tight feedback loop BEFORE reading code to build a theory — fast, deterministic, agent-runnable, specific to this bug"
      priority: CRITICAL
    }
    rules {
      text: "Phase 1 checklist: error messages read, tight loop exists and is red, recent changes identified, evidence gathered, problem isolated"
      priority: HIGH
    }
    rules {
      text: "Phase 2: minimize reproduction, find working examples, compare against references, identify differences"
      priority: HIGH
    }
    rules {
      text: "Phase 3: form 3-5 ranked falsifiable hypotheses, test highest-ranked with smallest probe, one variable at a time"
      priority: HIGH
    }
    rules {
      text: "Phase 4: create failing test FIRST, implement single fix, verify — if 3+ fixes failed, question the architecture"
      priority: HIGH
    }
    rules {
      text: "Tag temporary debug logs with unique prefix like [DEBUG-a4f2] so cleanup is a single search"
      priority: NORMAL
    }
    data {
      key: "phases"
      map_value {
        entries {
          key: "phase_1"
          string_value: "Root Cause Investigation — read errors, build tight loop, check changes, gather evidence, trace data flow"
        }
        entries {
          key: "phase_2"
          string_value: "Pattern Analysis — minimize repro, find working examples, compare, identify differences"
        }
        entries {
          key: "phase_3"
          string_value: "Hypothesis and Testing — form ranked hypotheses, test minimally, one variable at a time"
        }
        entries {
          key: "phase_4"
          string_value: "Implementation — create failing test, fix root cause, verify"
        }
      }
    }
    data {
      key: "tight_loop_methods"
      list_value {
        items {
          string_value: "1. Failing test at the seam that reaches the bug"
        }
        items {
          string_value: "2. HTTP script / curl against dev server"
        }
        items {
          string_value: "3. CLI invocation with fixture input"
        }
        items {
          string_value: "4. Headless browser script (Playwright/Puppeteer)"
        }
        items {
          string_value: "5. Replay a captured trace"
        }
        items {
          string_value: "6. Throwaway harness"
        }
        items {
          string_value: "7. Property / fuzz loop"
        }
        items {
          string_value: "8. Bisection harness for git bisect run"
        }
        items {
          string_value: "9. Differential loop comparing old vs new"
        }
      }
    }
    data {
      key: "red_flags"
      list_value {
        items {
          string_value: "Quick fix for now, investigate later"
        }
        items {
          string_value: "Just try changing X and see if it works"
        }
        items {
          string_value: "Skip the test, I'll manually verify"
        }
        items {
          string_value: "I don't fully understand but this might work"
        }
        items {
          string_value: "One more fix attempt (when already tried 2+)"
        }
        items {
          string_value: "Each fix reveals a new problem in a different place"
        }
      }
    }
    examples {
      label: "build tight feedback loop"
      language: "bash"
      code: "# Run specific failing test\npytest tests/test_module.py::test_name -v\n# Or scripted repro\npython scripts/repro_bug.py\n# Or high-repetition flaky repro\nfor i in {1..100}; do pytest tests/test_flake.py::test_name -q || break; done"
    }
}
actions {
  id: "multi_component_debug"
  description: "Debug issues in multi-component systems"
  trigger_phrases: "debug multi-component"
  trigger_phrases: "trace data flow"
  trigger_phrases: "system-level debug"
    rules {
      text: "For each component boundary: log what enters, log what exits, verify env/config propagation, check state at each layer"
      priority: HIGH
    }
    rules {
      text: "Run once to gather evidence showing WHERE it breaks, THEN analyze, THEN investigate the failing component"
      priority: HIGH
    }
    rules {
      text: "Trace data flow upstream until you find the source of the bad value — fix at source, not symptom"
      priority: NORMAL
    }
}

guardrails {
  text: "Never propose fixes without completing Phase 1 root cause investigation"
  scope: ALWAYS
}

guardrails {
  text: "If 3+ fixes failed, stop and question the architecture — don't attempt fix #4"
  scope: ALWAYS
}
