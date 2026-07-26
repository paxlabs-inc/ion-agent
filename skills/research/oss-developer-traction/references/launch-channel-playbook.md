# Developer launch channel playbook (condensed)

## Phases

### Phase 0 — Foundation (before any splash)

- One-command or Docker demo; **zero paid keys** on happy path
- README first screen: one-liner → demo media → try command → then architecture
- Discussions on; issue templates; 10–15 outsider-facing good-first-issues
- License FAQ in plain English; optional permissive thin SDK package
- Single hero repo; human release notes (not timestamp-only commits)
- Builders community link (Discord/Telegram) in README

### Phase 1 — Quiet seed (first 100–300 stars)

- Warm network only (real people); **no star farms**
- Awesome-list PRs (agents, language ecosystem, MCP/tools)
- Comparison posts (honest “when X is enough vs when you need us”)
- Substance replies in r/AI_Agents, r/golang, relevant Discords — no spam
- X/Twitter threads that end in **clone/demo**, not only philosophy

### Phase 2 — Launch week

Pick **Tue–Thu** US morning for HN.

| Channel | Asset | Notes |
|---------|--------|------|
| Show HN | Repo + 2–3 technical sentences + first comment (arch, license FAQ, won’t-dos) | Founders answer for 6–8h |
| Reddit | Language + AI agent subs; different titles | No multi-post spam |
| X | Demo video + one design constraint (e.g. closed verbs, receipts) | |
| Product Hunt | Hosted playground/client, not raw monorepo | Often day+1 after HN |
| Short video | 60s NL → IR → receipt (or equivalent) | Reuse in README |

Staff 2–3 people on comments. Silence kills ranking.

### Phase 3 — Compound

- Weekly public buildlog / changelog
- Monthly mini-release with one demo theme
- Merge external examples aggressively
- Eval/benchmark board when credible
- CFPs after runtime story is solid; chain conferences only if Web3 is true ICP

## Show HN skeleton

**Title:** Show HN: {Product} – {concrete technical hook, not “AI platform”}

**Body:**

- Problem with status quo in one sentence  
- 3–4 numbered mechanisms (what it *does*)  
- Demo command  
- Repo URL  
- Explicit ask for feedback on one sharp surface (schema, boundary, API)

**First comment:** diagram or IR example, license plain English, “what we refuse to do”, docs link, invite issues.

## HN sensitivities (agent + web3 products)

- Hostile to hype, vague autonomy claims, crypto-first framing
- Rewards constraints, failure modes, invariants, receipts, refusal paths
- Custom licenses get dissected — FAQ ready, non-defensive
- Star-count bragging invites “bought stars” suspicion; show clones/issues/demos

## Content ROI ranking

1. Demo video of the unique artifact (IR, receipt, replay, …)
2. Deep technical post on the wedge mechanism
3. Honest comparison matrix vs category leaders
4. “What we won’t do” / escalation boundaries
5. Systems post (journal, modules, determinism) for Go/infra audience
6. Agent-built-agent stories only as capability demos, not hype

## Metrics weekly

Stars, forks, unique cloners (if available), README→docs→demo funnel, external issues/PRs, community actives, UTM referrals, release downloads, dependent/“built with” reports.

Ignore bot followers and star spikes with no clones.
