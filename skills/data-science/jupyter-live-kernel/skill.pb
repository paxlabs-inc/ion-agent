meta {
  name: "jupyter-live-kernel"
  version: "1.0.0"
  summary: "Iterative Python via live Jupyter kernel (hamelnb) — persistent REPL with shared state"
  author: "Ion Agent"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "jupyter"
  keywords: "notebook"
  keywords: "repl"
  keywords: "live kernel"
  keywords: "iterative python"
  keywords: "data science"
  keywords: "hamelnb"
  intents: "run_jupyter"
  intents: "iterative_exploration"
  intents: "data_exploration"
  intents: "live_python"
  patterns: "(jupyter|notebook) .*(kernel|repl|live)"
  patterns: "(iterative|persistent) .*(python|repl|execution)"
  patterns: "hamelnb"
  patterns: "step.*(by step|through) .*(python|code|data)"
}

requires {
  tools {
    name: "terminal"
    required: true
  }
  binaries: "uv"
  binaries: "python3"
}

provides {
  capabilities: "persistent_python_repl"
  capabilities: "jupyter_kernel"
  capabilities: "iterative_execution"
  capabilities: "variable_inspection"
  output_types: ".ipynb"
  output_types: ".json"
}

actions {
  id: "setup_jupyter"
  description: "Set up JupyterLab server and kernel session"
  trigger_phrases: "set up jupyter"
  trigger_phrases: "start jupyter"
  trigger_phrases: "launch kernel"
  trigger_phrases: "jupyter setup"
    rules {
      text: "Prerequisites: uv must be present (which uv), JupyterLab installed (uv tool install jupyterlab)."
      priority: CRITICAL
    }
    rules {
      text: "Script location: $HOME/.agent-skills/hamelnb/skills/jupyter-live-kernel/scripts/jupyter_live_kernel.py"
      priority: CRITICAL
    }
    rules {
      text: "Check if server running: uv run '$SCRIPT' servers. If none, launch: jupyter-lab --no-browser --port=8888 --notebook-dir=$HOME/notebooks --IdentityProvider.token='' --ServerApp.password=''"
      priority: HIGH
    }
    rules {
      text: "Token and password disabled so agent can connect locally. Server runs without GUI."
      priority: HIGH
    }
    rules {
      text: "For scratch REPL: create bare .ipynb JSON, then open kernel session via REST API POST to /api/sessions."
      priority: NORMAL
    }
    data {
      key: "setup_commands"
      map_value {
        entries {
          key: "install"
          string_value: "uv tool install jupyterlab"
        }
        entries {
          key: "check"
          string_value: "uv run \"$SCRIPT\" servers --compact"
        }
        entries {
          key: "launch"
          string_value: "jupyter-lab --no-browser --port=8888 --notebook-dir=$HOME/notebooks --IdentityProvider.token='' --ServerApp.password=''"
        }
        entries {
          key: "session"
          string_value: "curl -s -X POST http://127.0.0.1:8888/api/sessions -H 'Content-Type: application/json' -d '{\"path\":\"scratch.ipynb\",\"type\":\"notebook\",\"kernel\":{\"name\":\"python3\"}}'"
        }
      }
    }
}
actions {
  id: "execute_code"
  description: "Execute Python code in live Jupyter kernel with persistent state"
  trigger_phrases: "run in jupyter"
  trigger_phrases: "execute in kernel"
  trigger_phrases: "jupyter execute"
  trigger_phrases: "run python live"
    rules {
      text: "State persists between execute calls — variables, imports, objects all persist."
      priority: CRITICAL
    }
    rules {
      text: "Always append --compact to keep token usage low. Without it, JSON responses can be extremely large."
      priority: CRITICAL
    }
    rules {
      text: "Multi-line snippets: use $'...' quoting. Example: uv run '$SCRIPT' execute --path scratch.ipynb --code $'import os\\nprint(os.listdir())' --compact"
      priority: HIGH
    }
    rules {
      text: "First execution after server start may time out — kernel needs brief spin-up window. Single retry usually resolves."
      priority: HIGH
    }
    rules {
      text: "Flag ordering matters: --path must appear BEFORE the subcommand. E.g., variables --path nb.ipynb list, not variables list --path nb.ipynb."
      priority: NORMAL
    }
    data {
      key: "execute_command"
      string_value: "uv run \"$SCRIPT\" execute --path <notebook.ipynb> --code '<python code>' --compact"
    }
    data {
      key: "timeout_default"
      int_value: 30
      unit: "seconds"
    }
    examples {
      label: "execute code in kernel"
      language: "bash"
      code: "uv run \"$SCRIPT\" execute --path scratch.ipynb --code $'import os\\nfiles = os.listdir(\".\")\\nprint(f\"Found {len(files)} files\")' --compact"
    }
}
actions {
  id: "inspect_variables"
  description: "Inspect live variables in the kernel"
  trigger_phrases: "inspect variables"
  trigger_phrases: "check state"
  trigger_phrases: "preview variable"
  trigger_phrases: "list variables"
    rules {
      text: "List variables: uv run '$SCRIPT' variables --path <nb.ipynb> list --compact"
      priority: HIGH
    }
    rules {
      text: "Preview variable: uv run '$SCRIPT' variables --path <nb.ipynb> preview --name <varname> --compact"
      priority: HIGH
    }
    rules {
      text: "Errors come back as JSON with traceback — inspect ename and evalue fields."
      priority: NORMAL
    }
}
actions {
  id: "edit_notebook"
  description: "Edit notebook cells — insert, replace, delete"
  trigger_phrases: "edit notebook"
  trigger_phrases: "insert cell"
  trigger_phrases: "replace cell"
  trigger_phrases: "delete cell"
    rules {
      text: "View cells: uv run '$SCRIPT' contents --path <nb.ipynb> --compact"
      priority: HIGH
    }
    rules {
      text: "Insert: uv run '$SCRIPT' edit --path <nb.ipynb> insert --at-index <N> --cell-type code --source '<code>' --compact"
      priority: HIGH
    }
    rules {
      text: "Replace: use cell-id from contents output. Delete: uv run '$SCRIPT' edit --path <nb.ipynb> delete --cell-id <id> --compact"
      priority: NORMAL
    }
}
actions {
  id: "verification"
  description: "Restart kernel and run all cells for clean verification"
  trigger_phrases: "restart and run all"
  trigger_phrases: "verify notebook"
  trigger_phrases: "clean run"
    rules {
      text: "Reserve for explicit user request or end-to-end confirmation: uv run '$SCRIPT' restart-run-all --path <nb.ipynb> --save-outputs --compact"
      priority: HIGH
    }
    rules {
      text: "The kernel's Python is whatever JupyterLab uses — extra packages must be installed into that environment."
      priority: NORMAL
    }
}

guardrails {
  text: "Always use --compact flag — responses without it can be extremely large"
  scope: ALWAYS
}

guardrails {
  text: "Install dependencies into the JupyterLab tool environment before running code that needs them"
  scope: ALWAYS
}

guardrails {
  text: "Occasional websocket timeouts after kernel restart — try once more before escalating"
  scope: ALWAYS
}
