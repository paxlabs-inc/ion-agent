---
name: academic-paper-style-rules
title: "Academic Paper Writing with Strict Style Rules"
description: "Write academic papers (.txt, .md) with enforced style constraints: banned words, voice rules, reference verification, IEEE structure."
version: 1.0.0
author: Ion Agent
license: MIT
metadata:
  ion:
    tags: [Research, Paper Writing, Style Rules, Academic Writing, Plain Text, Citations, arXiv]
    category: research
    related_skills: [research-paper-writing, arxiv, humanizer]
    requires_toolsets: [terminal, files, web_extract]

---

# Academic Paper Writing with Strict Style Rules

Compose academic research papers in plain text formats (`.txt`, `.md`) while adhering to rigorous style constraints. This skill spans the entire workflow: outlining the structure, drafting with style compliance, validating references, and performing pre-delivery checks.

**Activate this skill when**: The user requests a paper with explicit style constraints (prohibited vocabulary, voice requirements, formatting limitations) and expects plain-text output.

**Prefer `research-paper-writing` instead when**: Producing LaTeX papers targeting ML conferences (NeurIPS, ICML, ICLR) with automated citation management.

---

## Quick Start

1. Parse style rules from the user's request and build a checklist
2. Outline the paper structure (IEEE-format sections)
3. Draft the paper while respecting all style constraints
4. Batch-verify every reference (arXiv IDs, author lists)
5. Run compliance scans (search for banned words across ALL text, including headings)
6. Deliver the paper alongside a verification summary

---

## Style Rule Enforcement

### Extracting Rules

When the user supplies style rules, produce an explicit checklist:

```
STYLE CHECKLIST:
- Voice: [we/I/one/they]
- Banned words: [word1, word2, ...]
- Banned punctuation: [em dashes, semicolons, etc.]
- Required structure: [IEEE/custom/listed sections]
- Citation format: [arXiv IDs/DOIs/numbered refs]
- Definitions: [formal with propositions/informal]
- Claim boundaries: [precise with qualifiers/approximate]
```

### Common Banned Words (AI Tells)

These words are hallmarks of AI-generated text. Inspect ALL text, including section headings:

| Banned | Replace With |
|--------|--------------|
| delve | explore, examine, investigate |
| landscape | ecosystem, field, area, domain |
| tapestry | remove entirely |
| crucial | essential, necessary, required, critical |
| furthermore | also, additionally, (or just start next point) |
| moreover | also, and, (or restructure) |
| in conclusion | To summarize, We conclude, (or just conclude) |
| it is important to note | remove entirely, state the fact directly |
| in the realm of | in, within, for |
| aforementioned | this, these, the (or name it directly) |
| notably | specifically, in particular, (or remove) |
| particularly | specifically, especially, (or remove) |

### Checking ALL Text Locations

**Critical pitfall**: Banned words often hide in section headings and titles, not just body text.

```bash
# Check entire file including headings
grep -in 'word1\|word2\|word3' paper.txt

# Check for em dashes
grep -c '—' paper.txt  # Should be 0
```

**Example**: "II. BACKGROUND / A. The Current Benchmark Landscape" contains "landscape". Fix: "A. The Current Benchmark Ecosystem".

### Voice Enforcement

When "we" voice is required:
- Use "we" for all actions: "we propose", "we argue", "we define"
- Avoid passive: "it is shown" → "we show"
- Avoid "the author(s)": use "we"
- Avoid "this paper": use "we"

---

## Reference Verification

### The Problem

Plain-text papers lack programmatic citation tooling (no `.bib` files). References are composed from memory, which leads to:
- Incorrect arXiv IDs (paper title correct, ID wrong)
- Incomplete or inaccurate author lists
- Wrong publication years
- Mismatched venues

### Verification Workflow

1. **Draft the paper** with references from memory
2. **Collect all arXiv IDs** cited in the paper
3. **Batch-verify** via web_extract (max 5 URLs per call):
   - Title matches the arXiv ID
   - Author list matches
   - Year matches submission history
4. **Correct discrepancies** before delivery

### Batch Verification Pattern

