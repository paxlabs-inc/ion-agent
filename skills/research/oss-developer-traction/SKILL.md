---
name: oss-developer-traction
description: Research and plan GitHub + developer-community traction for OSS or agent products — baseline metrics, competitor gravity, pre-launch GTM, dual report deliverables.
---

# OSS / developer traction research

Class-level playbook for **earning attention and GitHub traction** (stars, forks, external issues, builders) — especially **pre-launch**. This covers GTM and packaging research, not source-deep architecture research.

For code-truth monorepo breakdowns, use `repository-deep-research` instead (clone + read load-bearing source).

## When to use

- More GitHub stars / forks / dev mindshare before or around launch
- OSS growth, developer GTM, pre-launch traction, "how do we get more eyes"
- Compare agent-framework / tool category gravity and launch playbooks
- Audit a public repo's star-worthiness (README, license, issues, demo friction)

## Hard rules

1. **Measure live, don't guess** — stars, issues, discussions, license, entity type, HN presence from APIs/sites, not memory.
2. **Stars are vanity unless paired with proof** — forks, external issues/PRs, clones, "built with", downloads. Say so in the report.
3. **Never recommend bought stars / engagement farms** — reputation poison on HN and eng Twitter.
4. **Dual deliverable** — chat-facing executive summary **and** a full markdown report file the user can keep.
5. **If product is Matrix/Paxlabs/agent-runtime**, lead recommendations with **reliability / Intent IR / demo friction**, not crypto-first capital-layer marketing, unless the user asks for Web3-only GTM.

## Workflow

### 1. Baseline the product surface (live)

Collect for the hero repo + org/user:

| Signal | How |
|--------|-----|
| stars, forks, watchers, open_issues | `GET /repos/{owner}/{repo}` |
| languages, topics, homepage, created/pushed | same + `/languages` |
| releases/tags | `/releases`, `/tags` |
| community health, issue templates, CoC, license file | `/community/profile` |
| discussions on/off | repo `has_discussions` |
| real issues vs PRs | `/issues` then **drop items with `pull_request`** |
| owner User vs Organization | `/users/{login}` `type` |
| license optics | README badge + LICENSE summary (commercial triggers matter) |
| HN footprint | Algolia: `https://hn.algolia.com/api/v1/search?query=...&tags=story` |
| docs/marketing sites | HTTP probe status + hero messaging |

**Shell tip:** write a small Python probe script to a file and run it. Nested `python3 -c` with heavy quoting fails often; file-based scripts are reliable.

See `references/github-traction-baseline.md`.

### 2. Category gravity (competitors)

Sample 4–8 peers in the same job-to-be-done (not every star chart):

- Orchestration frameworks (LangGraph, CrewAI, AutoGen, OpenAI Agents SDK, …)
- Self-hosted / personal agents (OpenClaw-class, Ion, …)
- DX-first stacks (Mastra-class TS frameworks)
- Spec/coding-agent layers when relevant

For each: approximate stars, wedge one-liner, **what they did for distribution** (Show HN, PH, awesome-lists, one-command demo, permissive license).

Pattern to teach in every report: **devs star what they can run in one sitting and explain in one tweet**. Architecture depth retains; install friction discovers.

### 3. Diagnose blockers (common stack)

Rank what is actually blocking *this* repo:

1. Time-to-first-success (keys, multi-toolchain monorepo, no dry-run)
2. Closed collaboration (0 issues, Discussions off, no templates)
3. License optics ("Other" / custom without plain-English FAQ)
4. Wrong lead narrative (crypto-first on HN; vague "agent platform")
5. Name collisions / SEO
6. Missing thin SDK in the language the audience lives in (e.g. Python/TS vs Go-only)
7. No multi-channel launch history (HN/PH/awesome-lists empty)
8. Diluted hero (many near-zero sibling repos; no single star target)

### 4. Positioning

Force one **primary wedge** for dev channels and one **secondary** for later:

- Primary: job engineers already search for (e.g. consequential agent runtime / Intent IR / durable memory)
- Secondary: ecosystem story (settlement, marketplace) after the runtime is understood

ICP order for **stars** is usually: agent-infra eng → systems/Go → security/fintech → Web3 last (unless product is purely chain tooling).

### 5. Recommendations that ship

Always include a **priority stack of ≤7 moves** and a **30/60/90 plan**. Default high-ROI order for pre-launch agent/OSS:

1. One-command / zero-paid-key demo
2. README first screen + 30–60s demo media
3. Open Discussions + issue templates + seeded good-first-issues
4. License plain-English FAQ (+ optional Apache/MIT thin SDK surface if core is source-available)
5. Thin client in audience language (TS/Python often)
6. Coordinated Show HN only after 1–4
7. Awesome-list PRs + weekly technical content with clone CTA

Channel playbook condensed in `references/launch-channel-playbook.md`.

### 6. Deliver

**Chat summary:** baseline table, blockers, wedge, top 7 moves, 90-day targets.

**Full report file** (e.g. `/data/<product>-prelaunch-dev-traction-report.md`):

1. Executive summary + live metrics vs category  
2. Current footprint (product strengths + repo health)  
3. Competitor / category lessons  
4. Positioning + ICP  
5. GitHub traction system (repo productization)  
6. Distribution phases (quiet seed → launch week → compound)  
7. Product moves for star velocity  
8. Metrics to instrument  
9. Risk register  
10. 30/60/90 checklist  
11. Show HN draft skeleton  
12. Appendix: live snapshot facts + source classes  

Cite live measurements and web sources; label planning star ranges as contingent targets, not forecasts.

## Pitfalls

1. **Doc-only "GTM vibes"** without live GitHub/HN numbers — fails credibility.
2. **Treating stars as the goal** — push external issues, forks, dependents, demos.
3. **Launching before one-command demo** — HN bounce + empty follow-through.
4. **Crypto-first titles on HN** for agent-infra products — expect hostility; lead systems reliability.
5. **Custom license without FAQ** — two-second bounce at "Other".
6. **Paid stars** — never.
7. **Confusing this skill with repository-deep-research** — traction reports need packaging/distribution truth; they do **not** replace source-first dives when the user asks how Neo/Cortex/MCL work internally.
8. **Shell quoting death-spiral** on GitHub API one-liners — use a script file.

## Related skills

- `repository-deep-research` — clone + source for architecture truth
- `github-repo-management` — clone, API, releases, settings
- `github-issues` — issue templates, triage once the funnel is open

## Support files

- `references/github-traction-baseline.md` — API fields and probe pattern
- `references/launch-channel-playbook.md` — HN / Reddit / PH / awesome-lists / content cadence
