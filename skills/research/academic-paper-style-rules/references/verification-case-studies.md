# Reference Verification Case Studies

Real examples of citation errors caught during paper writing, with detection and fix patterns.

---

## Case Study: Reproducibility Crisis Paper (July 2026)

### Context
Paper: "The Reproducibility Crisis in Agent Evaluation: Why Current Benchmarks Mislead"
Format: Plain .txt, IEEE-style, 19 references with arXiv IDs
Style rules: No em dashes, no AI tells, "we" voice, formal definitions

### Errors Found and Fixed

#### Error 1: Wrong arXiv ID (Reference [11])

**Written**: "S. Biderman et al., 'LoRA Learns Less and Forgets Less,' arXiv:2405.06001"
**Actual**: arXiv:2405.06001 is "LLMC: Benchmarking Large Language Model Quantization"
**Root cause**: Memorized paper title correctly but associated wrong ID
**Detection**: web_extract on https://arxiv.org/abs/2405.06001 showed different title
**Fix**: Replaced with verified contamination paper (arXiv:2311.01964)

**Lesson**: Even when paper title is correct, the arXiv ID can be wrong. Always verify both.

#### Error 2: Wrong First Author (Reference [5])

**Written**: "A. Gasse, A. Lacoste, Q. C. Bui, A. Piche, and D. Vazquez"
**Actual**: First author is Alexandre Drouin, Maxime Gasse is 2nd author
**Root cause**: Confused author order; remembered Gasse as first author
**Detection**: web_search for "WorkArena arXiv 2403.07718 authors" confirmed Drouin as first
**Fix**: Updated to "A. Drouin, M. Gasse, M. Caccia, I. H. Laradji, et al."

**Lesson**: Author order matters. Verify first author specifically.

#### Error 3: Completely Wrong Author List (Reference [15])

**Written**: "D. Gao, L. Ji, L. Zhou, K. Q. Lin, J. Chen, Z. Fan, and M. Z. Shou"
**Actual**: "Dawei Gao, Zitao Li, Weirui Kuang, Xuchen Pan, Daoyuan Chen, Zhijian Ma, Bingchen Qian, Liuyi Yao, Lin Zhu, Chen Cheng, et al."
**Root cause**: Fabricated author list from memory
**Detection**: Semantic Scholar search confirmed actual authors
**Fix**: Updated to correct author list

**Lesson**: For less well-known papers, author lists are especially unreliable. Always verify.

#### Error 4: Missing arXiv ID (Reference [8])

**Written**: "J. Pineau et al., 'Improving Reproducibility...' Journal of Machine Learning Research, vol. 22, no. 164, pp. 1-20, 2021"
**Actual**: arXiv:2003.12206, submitted 2020
**Root cause**: Wrote journal reference from memory, didn't check for arXiv version
**Detection**: web_search confirmed arXiv ID exists
**Fix**: Changed to "arXiv preprint arXiv:2003.12206, 2020"

**Lesson**: Papers often have arXiv versions. Check for them even if you remember the journal ref.

#### Error 5: Missing arXiv ID (Reference [9])

**Written**: "P. Henderson et al., 'Deep Reinforcement Learning that Matters,' in Proceedings of AAAI..."
**Actual**: arXiv:1709.06560
**Root cause**: Wrote conference reference, didn't check for arXiv preprint
**Detection**: web_search confirmed arXiv ID
**Fix**: Changed to "arXiv preprint arXiv:1709.06560, 2017"

### Error Pattern Summary

| Error Type | Count | Detection Method |
|------------|-------|------------------|
| Wrong arXiv ID | 1 | web_extract on arXiv page |
| Wrong author order | 1 | web_search for paper + authors |
| Wrong author list | 1 | Semantic Scholar / web_search |
| Missing arXiv ID | 2 | web_search for paper title + arXiv |

### Batch Verification Results

Verified in 3 batches of 5 URLs each using web_extract:
- Batch 1: GAIA, SWE-bench, AgentBench, WebArena, WorkArena
- Batch 2: OSWorld, VisualWebArena, Pineau, Henderson, Reflexion
- Batch 3: LoRA (wrong), ReAct, Voyager, Multiagent Debate, AgentScope

Additional web_searches needed: 4 (for author verification and arXiv ID lookup)

---

## Verification Efficiency Tips

1. **Batch arXiv page extractions**: web_extract accepts up to 5 URLs per call
2. **Use web_search for author disputes**: Faster than loading full pages
3. **Check HTML version for stubborn cases**: `https://arxiv.org/html/{id}v{version}` sometimes shows authors when abstract page doesn't
4. **Semantic Scholar for author lists**: Often more reliable than arXiv for complete author lists
5. **Verify in order of confidence**: Check well-known papers first (likely correct), then obscure ones (likely wrong)

---

## Style Rule Violations Found

### "Landscape" in Section Heading

**Location**: "II. BACKGROUND / A. The Current Benchmark Landscape"
**Detection**: `grep -in 'landscape' paper.txt`
**Fix**: Changed to "The Current Benchmark Ecosystem"

**Lesson**: Banned words can appear in structural text (headings, titles, index terms), not just body paragraphs. Always grep the entire file.
