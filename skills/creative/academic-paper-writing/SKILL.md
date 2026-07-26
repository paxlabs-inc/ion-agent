---
name: academic-paper-writing
description: "Write academic papers as plain text (.txt) with strict anti-AI style rules, IEEE structure, and real citations."
version: 1.0.0
author: Ion Agent
license: MIT
metadata:
  ion:
    tags: [writing, academic, papers, anti-ai-slop, citations, arxiv, ieee, plain-text]
    category: creative
    related_skills: [humanizer, arxiv]
---

# Academic Paper Writing (Plain Text)

Compose scholarly research papers as `.txt` documents following IEEE formatting conventions, rigorous anti-AI-tell writing rules, and verified citations. This skill addresses the patterns that the `humanizer` skill does not specifically cover: formal academic structure, voice conventions, mathematical definitions, and citation validation.

**When to load:** Activate when the user requests a research paper, scholarly article, or technical report saved as a `.txt` file — particularly when explicit stylistic constraints or anti-AI requirements are in play.

## Workflow

### 1. Research Citations First

Before drafting any prose, locate genuine arXiv IDs for every paper you intend to reference.

```bash
# Use web_search with targeted queries:
web_search('arXiv "paper title" arXiv ID')
web_search('arXiv author surname keyword 2023 2024')
```

- Confirm each ID follows the correct format: `arXiv:YYMM.NNNNN`
- Favor arXiv IDs rather than DOIs for broader accessibility
- Include a minimum of 15 references in research papers
- **Never invent citations.** If a paper cannot be found, cite a related genuine paper instead, or explicitly acknowledge the gap.

### 2. Write the Paper

Adhere to the IEEE-style structure outlined below. Maintain "we" voice throughout. Incorporate formal definitions and propositions where they strengthen the argument.

### 3. Verify Before Delivery

Apply the verification checklist (below) to the draft. Correct ALL violations prior to saving.

### 4. Save

Write the completed paper to the designated path using `write_file`.

## IEEE-Style Structure for Plain Text

```
Title (may span multiple lines)
(blank line)
Author, Affiliation
Correspondence: email
(blank line)
ABSTRACT
(one paragraph, 150-250 words)
(blank line)
Index Terms: term1, term2, term3
(blank line)
I. INTRODUCTION
II. BACKGROUND (or related sections)
III-X. Body sections (varies by topic)
[Optional: Related Work, Limitations]
X. CONCLUSION
(blank line)
REFERENCES
[numbered bracket entries, IEEE style]
```

References follow bracket-number format: `[1]`, `[2]`, etc.

## Voice Rules

- **Employ "we" consistently.** Never use "I" or passive constructions like "it can be observed that." Write "we observe" instead.
- **No em dashes.** Neither Unicode U+2014 (—) nor double-hyphen (--) in prose. Use commas, parentheses, or hyphens (for compound terms).
  - "X, which is Y" not "X — which is Y"
  - "X (a type of Y)" not "X — a type of Y"
- **Precise claim boundaries.** Resist hedging with "may" or "might" unless genuine uncertainty exists. Favor "X implies Y" over "X may suggest Y."

## Banned AI-Tell Words and Phrases

VIOLATING ANY OF THESE CONSTITUTES A FAILURE. Execute the verification grep before delivering.

### Single Words
delve, landscape, tapestry, crucial, furthermore, moreover, aforementioned, notably, particularly

### Phrases
- "it is important to note"
- "in the realm of"
- "in conclusion" (the section header "X. CONCLUSION" is acceptable; the phrase within running prose is not)

### Why
These serve as statistical fingerprints of LLM-generated text. Their presence signals machine generation to reviewers. The humanizer skill (Pattern #7) covers a broader list; this is the strict subset for academic contexts.

## Verification Checklist

Execute ALL checks before saving the file. Correct any violation.

```bash
FILE="path/to/paper.txt"

# 1. Word count (typically 4000-6000 for research papers)
wc -w "$FILE"

# 2. Banned words (should return nothing)
grep -inE '\bdelve\b|\blandscape\b|\btapestry\b|\bcrucial\b|it is important to note|in the realm of|\bfurthermore\b|\bmoreover\b|in conclusion|\baforementioned\b|\bnotably\b|\bparticularly\b' "$FILE"

# 3. Em dashes — Unicode (should return 0)
grep -cP '\x{2014}' "$FILE"

# 4. Em dashes — ASCII double-hyphen (check manually; table borders with --- are OK)
grep -n '\-\-' "$FILE"

# 5. First-person voice (should not find "I " at sentence start)
grep -nP '^I [a-z]|\. I [a-z]' "$FILE"

# 6. Reference count (typically 15+)
grep -cP '^\[\d+\]' "$FILE"
```

## Formal Definitions and Propositions

Academic papers benefit from structured definitions and propositions. Use this pattern:

```
Definition N (Name). A [thing] is [formal definition] if and only if [condition].

Proposition N. [Statement of the claim].

Proof sketch or intuition follows.
```

Definitions employ "if and only if" for biconditionals. Propositions state claims with precision, using quantifiers where appropriate ("for all", "there exists").

## Common Pitfalls

1. **Writing before researching citations.** Always locate genuine arXiv IDs first. Fabricated citations constitute a hard failure.
2. **Using "landscape" or "tapestry" metaphorically.** These are banned in academic writing. Use "field", "area", "body of work" instead.
3. **Starting the conclusion with "In conclusion."** The section header suffices; repeating it in prose is a banned AI-tell.
4. **Forgetting the "we" voice.** Scan the draft for sentences beginning with "I" or passive constructions.
5. **Using em dashes in tables.** Table separator lines using `---` are acceptable (they are borders, not em dashes). But em dashes in running text are not.
6. **Treating presence in context as memory.** This is the conceptual pitfall the paper "Context Windows Are Not Memory" addresses. Similarly, having a citation in your draft does not mean you verified it exists.

## Related Skills

- **humanizer** — General anti-AI-tell patterns (29 patterns). Load it for prose-level editing beyond the academic subset listed here.
- **arxiv** — Search arXiv papers by keyword, author, category, or ID. Use during the citation research phase.