```python
# Group references into batches of 5
batch_1 = [
    "https://arxiv.org/abs/2311.12983",  # GAIA
    "https://arxiv.org/abs/2310.06770",  # SWE-bench
    "https://arxiv.org/abs/2308.03688",  # AgentBench
    "https://arxiv.org/abs/2307.13854",  # WebArena
    "https://arxiv.org/abs/2403.07718",  # WorkArena
]
# web_extract each batch, compare results against paper
```

### Common Errors Found

| Error Type | Example | Detection |
|------------|---------|-----------|
| Wrong arXiv ID | 2405.06001 cited as LoRA paper (actually LLMC) | Extract page, check title |
| Wrong first author | "Gasse et al." vs actual "Drouin et al." | Check author list on page |
| Missing arXiv ID | Conference ref without arXiv | Search for arXiv version |
| Wrong year | Submission year vs publication year | Check submission history |

### When arXiv Page Doesn't Show Authors

Some arXiv pages don't render author lists in extracted text. Fallback:
1. Search for `"paper title" arxiv authors`
2. Check Semantic Scholar API
3. Check the HTML version: `https://arxiv.org/html/{id}v{version}`

---

## Paper Structure

### IEEE-Style Template (Standard)

```
Title

Author Name(s)
Affiliation
Correspondence: email

ABSTRACT
[Single paragraph, 150-250 words, states problem/approach/results]

INDEX TERMS
[Comma-separated, 5-8 terms]

I. INTRODUCTION
[Problem, motivation, contributions as bullet list, paper organization]

II. BACKGROUND / RELATED WORK
[Existing work, benchmarks, prior art, positioning]

III-V. MAIN SECTIONS
[Framework, metrics, protocol, evaluation, experiments]

VI. RELATED WORK (if separate from background)
[Detailed comparison with prior work]

VII. LIMITATIONS
[Honest assessment: computational cost, scope, assumptions]

VIII. CONCLUSION
[Summary of contributions, future work, invitation to community]

REFERENCES
[Numbered list, each with arXiv ID or DOI]
```

### Word Count Targets

Confirm with user before writing:
- Short: 3,000-4,000 words
- Standard: 4,000-6,000 words
- Long: 6,000-8,000 words

---

## Pre-Delivery Checklist

Run ALL checks before delivering the paper:

```bash
# 1. Word count
wc -w paper.txt

# 2. Em dashes
grep -c '—' paper.txt  # Must be 0

# 3. All banned words (including headings)
grep -in 'delve\|landscape\|tapestry\|crucial\|...' paper.txt

# 4. Reference count
grep -c '^\[' paper.txt  # Should match expected count

# 5. Section headers present
grep -E '^(I\.|II\.|III\.|...)' paper.txt

# 6. Verify "we" voice (if required)
grep -c ' we ' paper.txt  # Should be high
```

### Verification Summary Template

After completing the paper, provide:

```
VERIFICATION RESULTS:
- Word count: X (within Y-Z range)
- Em dashes: 0
- AI tells: None found
- References: N (all verified)
- Sections: All required sections present
- Voice: "we" used throughout
- File: /path/to/file.txt
```

---

## Pitfalls

1. **Section headings contain banned words**: Always grep the entire file, not just body text
2. **arXiv ID mismatch**: Paper title can be correct while ID is wrong; verify both
3. **Author list drift**: Well-known papers can have wrong author lists in memory; always verify
4. **Year confusion**: Submission year vs publication year vs camera-ready year differ
5. **Em dash variants**: Check for both `—` (Unicode) and `--` (ASCII double hyphen)
6. **Transitional phrases**: "Furthermore" and "Moreover" often creep in during long writing sessions; check at end
7. **Voice consistency**: Switch between "we" and passive voice mid-paragraph; do a focused check

---

## Related Skills

- `research-paper-writing`: LaTeX papers for ML conferences with programmatic citations
- `arxiv`: Search arXiv for papers to cite
- `humanizer`: Strip AI-isms from text

## Reference Files

- `references/verification-case-studies.md`: Real examples of citation errors caught during paper writing, with detection and fix patterns (5 error types documented with specific arXiv ID corrections)
