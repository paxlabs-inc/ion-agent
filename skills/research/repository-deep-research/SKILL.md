---
name: repository-deep-research
description: Deep research of GitHub repos - clone and read source not just READMEs; dual non-technical plus technical reports. Includes theory-grounded variant for analyzing codebases against papers/specs.
---

# Repository deep research

Grounded project breakdowns for open-source and monorepo codebases. Prefer code-truth over marketing docs.

## When to use

- Deep research, full breakdown, or technical dive of a GitHub repo
- Non-technical plus technical dual reports
- User names specific modules to read
- User says read the code, not docs, or dont be lazy
- User provides papers/specs and asks to analyze a codebase against them (theory-grounded variant)

## Hard rule

Reading READMEs and ARCHITECTURE.md is orientation only. For a deep dive or named subsystems, clone the repo and read source: entrypoints, control loops, types, store paths, failure modes. Doc-only synthesis fails the bar.

## Workflow

### 1. Orient (cheap pass)

- GitHub API: description, stars, dates, languages, license, topics, contributors, recent commits
  ```bash
  curl -s https://api.github.com/repos/OWNER/REPO -o /tmp/repo_meta.json
  python3 -c "import json; d=json.load(open('/tmp/repo_meta.json')); [print(f'{k}: {d.get(k)}') for k in ['description','stargazers_count','forks_count','language','license','open_issues_count','created_at','updated_at','topics']]"
  # or: curl -s URL | jq '{description, stars: .stargazers_count, ...}'
  ```
- Root README + ARCHITECTURE for the map only
- Tree inventory: top-level dirs, module roots, file counts

Do not stop here.

### 2. Clone and map

```bash
mkdir -p /data/research && cd /data/research
git clone --depth 1 https://github.com/OWNER/REPO.git
```

Inventory modules, Makefile install targets, frozen specs (`.kvx`), skills/docs trees.

### 3. Source-first deep dive

For each load-bearing subsystem the product claims (or the user named):

| Read | Why |
|------|-----|
| `cmd/` / main entry / `cli.py` | Boot wiring, degrade paths |
| Core package facade or loop | Actual control flow |
| Store / journal / writebatch / SQLite | Persistence invariants |
| Failure / incomplete / gate types | Honesty model |
| Package comments + key tests | Contract and enforcement |

Technique: search for language-appropriate symbols, read large files in chunks, follow call sites. Prefer implementation comments and tests over polished docs when they disagree.

**Language-adaptive symbol search:**

| Language | Look for |
|----------|----------|
| Go | `func (a *Agent) Chat`, `WriteBatch`, `Verdict`, `type ... interface` |
| Python | `class AIAgent`, `def run_conversation`, `registry.register()`, `@dataclass`, `ABC` |
| TypeScript | `class Agent {`, `async run(`, `interface Tool`, `export function` |
| Rust | `impl Agent`, `fn run(`, `trait Tool`, `pub fn dispatch` |

For large repos (50+ modules), prioritize by: (1) `wc -l` on candidate files to find the heaviest, (2) read entry points and class definitions first, (3) follow the control flow from there.

Minimum bar when user names agent / memory / security subsystems:

- Agent loop source (not agent README)
- Memory/store source (not memory README)
- Critic / gate / policy modules as source

### 4. Cross-check docs vs code

Call out divergences (design says escalate money tools but classifier patterns empty; completion gate retired while frozen spec still binds; etc.).

### 5. Deliver

- Non-technical user-presentable: product story, layers, capabilities, who-for, status
- Technical report: facts, architecture, module maturity, deep dives, security, strengths, risks, how to evaluate
- Both: dual deliverable (default when user asks for full breakdown + report)

See `references/dual-deliverable-template.md` and `references/source-first-checklist.md`.

## Theory-grounded codebase analysis (advanced variant)

When the user provides a theoretical framework (academic papers, architecture specs, design documents) and asks you to analyze a codebase against it, the methodology changes:

1. **Read the theory FIRST, completely.** Extract full text (web_extract or read_file), including omitted middles. Do not reason about the codebase until the theoretical framework is fully loaded.
2. **Build a correspondence table.** For each core claim/concept in the theory, identify the specific implementation file(s) that embody it. Present this as an explicit mapping (theory concept → code artifact). This is the primary deliverable, not a generic architecture overview.
3. **Look for self-referential patterns.** Systems that embody their own theory (e.g., an agent system that uses its own absence-blindness mitigation on itself) are the highest-signal findings. Trace the reflexivity explicitly.
4. **Evaluate, don't just map.** The theory makes claims. The code either validates, extends, or contradicts them. Call out all three. Where the code goes *further* than the theory (e.g., the premise ledger gates dispatch, which is stronger than Paper 2's Gap-Hunter proposal), say so. Where the code falls short of the theory's claims, say that too.
5. **Name the tensions.** If Paper A says "models are structurally blind to absence" and Paper B says "models recover unnamed centers from scattered signals," the codebase analysis should show exactly where these phenomena diverge — not paper over the tension.
6. **Read named modules as source, not docs.** If the user says "read the neo/ module," read `agent.go`, `premise.go`, `prediction.go`, `taskgraph.go`, `capability.go`, `self_model.go` — the actual control loops and mechanisms. Not the README.

