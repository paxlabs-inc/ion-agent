meta {
  name: "research-paper-writing"
  version: "1.1.0"
  summary: "End-to-end ML paper pipeline: experiment design → writing → review → submission for NeurIPS/ICML/ICLR/ACL"
  author: "Orchestra Research"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
}

triggers {
  keywords: "research paper"
  keywords: "ml paper"
  keywords: "neurips"
  keywords: "icml"
  keywords: "iclr"
  keywords: "acl"
  keywords: "latex paper"
  keywords: "experiment design"
  keywords: "paper writing"
  keywords: "bibtex"
  keywords: "rebuttal"
  intents: "write_paper"
  intents: "design_experiments"
  intents: "draft_section"
  intents: "review_paper"
  intents: "prepare_submission"
  intents: "respond_reviews"
  patterns: "(write|draft|start) .*(paper|research|manuscript)"
  patterns: "(neurips|icml|iclr|acl|aaai|colm) .*(paper|submission|template)"
  patterns: "(design|run|execute) .*(experiment|ablation|baseline)"
  patterns: "(self.?review|simulate review|reviewer feedback)"
  patterns: "(submission|camera.?ready|rebuttal) .*(prep|preparation|writing)"
  patterns: "(negative|null) .*(result|finding) .*(paper|write)"
}

requires {
  tools {
    name: "terminal"
    required: true
  }
  tools {
    name: "write_file"
    required: true
  }
  tools {
    name: "read_file"
    required: true
  }
  tools {
    name: "web_search"
    required: false
  }
  tools {
    name: "web_extract"
    required: false
  }
  binaries: "latexmk"
  binaries: "pdflatex"
  binaries: "bibtex"
  binaries: "python3"
  binaries: "git"
}

provides {
  capabilities: "paper_drafting"
  capabilities: "experiment_design"
  capabilities: "citation_management"
  capabilities: "statistical_analysis"
  capabilities: "self_review"
  capabilities: "submission_prep"
  capabilities: "rebuttal_writing"
  output_types: ".tex"
  output_types: ".pdf"
  output_types: ".bib"
  output_types: ".md"
}

