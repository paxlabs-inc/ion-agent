---
name: market-research
description: >
  Compile structured market/competitive intelligence from web sources into actionable reference documents.
  Use for landscape scans, vendor comparisons, pricing research, industry mapping, and reconnaissance tasks
  where the deliverable is a synthesized document, not just an answer.
triggers:
  - "research X market/industry/landscape"
  - "find who does X and compare them"
  - "compile a list of vendors/services/providers for X"
  - "what are the options for X"
  - competitive intelligence or pricing research requests
---

# Market Research & Intelligence Compilation

## Workflow

### 1. Scope the Research
- Identify the key dimensions the user cares about (pricing, features, coverage, payment terms, etc.)
- Note any specific filters (geography, tier, size, etc.)

### 2. Multi-Query Search Strategy
Execute **3–6 independent searches** in parallel, varying the angle:
- Direct name searches ("X listing agents services")
- Specific feature searches ("X accept payment in Y")
- Comparison/ranking searches ("best X 2024 2025 comparison")
- Cost/fee searches ("X pricing fees cost breakdown")

Batch independent searches in a single turn. Each search returns snippets — read them carefully for cross-references and specific data points.

### 3. Deep Extraction (when available)
- Use `web_extract` on promising URLs for full-page content
- **Fallback:** When `web_extract` fails (rate limits, bot blocking), search snippets often contain enough detail — cross-reference across multiple snippet sources to fill gaps
- **Pitfall:** Don't retry `web_extract` more than twice on the same domain in one turn — it's likely rate-limited. Move to alternative search queries instead.

### 4. Cross-Reference & Validate
- Look for the same data point appearing in 2+ independent sources
- Flag single-source claims as "reported" or "estimated"
- Note when information is clearly outdated (check dates in snippets)

### 5. Compile Deliverable
Structure the output as a markdown document with:
- **Summary table(s)** for quick scanning
- **Tiered/grouped organization** (by size, cost, capability, etc.)
- **Explicit confidence markers** (confirmed vs. estimated vs. rumored)
- **Actionable takeaway section** at the end

Save to `/tmp/` with a descriptive filename. The user typically wants:
- A document they can reference/share, not just a chat answer
- Clear structure that lets them find specific items fast
- Honest gaps — don't fill unknowns with plausible-sounding guesses

## Pitfalls
- Don't stop at the first search — surface breadth matters for market research
- Don't fabricate pricing or contact details you didn't find
- Don't omit negative findings ("X does NOT charge fees") — they're often the most useful data points
- When web_extract fails, compile from snippets — the research doesn't stop
