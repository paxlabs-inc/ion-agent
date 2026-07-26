# Competitive Analysis Pattern

Compare an internal system against external competitors. Extends the source-first deep dive with parallel external research.

## When to use

- User asks "how does X compare to Y/Z" or "analyze the competition"
- User wants to understand relative positioning of an internal system
- Task requires reading internal code AND researching multiple external systems
- User asks for a neutral, fact-based comparison of two or more independent systems

## Workflow

### 1. Deep-dive internal system FIRST

Read the actual source code. Understand the architecture, data model, invariants, and unique features. This is the anchor — everything else is compared against it.

### 2. Verify independence before comparing

Before writing any comparison, VERIFY whether the systems are independent or have dependency relationships:
- Check `go.mod`, `package.json`, `requirements.txt`, `Cargo.toml` for imports
- Check import statements in source code
- Check build system configuration
- Check README for explicit dependency claims
- **Never assume** a relationship exists based on shared terminology, overlapping branding, or ecosystem similarity. Two projects using "agent", "memory", "tools" can be completely independent implementations.

### 3. Dispatch parallel external research

Use `delegate_task` with batch mode (up to 3 subagents) to research external systems concurrently. Each subagent gets:
- Clear goal: "Research [System X] in depth — architecture, memory types, retrieval, persistence, versioning, unique capabilities"
- Context: what to look for (GitHub repos, docs, papers)
- Constraint: "Report back with a detailed technical breakdown"

Group external systems into logical batches (e.g., "MemGPT + Letta", "Mem0 + Zep", "LangChain + others").

### 4. Build comparison matrix

While subagents work, start the comparison table using your internal-system knowledge + training data. Key columns:
- Feature/capability dimensions relevant to the domain
- Each system's approach
- Who's ahead and why

### 5. Synthesize when subagents return

Read full subagent outputs (they may be truncated in the summary). Update the comparison with real data. Structure:
- Executive summary with verdict
- Landscape-at-a-glance table
- Detailed system-by-system comparison (strengths, where internal is better, where competitor is better)
- Unique features no competitor has
- Competitor advantages worth adopting
- Long-term potential assessment
- Concrete next steps

### 6. Deliver

Write to a markdown file. Present key findings inline. Subagent research files are saved automatically — reference them for detail.

## Neutral comparison methodology

When comparing two independent systems (not internal vs competitor), adjust the framing:

1. **No advocacy.** Present facts from both sides equally. "Where X is deeper" and "Where Y is deeper" — not "X wins at..."
2. **Source-grounded claims only.** Every assertion must trace to a specific file, function, class, or module you read. No claims from training data alone.
3. **Architecture dimensions, not feature lists.** Compare design philosophy (horizontal breadth vs vertical depth), not checkbox features.
4. **Explicit "Key Difference" blocks.** After each dimension, write a short paragraph explaining the architectural divergence — not just "X has it, Y doesn't."
5. **Bottom line is honest.** State what each system is good at without ranking. "If you need A: X. If you need B: Y."

## Pitfalls

1. **Don't wait for subagents before starting** — you know more than you think from training data. Start the comparison framework immediately.
2. **Don't trust subagent summaries blindly** — read the full outputs. Summaries truncate important details.
3. **Don't just list features** — evaluate. Say who's ahead, why, and what it means.
4. **Don't flatten the comparison** — some competitors are close on one axis but far on another. Call out the dimensions.
5. **Don't fabricate external system details** — if you don't know, say so and let the subagents research it.
6. **Don't assume dependency relationships** — verify in source before claiming System A runs on System B. Shared terminology does not imply shared code.
7. **Don't re-explain incorrect claims after correction** — fix it and move on.