actions {
  id: "project_setup"
  description: "Establish workspace, identify contribution, create TODO"
  trigger_phrases: "set up paper project"
  trigger_phrases: "start research paper"
  trigger_phrases: "initialize paper workspace"
    rules {
      text: "Articulate contribution before writing: The What (one sentence), The Why (evidence), The So What (why readers care)"
      priority: CRITICAL
    }
    rules {
      text: "Workspace structure: paper/ (LaTeX), experiments/ (runner scripts), code/ (method), results/ (auto-generated), tasks/ (benchmarks)"
      priority: HIGH
    }
    rules {
      text: "Git discipline: every completed experiment batch gets committed with descriptive message"
      priority: HIGH
    }
    rules {
      text: "Create TODO list as persistent state tracker across sessions"
      priority: HIGH
    }
    rules {
      text: "Estimate compute budget before running: API costs, GPU hours, human eval costs + 30-50% contingency"
      priority: NORMAL
    }
    data {
      key: "workspace_dirs"
      list_value {
        items {
          string_value: "paper/ — LaTeX source, figures, compiled PDFs"
        }
        items {
          string_value: "experiments/ — experiment runner scripts"
        }
        items {
          string_value: "code/ — core method implementation"
        }
        items {
          string_value: "results/ — raw experiment results (auto-generated)"
        }
        items {
          string_value: "tasks/ — task/benchmark definitions"
        }
        items {
          string_value: "human_eval/ — human evaluation materials"
        }
      }
    }
}
actions {
  id: "literature_review"
  description: "Find related work, verify citations, organize by methodology"
  trigger_phrases: "literature review"
  trigger_phrases: "find related work"
  trigger_phrases: "search citations"
  trigger_phrases: "verify references"
    rules {
      text: "NEVER generate BibTeX from memory — ALWAYS fetch programmatically via DOI content negotiation or Semantic Scholar"
      priority: CRITICAL
    }
    rules {
      text: "5-step verification per citation: SEARCH → VERIFY (2+ sources) → RETRIEVE BibTeX → VALIDATE claim → ADD. Fail = [CITATION NEEDED]"
      priority: CRITICAL
    }
    rules {
      text: "Use iterative breadth-then-depth search: Round 1 (4-6 parallel queries) → Round 2 (follow-up from Round 1) → Round 3 (fill gaps)"
      priority: HIGH
    }
    rules {
      text: "Stop when round returns >80% papers already collected — typically 2-3 rounds suffice"
      priority: HIGH
    }
    rules {
      text: "Organize related work by methodology, not paper-by-paper"
      priority: HIGH
    }
    rules {
      text: "Install Exa MCP for real-time academic search: claude mcp add exa -- npx -y mcp-remote 'https://mcp.exa.ai/mcp'"
      priority: NORMAL
    }
    examples {
      label: "fetch BibTeX via DOI"
      language: "python"
      code: "import requests\ndef doi_to_bibtex(doi: str) -> str:\n    response = requests.get(f\"https://doi.org/{doi}\", headers={\"Accept\": \"application/x-bibtex\"})\n    response.raise_for_status()\n    return response.text"
    }
}
actions {
  id: "experiment_design"
  description: "Design experiments that support paper claims with baselines and evaluation protocol"
  trigger_phrases: "design experiments"
  trigger_phrases: "plan experiments"
  trigger_phrases: "create experiment design"
    rules {
      text: "Every experiment must map to a specific paper claim — if it doesn't map, don't run it"
      priority: CRITICAL
    }
    rules {
      text: "Baseline categories: naive (simplest), strong (best existing), ablation (minus one component), compute-matched"
      priority: HIGH
    }
    rules {
      text: "Define before running: metrics + direction, aggregation method, statistical tests, sample sizes"
      priority: HIGH
    }
    rules {
      text: "Save results incrementally after each step for crash recovery"
      priority: HIGH
    }
    rules {
      text: "Keep generation, evaluation, and visualization as separate scripts"
      priority: NORMAL
    }
    data {
      key: "baseline_categories"
      list_value {
        items {
          string_value: "Naive baseline — simplest possible approach"
        }
        items {
          string_value: "Strong baseline — best known existing method"
        }
        items {
          string_value: "Ablation baselines — your method minus one component"
        }
        items {
          string_value: "Compute-matched baselines — same budget, different allocation"
        }
      }
    }
}
actions {
  id: "experiment_execution"
  description: "Run experiments reliably with monitoring and failure recovery"
  trigger_phrases: "run experiments"
  trigger_phrases: "launch experiments"
  trigger_phrases: "start experiment run"
    rules {
      text: "Use nohup for long-running experiments: nohup python run_experiment.py --config config.yaml > logs/exp.log 2>&1 &"
      priority: HIGH
    }
    rules {
      text: "Scripts should check for existing results and skip completed work — makes re-runs safe"
      priority: HIGH
    }
    rules {
      text: "Maintain experiment journal (JSONL): hypothesis, plan, config, status, key_metrics, analysis, next_steps"
      priority: HIGH
    }
    rules {
      text: "Snapshot experiment script after each run: cp experiment.py results/exp_N/experiment_snapshot.py"
      priority: NORMAL
    }
    rules {
      text: "Cron monitoring: check process, read last 30 lines of log, check results. Respond [SILENT] if nothing changed"
      priority: NORMAL
    }
    data {
      key: "failure_recovery"
      map_value {
        entries {
          key: "rate_limit"
          string_value: "Wait then re-run (scripts skip completed work)"
        }
        entries {
          key: "process_crash"
          string_value: "Re-run from last checkpoint"
        }
        entries {
          key: "timeout"
          string_value: "Kill and skip, note in results"
        }
        entries {
          key: "wrong_model"
          string_value: "Fix ID and re-run"
        }
      }
    }
}
actions {
  id: "result_analysis"
  description: "Aggregate results, compute statistics, identify the story"
  trigger_phrases: "analyze results"
  trigger_phrases: "statistical analysis"
  trigger_phrases: "compute significance"
    rules {
      text: "Always compute: error bars (specify std dev vs std error), 95% CIs, pairwise tests (McNemar's), effect sizes (Cohen's d/h)"
      priority: HIGH
    }
    rules {
      text: "Create experiment_log.md before writing: one-sentence contribution, per-experiment claim/result/files, figure inventory"
      priority: HIGH
    }
    rules {
      text: "If hypothesis was wrong: frame paper around analysis of why, or reframe as understanding contribution"
      priority: HIGH
    }
    rules {
      text: "Figures: vector PDF, colorblind-safe palettes (Okabe-Ito), self-contained captions, no title inside figure"
      priority: NORMAL
    }
    rules {
      text: "Tables: booktabs package, bold best value, direction symbols, consistent decimal precision"
      priority: NORMAL
    }
}
actions {
  id: "paper_drafting"
  description: "Write complete publication-ready paper"
  trigger_phrases: "draft paper"
  trigger_phrases: "write paper"
  trigger_phrases: "write introduction"
  trigger_phrases: "write methods"
    rules {
      text: "Paper is a story with one clear contribution — every section serves that narrative"
      priority: CRITICAL
    }
    rules {
      text: "If you cannot state contribution in one sentence, you don't yet have a paper"
      priority: CRITICAL
    }
    rules {
      text: "5-sentence abstract formula: (1) What achieved, (2) Why hard/important, (3) How, (4) Evidence, (5) Best number"
      priority: HIGH
    }
    rules {
      text: "Time allocation: ~equal time on abstract, introduction, figures, and everything else combined"
      priority: HIGH
    }
    rules {
      text: "Figure 1: draft before introduction — forces clarity on core idea"
      priority: HIGH
    }
    rules {
      text: "Introduction: 1-1.5 pages max, contribution bullets, methods start by page 2-3"
      priority: HIGH
    }
    rules {
      text: "Limitations section is REQUIRED by all major conferences"
      priority: HIGH
    }
    rules {
      text: "Two-pass refinement: Pass 1 = write + immediate refine per section; Pass 2 = global refinement with full-paper context"
      priority: NORMAL
    }
    rules {
      text: "Context management: load experiment_log.md (not raw JSONs) as primary writing context"
      priority: NORMAL
    }
    data {
      key: "conference_templates"
      map_value {
        entries {
          key: "neurips"
          string_value: "neurips2025 — 9 pages, main.tex"
        }
        entries {
          key: "icml"
          string_value: "icml2026 — 8 pages, example_paper.tex"
        }
        entries {
          key: "iclr"
          string_value: "iclr2026 — 9 pages, iclr2026_conference.tex"
        }
        entries {
          key: "acl"
          string_value: "acl2025 — 8 pages (long), acl_latex.tex"
        }
        entries {
          key: "aaai"
          string_value: "aaai2026 — 7 pages, aaai2026-unified-template.tex"
        }
      }
    }
}
actions {
  id: "self_review"
  description: "Simulate reviews before submission using ensemble pattern"
  trigger_phrases: "self review"
  trigger_phrases: "simulate reviews"
  trigger_phrases: "paper review"
  trigger_phrases: "reviewer simulation"
    rules {
      text: "Generate N=3-5 independent reviews with different models/temperatures — each reviewer sees only the paper"
      priority: HIGH
    }
    rules {
      text: "Default to negative bias — LLMs have well-documented positivity bias in evaluation"
      priority: HIGH
    }
    rules {
      text: "Meta-review: aggregate N reviews, identify consensus strengths/weaknesses, use averaged scores"
      priority: HIGH
    }
    rules {
      text: "Separate claim verification pass: extract every factual claim, trace to specific experiment, verify number matches result file"
      priority: HIGH
    }
    rules {
      text: "Visual review pass (if VLM available): figure quality, layout issues, grayscale readability"
      priority: NORMAL
    }
    rules {
      text: "Categorize feedback: Critical (must fix) > High (should fix) > Medium (if time) > Low (note for future)"
      priority: NORMAL
    }
}
actions {
  id: "rebuttal_writing"
  description: "Respond to actual reviews post-submission"
  trigger_phrases: "write rebuttal"
  trigger_phrases: "respond to reviews"
  trigger_phrases: "reviewer response"
    rules {
      text: "Address every concern — reviewers notice if you skip one"
      priority: CRITICAL
    }
    rules {
      text: "Format: point-by-point. Lead with strongest responses. Be concise and direct."
      priority: HIGH
    }
    rules {
      text: "Include new results if experiments run during rebuttal period"
      priority: HIGH
    }
    rules {
      text: "Use latexdiff to generate marked-up PDF showing changes"
      priority: HIGH
    }
    rules {
      text: "Never be defensive or dismissive, even of weak criticisms"
      priority: NORMAL
    }
    rules {
      text: "Thank reviewers for specific, actionable feedback (not generic praise)"
      priority: NORMAL
    }
}
actions {
  id: "submission_prep"
  description: "Final formatting, anonymization, and conference-specific requirements"
  trigger_phrases: "prepare submission"
  trigger_phrases: "submission checklist"
  trigger_phrases: "final formatting"
  trigger_phrases: "camera ready"
    rules {
      text: "Anonymization: no author names, no self-citations in first person, no GitHub URLs to personal repos, use Anonymous GitHub"
      priority: CRITICAL
    }
    rules {
      text: "Formatting: page limit respected (excl refs/appendix), vector figures, booktabs tables, no fabricated citations"
      priority: HIGH
    }
    rules {
      text: "Pre-compile validation: chktex for LaTeX errors, verify all \\cite entries exist in .bib, verify figure files exist"
      priority: HIGH
    }
    rules {
      text: "Conference-specific: NeurIPS checklist in appendix, ICML broader impact, ICLR LLM disclosure, ACL limitations section"
      priority: HIGH
    }
    rules {
      text: "Conference conversion: never copy LaTeX preambles — start fresh with target template, copy content only"
      priority: NORMAL
    }
    data {
      key: "page_limits"
      map_value {
        entries {
          key: "neurips"
          string_value: "9 pages (excl refs/appendix)"
        }
        entries {
          key: "icml"
          string_value: "8 pages"
        }
        entries {
          key: "iclr"
          string_value: "9 pages"
        }
        entries {
          key: "acl"
          string_value: "8 pages (long), 4 pages (short)"
        }
        entries {
          key: "aaai"
          string_value: "7 pages"
        }
        entries {
          key: "colm"
          string_value: "9 pages"
        }
      }
    }
}
actions {
  id: "iterative_refinement"
  description: "Choose the right refinement strategy for paper improvement"
  trigger_phrases: "refine paper"
  trigger_phrases: "improve draft"
  trigger_phrases: "iterate on paper"
  trigger_phrases: "autoreason"
    rules {
      text: "Autoreason: best for mid-tier model + constrained task; worst for frontier model + unconstrained task"
      priority: HIGH
    }
    rules {
      text: "Autoreason loop: Critic→Author B→Synthesizer→Judge Panel (3 blind CoT judges, Borda count)→converge at k=2"
      priority: HIGH
    }
    rules {
      text: "Critique-and-revise: best for concrete technical tasks (system design, find-and-fix loop)"
      priority: HIGH
    }
    rules {
      text: "Single pass: best for template-filling tasks or very weak models (Llama 8B class)"
      priority: NORMAL
    }
    rules {
      text: "Provide ground truth data to critic — without it, models hallucinate fabricated ablations and fake CIs"
      priority: NORMAL
    }
    data {
      key: "strategy_selection"
      map_value {
        entries {
          key: "mid_tier_constrained"
          string_value: "Autoreason — maximum value from generation-evaluation gap"
        }
        entries {
          key: "frontier_constrained"
          string_value: "Autoreason — wins 2/3 constrained tasks"
        }
        entries {
          key: "frontier_unconstrained"
          string_value: "Critique-and-revise or single pass"
        }
        entries {
          key: "concrete_technical"
          string_value: "Critique-and-revise — direct find-and-fix"
        }
        entries {
          key: "template_filling"
          string_value: "Single pass — minimal decision space"
        }
        entries {
          key: "very_weak_model"
          string_value: "Single pass — invest in generation quality"
        }
      }
    }
}

guardrails {
  text: "Never hallucinate citations — AI-generated citations have ~40% error rate. Always fetch programmatically."
  scope: ALWAYS
}

guardrails {
  text: "Every experiment must explicitly state which paper claim it supports"
  scope: ALWAYS
}

guardrails {
  text: "Limitations section is REQUIRED by all major conferences"
  scope: ALWAYS
}

guardrails {
  text: "Double-blind: no author names, no first-person self-citations, no personal repo URLs"
  scope: WRITE_OPS
}

related {
  name: "arxiv"
  relationship: "composes_with"
  description: "Search arXiv for papers, generate BibTeX, find related work via Semantic Scholar"
}
