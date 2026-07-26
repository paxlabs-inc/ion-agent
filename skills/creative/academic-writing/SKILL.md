---
name: academic-writing
description: "Write academic papers, reports, and technical documents with enforced style constraints and verified real citations."
version: 1.0.0
author: Ion Agent
license: MIT
metadata:
  ion:
    tags: [writing, academic, papers, citations, arxiv, style-constraints, IEEE, research]
    category: creative
    related_skills: [humanizer, research-paper-writing, arxiv]
---

# Academic Writing with Style Constraints

Produce academic papers, technical reports, perspective pieces, and survey documents that adhere to strict stylistic enforcement and verified real citations. Deploy when the user supplies explicit style rules (banned words, punctuation constraints, voice mandates) or requests a paper with genuine references.

## When to use this skill

- User requests an academic/research paper governed by specific style rules
- User supplies a banned word list or demands a particular voice/tone
- User requires authentic citations with real IDs (arXiv, DOI, PubMed)
- User prescribes a structure (IEEE, ACM, numbered sections)
- User defines a word count range
- Composing position papers, perspective pieces, technical reports, or survey papers

## Workflow

### Step 1: Gather Real Citations (before writing)

Never fabricate citation IDs. Search for genuine arXiv IDs, DOIs, or official URLs before including any reference.

- Batch independent citation searches in parallel (multiple web_search calls in one turn)
- Search pattern: `arxiv "exact paper title" author year arXiv ID`
- Confirm each ID resolves to the intended paper on arxiv.org
- Maintain a running reference list: (authors, title, venue, year, ID)

### Step 2: Write with Embedded Constraints

Treat user-supplied style rules as hard constraints. Keep the complete banned word list active in memory throughout the writing process:

**Constraint types:**
- Banned words (AI tells: delve, landscape, tapestry, crucial, furthermore, moreover, etc.)
- Banned punctuation (em dashes, semicolons, curly quotes)
- Required voice (we, first person, passive)
- Citation format (IEEE numbered [1], author-year, footnote)
- Structure (IEEE sections with Roman numerals, specific heading names)
- Word count range
- Claim precision (hedge appropriately, avoid absolutes)

**Writing strategies:**
- For em dashes: use commas, hyphens (-), or parentheses
- For "we" voice: "we argue", "we show", "we propose", "we present"
- Draft all sections, then verify (don't verify mid-draft)
- Include formal definitions and propositions when the topic warrants them

### Step 3: Post-Writing Compliance Verification (always run)

After drafting, run automated checks before delivering. Never skip this step.

```bash
# Word count
wc -w <file>

# Em dash check (UTF-8 em dash character)
grep -c '—' <file>

# Banned word check (case-insensitive, expand list per user's rules)
grep -i -c -E 'delve|landscape|tapestry|crucial|it is important to note|in the realm of|furthermore|moreover|in conclusion|aforementioned|notably|particularly' <file>
```

### Step 4: Fix All Violations

If violations are discovered:
- Use `sed -i` for straightforward word replacements across the file
- Use `patch` for targeted rewrites of complex passages
- Re-run the full verification after each round of fixes
- Repeat until zero violations remain

## Style Quick Reference

### Em Dash Replacements

| Context | Replacement |
|---------|-------------|
| Parenthetical aside | Commas or parentheses |
| Interrupting clause | Commas |
| Range/connection | Hyphen (-) |
| Dramatic pause | Rewrite sentence |

### Common AI Tells and Replacements

| Banned | Use Instead |
|--------|-------------|
| delve | examine, explore, investigate |
| landscape | field, area, domain, space |
| tapestry | (remove, restructure) |
| crucial | essential, important, necessary |
| furthermore | (remove, new sentence) |
| moreover | (remove, new sentence) |
| in conclusion | (remove, just conclude) |
| it is important to note | (remove, state fact directly) |
| notably | (remove, or "specifically") |
| particularly | especially, specifically |
| aforementioned | (remove, use "this" or repeat noun) |
| in the realm of | in, within, for |
| moreover | additionally, also (or remove) |

### IEEE Paper Structure

```
Title
Author, Affiliation, Correspondence

ABSTRACT
[Index Terms]

I. INTRODUCTION
II. BACKGROUND / RELATED WORK
III-X. BODY SECTIONS (numbered Roman numerals)
XI. LIMITATIONS (or CONCLUSION)
XII. CONCLUSION
REFERENCES ([1], [2], ... numbered)
```

## Support Files

Consult `references/style-enforcement-recipes.md` for reusable shell commands (grep, sed patterns), verified arXiv IDs for commonly-cited ML papers, and a complete banned-word replacement map.

## Pitfalls

1. **Fabricating citations.** NEVER invent arXiv IDs, DOIs, or paper titles. A single fake citation destroys credibility. Always verify with web search.

2. **Skipping post-writing verification.** LLMs naturally produce banned words. Always run grep checks, even if you were careful while writing.

3. **Partial fix.** Correcting some violations but missing others. Re-run the full check after each fix round until zero violations.

4. **Em dash vs hyphen.** Em dash (—) is the banned character. Regular hyphen (-) is fine. Verify the grep targets the right UTF-8 character.

5. **Voice drift.** In "we" voice papers, avoid switching to "the authors", "this paper", or "it is shown". Stay in "we" throughout.

6. **Overclaiming.** With "precise claim boundaries", avoid "always", "never", "proves". Use "we argue", "evidence suggests", "this implies", "we observe".

7. **Landscape is an AI tell.** Even in legitimate academic contexts (e.g., "the NLP landscape"), replace with "field", "area", or "domain". Same for "tapestry" even if metaphorically apt.

8. **Writing before gathering citations.** If you write first and try to find real citations after, you may not find papers matching your claims. Gather citations first, then write around what actually exists.