This variant still follows the core workflow (orient → clone → source-first deep dive → cross-check → deliver), but the "orient" phase includes the theoretical framework, and the "deliver" phase is a correspondence analysis rather than a dual report.

## Pitfalls

1. **README-as-research** — module tables and architecture diagrams without source reads. Fails the deep-dive bar.
2. **Ignoring named modules** — if the user names Neo/Cortex/Cassandra (or equivalents), those source trees are mandatory.
3. **Frozen design != runtime** — `.kvx` / design docs can be aspirational or superseded; verify enablement flags and live call sites.
4. **Single audience dump** — mixing marketing into technical reports (or function names into non-technical pieces) reduces usefulness.
5. **Fabricating tool output** — if clone/API fails, say so; never invent stars, LOC, or file contents.
6. **Stopping at stubs** — keep reading until control loop + invariants + failure modes are clear.
7. **Mapping without evaluating** — building a correspondence table without calling out where the code validates, extends, or contradicts the theory. The table is the skeleton; the evaluation is the analysis.
8. **Skipping the theory** — jumping to code analysis without fully reading the theoretical framework first. The theory defines what to look for; without it, you're just doing generic architecture review.
9. **Smoothing tensions** — if the theoretical framework contains internal tensions (e.g., two papers making partially contradictory claims), the codebase analysis should surface where the code resolves or amplifies those tensions, not ignore them.
10. **Assuming dependency/integration relationships** — never claim System A "runs on" or "is built on" System B without verifying in the source code. Two projects with overlapping names, branding, or ecosystem can be completely independent. Check `go.mod`, `package.json`, `requirements.txt`, import statements, and build system before asserting any dependency. This is especially dangerous when comparing agent frameworks — shared terminology (e.g., "agent", "memory", "tools") does NOT imply shared code.
11. **Conflating correction with explanation** — when the user corrects a factual error about a project relationship, fix it silently and move on. Do not re-explain the incorrect claim or justify why you made it. The correction IS the content.
12. **Reading giant files linearly** — when a file is 3,000+ lines, don't read from line 1 and hope to reach the important parts. Strategy: read the first 100-200 lines for imports/docstrings/module-level setup, then search for the class/function definitions you need (`search_files` for class/def keywords), then read those specific sections. For 6K+ line files, `wc -l` on candidate files BEFORE reading to prioritize.
13. **`curl | python3` pipe blocked by security scanners** — many environments flag piping curl output directly to an interpreter as a security risk. Use `curl -s URL -o /tmp/file.json && python3 -c 'import json; ...' < /tmp/file.json`, or pipe to `jq` instead: `curl -s URL | jq '.field'`. The two-step approach also makes debugging easier when the API returns unexpected shapes.

## Related skills

- `github-repo-management` — clone, API, remotes
- `codebase-inspection` — pygount LOC/language metrics (supplemental only)
- `oss-developer-traction` — pre-launch GitHub/dev GTM and star-worthiness (packaging/distribution; not a substitute for source-first dives)

## Variants

- **Competitive analysis** — compare internal system against external competitors with parallel subagent research. See `references/competitive-analysis-pattern.md`.
- **Multi-repo wave-based research** — 3+ equal-priority repos, 3-wave delegation (research → synthesis → architecture design). For tasks like "take the best ideas from X, Y, Z and design something better." See `references/multi-repo-wave-pattern.md`.

14. **Repo URLs can be wrong or private** — when the user provides a URL like `https://github.com/org/repo.git`, verify it exists via the GitHub API before committing to it. If the API returns 404, search GitHub for the org name and repo name separately, try alternate URL patterns (`open-claw` vs `openclaw`), and search the web for the project name + "github." Never assume a URL is correct because the user provided it — repos get renamed, moved, or made private.
15. **Pass file paths, not content, to synthesis agents** — when delegating synthesis work that depends on research reports, have the research agents write to disk (`/data/research/<name>-report.md`) and pass the file paths to synthesis agents. Passing report content inline truncates and bloats context. Subagents can `read_file` themselves.

## Support files

- `references/dual-deliverable-template.md` — dual report structure
- `references/source-first-checklist.md` — bar checklist before finishing
- `references/theory-grounded-analysis.md` — methodology for analyzing a codebase against papers/specs (correspondence tables, self-referential patterns, evaluation framework)
- `references/competitive-analysis-pattern.md` — comparing internal systems against external competitors with parallel subagent research
- `references/multi-repo-wave-pattern.md` — 3-wave parallel research → synthesis → architecture design for 3+ equal-priority repos
