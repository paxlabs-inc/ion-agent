# Style Constraint Enforcement: Verification Recipes

Reusable shell commands and patterns for verifying academic paper compliance.

## Verification Commands

### Word Count
```bash
wc -w /path/to/paper.txt
```

### Em Dash Detection
The em dash is Unicode U+2014, UTF-8 bytes e2 80 94.
```bash
grep -c '—' /path/to/paper.txt
```

### Banned Word Detection (case-insensitive)
Expand the pattern per user's specific banned list.
```bash
grep -i -c -E 'delve|landscape|tapestry|crucial|it is important to note|in the realm of|furthermore|moreover|in conclusion|aforementioned|notably|particularly' /path/to/paper.txt
```

### Find Violating Lines (for fixing)
```bash
grep -n -i -E 'delve|landscape|tapestry|crucial|furthermore|moreover|aforementioned' /path/to/paper.txt
```

### Batch Fix with sed
```bash
sed -i 's/landscape/field/g' /path/to/paper.txt
sed -i 's/furthermore/additionally/g' /path/to/paper.txt
```

## Citation Search Patterns

### arXiv Papers
```
arxiv "exact paper title" author surname year arXiv ID
arxiv "method name" topic arXiv ID year
```

### Verified arXiv IDs for Commonly-Cited ML Papers
- Hinton distillation: 1503.02531
- Chain-of-thought (Wei): 2201.11903
- Scaling laws (Kaplan): 2001.08361
- Chinchilla (Hoffmann): 2203.15556
- Emergent abilities (Wei): 2206.07682
- GPT-4 technical report: 2303.08774
- LoRA (Hu): 2106.09685
- LIMA: 2305.11206
- Self-Instruct: 2212.10560
- Orca: 2306.02707
- Distilling Step-by-Step: 2305.02301
- SCOTT: 2305.01879
- SCoTD (Li): 2306.14050
- On-policy distillation: 2306.13649
- DeepSeek-R1: 2501.12948
- TinyStories: 2305.07759
- Muennighoff scaling: 2305.16264
- QLoRA: 2305.14314

## Common Replacement Map

| Banned Word | Academic Replacements |
|-------------|----------------------|
| delve | examine, explore, investigate, analyze |
| landscape | field, area, domain, space, environment |
| tapestry | (restructure sentence; no direct synonym) |
| crucial | essential, important, necessary, central |
| furthermore | (remove; start new sentence) |
| moreover | (remove; start new sentence) |
| in conclusion | (remove; section heading suffices) |
| it is important to note | (remove; state fact directly) |
| notably | specifically, in particular (or remove) |
| particularly | especially, specifically |
| aforementioned | this, the, (repeat the noun) |
| in the realm of | in, within, for, among |
| additionally | also, or remove and restructure |
| indeed | (often removable; or use in fact) |
