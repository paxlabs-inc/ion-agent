meta {
  name: "node-inspect-debugger"
  version: "1.0.0"
  summary: "Debug Node.js via --inspect + Chrome DevTools Protocol CLI."
  author: "Ion Agent"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "node inspect"
  keywords: "node debugger"
  keywords: "chrome devtools"
  keywords: "cdp"
  keywords: "breakpoints"
  keywords: "vitest debug"
  intents: "debug_node"
  intents: "inspect_node"
  intents: "attach_debugger"
  intents: "heap_snapshot"
  intents: "cpu_profile"
  patterns: "(debug|inspect|breakpoint) .*(node|nodejs|tsx|vitest)"
  patterns: "node.inspect"
  patterns: "--inspect"
}

requires {
  tools {
    name: "terminal"
    required: true
  }
  binaries: "node"
}

provides {
  capabilities: "node_debugging"
  capabilities: "cdp_debugging"
}

actions {
  id: "node_inspect_repl"
  description: "Use node inspect REPL for interactive debugging"
  trigger_phrases: "debug node script"
  trigger_phrases: "node inspect"
  trigger_phrases: "set breakpoint in node"
    rules {
      text: "Prefer node inspect first — it is always available and the REPL is fast"
      priority: HIGH
    }
    rules {
      text: "Use --inspect-brk (not --inspect) when you need to set breakpoints before any code runs"
      priority: HIGH
    }
    rules {
      text: "For TypeScript via tsx: node --inspect-brk --import tsx script.ts"
      priority: HIGH
    }
    rules {
      text: "In repl sub-mode, type any JS expression including closure variables — Ctrl+C exits back to debug>"
      priority: NORMAL
    }
    rules {
      text: "Default port is 9229 — if multiple processes, use --inspect=0 and read actual URL from /json/list"
      priority: NORMAL
    }
    data {
      key: "repl_commands"
      map_value {
        entries {
          key: "continue"
          string_value: "c or cont"
        }
        entries {
          key: "step_over"
          string_value: "n or next"
        }
        entries {
          key: "step_into"
          string_value: "s or step"
        }
        entries {
          key: "step_out"
          string_value: "o or out"
        }
        entries {
          key: "set_breakpoint"
          string_value: "sb('file.js', 42)"
        }
        entries {
          key: "clear_breakpoint"
          string_value: "cb('file.js', 42)"
        }
        entries {
          key: "backtrace"
          string_value: "bt"
        }
        entries {
          key: "repl"
          string_value: "repl (drop into REPL in current scope)"
        }
        entries {
          key: "evaluate"
          string_value: "exec expr"
        }
        entries {
          key: "quit"
          string_value: ".exit"
        }
      }
    }
    examples {
      label: "launch and set breakpoint"
      language: "bash"
      code: "node --inspect-brk script.js &\nnode inspect -p $!\n# debug> sb('script.js', 42)\n# debug> cont"
    }
}
actions {
  id: "node_attach_running"
  description: "Attach debugger to a running Node.js process"
  trigger_phrases: "attach to node process"
  trigger_phrases: "debug running node"
  trigger_phrases: "inspect running node"
    rules {
      text: "Send SIGUSR1 to enable inspector on existing process: kill -SIGUSR1 <pid>"
      priority: HIGH
    }
    rules {
      text: "Attach with: node inspect -p <pid> or node inspect ws://127.0.0.1:9229/<uuid>"
      priority: HIGH
    }
    rules {
      text: "Find WS URL: curl -s http://127.0.0.1:9229/json/list | jq -r '.[0].webSocketDebuggerUrl'"
      priority: NORMAL
    }
    examples {
      label: "attach to TUI process"
      language: "bash"
      code: "TUI_PID=$(pgrep -f 'ui-tui/dist/entry' | head -1)\nkill -SIGUSR1 \"$TUI_PID\"\ncurl -s http://127.0.0.1:9229/json/list | jq -r '.[0].webSocketDebuggerUrl'"
    }
}
actions {
  id: "node_cdp_scripting"
  description: "Programmatic CDP debugging via chrome-remote-interface"
  trigger_phrases: "cdp debug"
  trigger_phrases: "programmatic node debug"
  trigger_phrases: "chrome-remote-interface"
    rules {
      text: "Install chrome-remote-interface to /tmp to avoid dirtying project: mkdir -p /tmp/cdp-tools && cd /tmp/cdp-tools && npm i chrome-remote-interface"
      priority: HIGH
    }
    rules {
      text: "Use NODE_PATH=/tmp/cdp-tools/node_modules to make it available"
      priority: NORMAL
    }
    rules {
      text: "For heap snapshots use HeapProfiler.takeHeapSnapshot; for CPU profiles use Profiler.start/stop"
      priority: NORMAL
    }
    examples {
      label: "CDP driver script"
      language: "javascript"
      code: "const CDP = require('chrome-remote-interface');\n(async () => {\n  const client = await CDP({ port: 9229 });\n  const { Debugger, Runtime } = client;\n  Debugger.paused(async ({ callFrames }) => {\n    const top = callFrames[0];\n    console.log(`PAUSED @ ${top.url}:${top.location.lineNumber + 1}`);\n    await Debugger.resume();\n  });\n  await Debugger.enable();\n  await Runtime.enable();\n  await Runtime.runIfWaitingForDebugger();\n})();"
    }
}

guardrails {
  text: "Never bind --inspect to 0.0.0.0 — always use 127.0.0.1 (default) unless isolated network"
  scope: ALWAYS
}

guardrails {
  text: "Never use --inspect without -brk if you need breakpoints before code runs"
  scope: ALWAYS
}
