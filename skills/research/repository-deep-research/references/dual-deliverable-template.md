# Dual deliverable template

Use when the user wants both a public-facing explanation and a technical report.

## Part 1 — Non-technical (user-presentable)

Audience: product, partners, non-specialists.

1. **What it is** — one plain sentence + who builds it
2. **Problem it solves** — why chat-only AI fails for this domain
3. **What it feels like to use** — rails/layers in human terms
4. **What you can do** — capabilities list, no jargon dumps
5. **Who it's for**
6. **How the team builds** (if methodology is part of the story)
7. **Status** — maturity in human terms (demo / pre-1.0 / production)
8. **Bottom line** for a non-engineer

Rules:
- Prefer tables of what the user experiences
- Avoid package paths and function names as the spine
- Rephrase technical claims as user-visible consequences

## Part 2 — Technical project report

Audience: the requester / engineer.

1. **Repo facts** — stars, dates, languages, license, contributors
2. **Architecture map** — as implemented
3. **Module-by-module** — role + maturity notes
4. **Deep dives** — named subsystems: layout, core types/control flow, invariants, failure modes, code-truth vs README
5. **Security / ops model** if relevant
6. **Strengths**
7. **Risks / gaps** honest; prefer code-truth
8. **How to evaluate** — concrete next commands/files
9. **Bottom line**

Rules:
- Prefer code-truth callouts when docs disagree with source
- End with optional next deep-dive targets

## Single-audience variants

- Only non-technical → Part 1 + light status
- Only technical → Part 2; still clone and read source
- Full breakdown without audience → default to both parts
