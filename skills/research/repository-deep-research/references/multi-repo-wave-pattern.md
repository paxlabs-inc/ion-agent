# Multi-Repo Wave-Based Research Pattern

Research 3+ equal-priority repositories, synthesize cross-comparisons, and produce a novel architecture that combines the best of all. Unlike competitive analysis (internal vs external), all repos have equal weight — the goal is synthesis, not comparison.

## When to use

- User says "research X, Y, Z and design something better than all of them"
- User says "take the best ideas from these repos and combine them"
- Task requires deep source-code understanding of multiple independent codebases
- Final deliverable is a novel design, not a comparison report

## Workflow: 3-Wave Delegation

### Wave 1: Parallel Research (3 subagents, one per repo)

Each subagent gets:
- The repo URL + explicit instruction to verify URL via GitHub API first
- The standard deep-dive methodology (orient → clone → source-first dive → deliver)
- Output path: `/data/research/<repo-name>-report.md`

All three run in parallel. No dependencies between them.

**Template prompt for each subagent:**
```
Deep-research the <REPO_URL> repository.
1. Orient: GitHub API for metadata, README for orientation only
2. Clone and map: git clone --depth 1, inventory structure
3. Source-first deep dive: read actual source, focus on [specific subsystems]
4. Write report to /data/research/<name>-report.md
```

### Wave 2: Parallel Synthesis (2 subagents)

Wait for Wave 1 to complete. Then dispatch two parallel synthesis agents:

**Agent A: Cross-Comparison Matrix**
- Reads all Wave 1 report files from disk
- Produces a dimension-by-dimension comparison
- For each dimension: who does what, winner, ideal pattern
- Output: `/data/research/cross-comparison.md`

**Agent B: Domain-Specific Analysis**
- Reads all Wave 1 report files from disk
- Focuses on the specific quality the user cares about (liveness, security, performance, etc.)
- Identifies what's missing from ALL frameworks
- Output: `/data/research/<domain>-analysis.md`

Both agents read from file paths — never pass report content inline.

### Wave 3: Final Synthesis (you, the parent)

Read both Wave 2 outputs from disk. Produce the final deliverable:
- Novel architecture that takes the best from each framework
- Fills the gaps identified in Wave 2
- Implementation roadmap
- Output: `/data/research/<project-name>-architecture.md`

## Key design decisions

1. **File paths as contracts**: Each wave writes to disk; the next wave reads from disk. This avoids context bloat and lets subagents read full reports (not truncated summaries).

2. **3 subagents max per wave**: The `delegate_task` batch limit is 3 for most users. Wave 1 uses all 3 slots for parallel research. Wave 2 uses 2 for parallel synthesis. Wave 3 is the parent (no delegation needed).

3. **Wave 3 is not delegated**: The final synthesis requires the parent's full context (user preferences, conversation history, cross-wave understanding). Delegating it would lose that context.

4. **Verify repo URLs in Wave 1**: Each research subagent must verify the repo URL via GitHub API before cloning. If 404, search for the correct URL. Never assume user-provided URLs are exact.

## Pitfalls

1. **Passing report content inline** — always pass file paths. Subagents read from disk.
2. **Starting Wave 2 before Wave 1 completes** — the delegation system handles this (batch mode waits for all), but don't try to be clever with partial results.
3. **Delegating Wave 3** — the final synthesis needs parent context. Do it yourself.
4. **More than 3 repos** — if the user names 4+ repos, batch them into groups of 3 and run multiple Wave 1 rounds, or prioritize the most important 3 and research the rest in a follow-up.
5. **Flattening the comparison** — Wave 2 agents should evaluate, not just list. "Winner with justification" for each dimension, not just "X has it, Y doesn't."
6. **Forgetting the domain analysis** — the cross-comparison matrix tells you who's best at what. The domain analysis tells you what's missing from everyone. Both are needed for Wave 3.

## Example session flow

```
User: "Research matrix-core, ion-agent, and openclaw. Take the best 
       agent patterns from all three and design something more advanced."

→ Wave 1: 3 parallel subagents
  - Agent 1: clone matrix-core, read source, write /data/research/matrix-core-report.md
  - Agent 2: clone ion-agent, read source, write /data/research/ion-agent-report.md
  - Agent 3: clone openclaw, read source, write /data/research/openclaw-report.md

→ Wave 2: 2 parallel subagents (after Wave 1 completes)
  - Agent A: read 3 reports, write /data/research/cross-comparison.md (12 dimensions)
  - Agent B: read 3 reports, write /data/research/agent-liveness-analysis.md (liveness gaps)

→ Wave 3: parent reads cross-comparison + liveness analysis
  - Write /data/research/super-agent-architecture.md (novel design)
  - Present summary to user
```

## Duration benchmarks

- Wave 1 (3 parallel): ~90-180s per agent (depends on repo size)
- Wave 2 (2 parallel): ~30-60s per agent (reading + synthesizing)
- Wave 3 (parent): ~60-120s (reading + writing architecture)
- **Total: ~5-8 minutes** for a complete 3-repo research + synthesis pipeline
