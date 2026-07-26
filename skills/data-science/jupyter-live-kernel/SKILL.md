---
name: jupyter-live-kernel
description: "Iterative Python via live Jupyter kernel (hamelnb)."
version: 1.0.0
author: Ion Agent
license: MIT
platforms: [linux, macos, windows]
metadata:
  ion:
    tags: [jupyter, notebook, repl, data-science, exploration, iterative]
    category: data-science
---

# Jupyter Live Kernel (hamelnb)

Provides a **persistent Python REPL** backed by a live Jupyter kernel. Variables
and state are maintained between executions. Reach for this instead of
`execute_code` when you need to accumulate state over time, poke at APIs,
examine DataFrames, or refine complex code iteratively.

## When to Use This vs Other Tools

| Tool | Use When |
|------|----------|
| **This skill** | Step-by-step exploration, shared state across calls, data science, ML, "try something and inspect" |
| `execute_code` | Single-run scripts that need ion tool access (web_search, file ops). No state retained. |
| `terminal` | Shell commands, builds, installs, git, process management |

**Guideline:** If a Jupyter notebook would be your natural choice for the task, reach for this skill.

## Prerequisites

1. **uv** must be present (verify with: `which uv`)
2. **JupyterLab** must be installed: `uv tool install jupyterlab`
3. A running Jupyter server is required (see Setup below)

## Setup

The hamelnb script lives at:
```
SCRIPT="$HOME/.agent-skills/hamelnb/skills/jupyter-live-kernel/scripts/jupyter_live_kernel.py"
```

If you haven't cloned it yet:
```
git clone https://github.com/hamelsmu/hamelnb.git ~/.agent-skills/hamelnb
```

### Starting JupyterLab

First check whether a server is already up:
```
uv run "$SCRIPT" servers
```

If none is running, launch one:
```
jupyter-lab --no-browser --port=8888 --notebook-dir=$HOME/notebooks \
  --IdentityProvider.token='' --ServerApp.password='' > /tmp/jupyter.log 2>&1 &
sleep 3
```

Note: Token and password are disabled so the agent can connect locally. The
server runs without a GUI.

### Creating a Notebook for REPL Use

When you only need a scratch REPL (no pre-existing notebook), set up a minimal
notebook:
```
mkdir -p ~/notebooks
```
Then create a bare .ipynb JSON file with a single empty code cell and open a
kernel session through the Jupyter REST API:
```
curl -s -X POST http://127.0.0.1:8888/api/sessions \
  -H "Content-Type: application/json" \
  -d '{"path":"scratch.ipynb","type":"notebook","name":"scratch.ipynb","kernel":{"name":"python3"}}'
```

## Core Workflow

Every command emits structured JSON. Always append `--compact` to keep token
usage low.

### 1. Discover servers and notebooks

```
uv run "$SCRIPT" servers --compact
uv run "$SCRIPT" notebooks --compact
```

### 2. Execute code (primary operation)

```
uv run "$SCRIPT" execute --path <notebook.ipynb> --code '<python code>' --compact
```

State is preserved between execute calls. Variables, imports, and objects all
persist.

Multi-line snippets work with $'...' quoting:
```
uv run "$SCRIPT" execute --path scratch.ipynb --code $'import os\nfiles = os.listdir(".")\nprint(f"Found {len(files)} files")' --compact
```

### 3. Inspect live variables

```
uv run "$SCRIPT" variables --path <notebook.ipynb> list --compact
uv run "$SCRIPT" variables --path <notebook.ipynb> preview --name <varname> --compact
```

### 4. Edit notebook cells

```
# View current cells
uv run "$SCRIPT" contents --path <notebook.ipynb> --compact

# Insert a new cell
uv run "$SCRIPT" edit --path <notebook.ipynb> insert \
  --at-index <N> --cell-type code --source '<code>' --compact

# Replace cell source (use cell-id from contents output)
uv run "$SCRIPT" edit --path <notebook.ipynb> replace-source \
  --cell-id <id> --source '<new code>' --compact

# Delete a cell
uv run "$SCRIPT" edit --path <notebook.ipynb> delete --cell-id <id> --compact
```

### 5. Verification (restart + run all)

Reserve this for cases where the user explicitly requests a clean run-through,
or you need to confirm the notebook executes end-to-end without errors:

```
uv run "$SCRIPT" restart-run-all --path <notebook.ipynb> --save-outputs --compact
```

## Practical Tips from Experience

1. **The first execution after server start may time out** — the kernel needs a
   brief window to spin up. A single retry usually resolves it.

2. **The kernel's Python is whatever JupyterLab uses** — any extra packages must
   be installed into that environment. Install dependencies into the JupyterLab
   tool environment before running code that needs them.

3. **--compact saves a lot of tokens** — always include it. Without it, JSON
   responses can be extremely large.

4. **For pure REPL work**, spin up a scratch.ipynb and skip cell editing
   entirely. Just call `execute` over and over.

5. **Flag ordering matters** — subcommand flags like `--path` must appear
   BEFORE the sub-subcommand. Example: `variables --path nb.ipynb list`, not
   `variables list --path nb.ipynb`.

6. **If no session exists yet**, you must create one via the REST API (see Setup
   above). The tool cannot run code without an active kernel session.

7. **Errors come back as JSON** with a traceback — inspect the `ename` and
   `evalue` fields to diagnose the problem.

8. **Occasional websocket timeouts** — certain operations may fail on the first
   attempt, particularly after a kernel restart. Try once more before escalating.

## Timeout Defaults

The script defaults to a 30-second timeout per execution. For longer-running
work, pass `--timeout 120`. Use generous timeouts (60 or higher) for initial
setup or computationally heavy operations.
