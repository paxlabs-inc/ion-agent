meta {
  name: "python-debugpy"
  version: "1.0.0"
  summary: "Debug Python via pdb REPL and debugpy remote (DAP)."
  author: "Ion Agent"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
}

triggers {
  keywords: "python debug"
  keywords: "pdb"
  keywords: "debugpy"
  keywords: "breakpoint"
  keywords: "post-mortem"
  keywords: "remote debug"
  keywords: "dap"
  intents: "debug_python"
  intents: "pdb_debug"
  intents: "debugpy_attach"
  intents: "post_mortem"
  patterns: "(debug|breakpoint|pdb) .*(python|pytest)"
  patterns: "debugpy"
  patterns: "post.mortem"
}

requires {
  tools {
    name: "terminal"
    required: true
  }
  binaries: "python3"
}

provides {
  capabilities: "python_debugging"
  capabilities: "remote_debugging"
}

actions {
  id: "pdb_local"
  description: "Use pdb/breakpoint() for local Python debugging"
  trigger_phrases: "debug python"
  trigger_phrases: "add breakpoint"
  trigger_phrases: "pdb debug"
  trigger_phrases: "python breakpoint"
    rules {
      text: "Start with breakpoint() — it is the cheapest thing that works"
      priority: HIGH
    }
    rules {
      text: "Don't forget to remove breakpoint() before committing — use rg -n 'breakpoint\\(\\)' --type py"
      priority: HIGH
    }
    rules {
      text: "pdb under pytest-xdist silently does nothing — always use -p no:xdist or -n 0"
      priority: HIGH
    }
    rules {
      text: "The interact command is most powerful — import anything, inspect complex objects, call mutating methods"
      priority: NORMAL
    }
    rules {
      text: "PYTHONBREAKPOINT=0 disables all breakpoint() calls — check env if breakpoint not hitting"
      priority: NORMAL
    }
    data {
      key: "pdb_commands"
      map_value {
        entries {
          key: "next"
          string_value: "n (step over)"
        }
        entries {
          key: "step_into"
          string_value: "s"
        }
        entries {
          key: "return"
          string_value: "r"
        }
        entries {
          key: "continue"
          string_value: "c"
        }
        entries {
          key: "list"
          string_value: "l / ll"
        }
        entries {
          key: "where"
          string_value: "w (stack trace)"
        }
        entries {
          key: "up_down"
          string_value: "u / d"
        }
        entries {
          key: "print"
          string_value: "p expr / pp expr"
        }
        entries {
          key: "breakpoint"
          string_value: "b file:line or b func"
        }
        entries {
          key: "conditional"
          string_value: "b file:line, cond"
        }
        entries {
          key: "interact"
          string_value: "interact (full Python REPL in scope)"
        }
        entries {
          key: "quit"
          string_value: "q"
        }
      }
    }
    examples {
      label: "local breakpoint"
      language: "python"
      code: "def compute(x, y):\n    result = some_helper(x)\n    breakpoint()           # drops into pdb here\n    return result + y"
    }
}
actions {
  id: "debugpy_remote"
  description: "Use debugpy for remote/headless Python debugging"
  trigger_phrases: "remote python debug"
  trigger_phrases: "debugpy attach"
  trigger_phrases: "debug running python"
    rules {
      text: "For long-lived processes use debugpy; for quick terminal debugging use remote-pdb instead"
      priority: HIGH
    }
    rules {
      text: "debugpy.listen blocks only if you also call wait_for_client() — without it, execution continues"
      priority: HIGH
    }
    rules {
      text: "Attach to PID may fail on hardened kernels (ptrace_scope=1) — fix: echo 0 | sudo tee /proc/sys/kernel/yama/ptrace_scope"
      priority: HIGH
    }
    rules {
      text: "For agent-friendly terminal debugging, remote-pdb is cleaner than debugpy DAP"
      priority: NORMAL
    }
    data {
      key: "setup"
      string_value: "pip install debugpy"
    }
    data {
      key: "listen_pattern"
      string_value: "debugpy.listen(('127.0.0.1', 5678)); debugpy.wait_for_client()"
    }
    data {
      key: "launch_pattern"
      string_value: "python -m debugpy --listen 127.0.0.1:5678 --wait-for-client script.py"
    }
    data {
      key: "attach_pattern"
      string_value: "python -m debugpy --listen 127.0.0.1:5678 --pid <pid>"
    }
    examples {
      label: "debugpy source-edit pattern"
      language: "python"
      code: "import debugpy\ndebugpy.listen((\"127.0.0.1\", 5678))\nprint(\"debugpy listening on 5678, waiting for client...\", flush=True)\ndebugpy.wait_for_client()\ndebugpy.breakpoint()"
    }
}
actions {
  id: "post_mortem"
  description: "Post-mortem debugging on Python exceptions"
  trigger_phrases: "post-mortem debug"
  trigger_phrases: "debug crash"
  trigger_phrases: "inspect exception"
    rules {
      text: "Use pdb.post_mortem(tb) in an except block to inspect locals at crash site"
      priority: HIGH
    }
    rules {
      text: "Or wrap whole script: python -m pdb -c continue script.py — pdb catches the crash"
      priority: NORMAL
    }
    examples {
      label: "post-mortem wrapper"
      language: "python"
      code: "import pdb, sys\ntry:\n    run_the_thing()\nexcept Exception:\n    pdb.post_mortem(sys.exc_info()[2])"
    }
}

guardrails {
  text: "Never commit breakpoint()/set_trace()/debugpy.listen calls"
  scope: ALWAYS
}

guardrails {
  text: "Always use -p no:xdist when debugging pytest tests"
  scope: ALWAYS
}
