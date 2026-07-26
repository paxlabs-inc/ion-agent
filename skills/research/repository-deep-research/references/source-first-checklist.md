# Source-first checklist

Run before declaring a deep dive complete.

## Intake

- [ ] Repo URL / owner-name identified
- [ ] Named subsystems listed (if user named any — those are mandatory source reads)
- [ ] Deliverable shape: non-technical / technical / dual / other

## Acquire

- [ ] GitHub API: description, stars, dates, languages, license, topics, contributors
- [ ] Shallow clone to a research path
- [ ] Tree inventory: top-level dirs, file counts by extension, module roots

## Docs pass (orientation only)

- [ ] README (and ARCHITECTURE if present) for map
- [ ] License summary
- [ ] Frozen specs / design docs noted as claims, not verified truth

## Source pass (required)

For each critical module:

- [ ] Entry points (cmd/, main, package root)
- [ ] Core control loop or API facade
- [ ] Persistence / state model if any
- [ ] Failure and honesty paths
- [ ] At least one code-truth vs README check

When user says deep dive on agents/memory/security:

- [ ] Agent loop source read (not agent README)
- [ ] Memory/store source read (not memory README)
- [ ] Critic / gate / policy modules read as source

## Synthesis

- [ ] Architecture map matches code, not only docs
- [ ] Divergences called out explicitly
- [ ] Dual deliverable split if requested
- [ ] Honest risks section grounded in code

## Anti-patterns (fail the bar)

- Stopping after README + ARCHITECTURE + module table
- Summarizing a package from its package comment alone without the loop/facade
- Treating frozen design docs as current runtime without enablement checks
- Skipping named subsystems the user listed

## Theory-grounded variant (when user provides papers/specs)

Additional checks before declaring complete:

- [ ] All papers/specs read in full (including omitted middles)
- [ ] Core claims identified (3-5 per paper)
- [ ] Correspondence table built (theory concept → implementation file)
- [ ] Each correspondence evaluated (validates / extends / contradicts)
- [ ] Internal tensions between papers surfaced
- [ ] Self-referential patterns identified (system applying its own theory)
- [ ] Named modules read as source, not docs
