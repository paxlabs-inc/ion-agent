meta {
  name: "evaluating-llms-harness"
  version: "1.0.0"
  summary: "lm-eval-harness: benchmark LLMs across 60+ academic tasks (MMLU, GSM8K, HumanEval)"
  author: "Orchestra Research"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
}

triggers {
  keywords: "lm-eval"
  keywords: "lm-evaluation-harness"
  keywords: "mmlu"
  keywords: "gsm8k"
  keywords: "hellaswag"
  keywords: "humaneval"
  keywords: "truthfulqa"
  keywords: "arc"
  keywords: "benchmark llm"
  keywords: "evaluate llm"
  keywords: "model evaluation"
  intents: "evaluate_model"
  intents: "benchmark_llm"
  intents: "run_benchmark"
  intents: "compare_models"
  patterns: "(evaluate|benchmark|assess) .*(model|llm|llama|mistral)"
  patterns: "(run|execute) .*(mmlu|gsm8k|hellaswag|humaneval|truthfulqa)"
  patterns: "lm.eval"
  patterns: "model (comparison|evaluation|benchmark)"
}

requires {
  tools {
    name: "terminal"
    required: true
  }
  binaries: "pip"
  binaries: "python3"
}

provides {
  capabilities: "llm_benchmarking"
  capabilities: "model_evaluation"
  capabilities: "training_progress_tracking"
  output_types: ".json"
}

actions {
  id: "install"
  description: "Install lm-evaluation-harness"
  trigger_phrases: "install lm-eval"
  trigger_phrases: "set up lm-evaluation-harness"
    rules {
      text: "pip install lm-eval"
      priority: HIGH
    }
    rules {
      text: "For vLLM backend: pip install vllm"
      priority: NORMAL
    }
    data {
      key: "install_command"
      string_value: "pip install lm-eval"
    }
}
actions {
  id: "run_benchmark"
  description: "Run a model against academic benchmarks"
  trigger_phrases: "evaluate this model"
  trigger_phrases: "run mmlu"
  trigger_phrases: "benchmark the model"
  trigger_phrases: "test model on gsm8k"
    rules {
      text: "Standard few-shot count is 5 for most papers (--num_fewshot 5)"
      priority: CRITICAL
    }
    rules {
      text: "Use exact task names: mmlu, gsm8k, hellaswag, truthfulqa, arc_challenge (not mmlu_direct or mmlu_fewshot)"
      priority: CRITICAL
    }
    rules {
      text: "Standard benchmark suite: --tasks mmlu,gsm8k,hellaswag,truthfulqa,arc_challenge"
      priority: HIGH
    }
    rules {
      text: "Use --batch_size auto for optimal throughput"
      priority: HIGH
    }
    rules {
      text: "Save results with --output_path and --log_samples"
      priority: HIGH
    }
    rules {
      text: "HumanEval requires --allow_code_execution flag and pip install human-eval"
      priority: NORMAL
    }
    data {
      key: "core_benchmarks"
      map_value {
        entries {
          key: "mmlu"
          string_value: "57 subjects, multiple choice"
        }
        entries {
          key: "gsm8k"
          string_value: "Grade school math word problems"
        }
        entries {
          key: "hellaswag"
          string_value: "Common sense reasoning"
        }
        entries {
          key: "truthfulqa"
          string_value: "Truthfulness and factuality"
        }
        entries {
          key: "arc_challenge"
          string_value: "AI2 Reasoning Challenge"
        }
        entries {
          key: "humaneval"
          string_value: "Python code generation (164 problems)"
        }
      }
    }
    data {
      key: "quick_benchmarks"
      list_value {
        items {
          string_value: "HellaSwag: ~10 min on 1 GPU"
        }
        items {
          string_value: "GSM8K: ~5 min"
        }
        items {
          string_value: "PIQA: ~2 min"
        }
      }
    }
    data {
      key: "slow_benchmarks"
      list_value {
        items {
          string_value: "MMLU: ~2 hours (57 subjects)"
        }
        items {
          string_value: "HumanEval: requires code execution"
        }
      }
    }
    examples {
      label: "standard HF model evaluation"
      language: "bash"
      code: "lm_eval --model hf \\\n  --model_args pretrained=meta-llama/Llama-2-7b-hf,dtype=bfloat16 \\\n  --tasks mmlu,gsm8k,hellaswag,truthfulqa,arc_challenge \\\n  --num_fewshot 5 \\\n  --batch_size auto \\\n  --output_path results/ \\\n  --log_samples"
    }
    examples {
      label: "quantized model"
      language: "bash"
      code: "lm_eval --model hf \\\n  --model_args pretrained=meta-llama/Llama-2-7b-hf,load_in_4bit=True \\\n  --tasks mmlu \\\n  --device cuda:0"
    }
    examples {
      label: "view available tasks"
      language: "bash"
      code: "lm_eval --tasks list"
    }
}
actions {
  id: "compare_models"
  description: "Compare multiple models on the same benchmarks"
  trigger_phrases: "compare models"
  trigger_phrases: "model comparison"
  trigger_phrases: "which model is better"
    rules {
      text: "Run same benchmark suite on all models with identical fewshot count"
      priority: HIGH
    }
    rules {
      text: "Extract primary metric per task: acc for MMLU, exact_match for GSM8K, acc_norm for HellaSwag"
      priority: HIGH
    }
    rules {
      text: "Generate markdown comparison table with pandas"
      priority: NORMAL
    }
    examples {
      label: "batch evaluation script"
      language: "bash"
      code: "while read model; do\n  lm_eval --model hf \\\n    --model_args pretrained=$model,dtype=bfloat16 \\\n    --tasks mmlu,gsm8k,hellaswag,truthfulqa \\\n    --num_fewshot 5 \\\n    --batch_size auto \\\n    --output_path results/$(echo $model | sed 's/\\//-/g').json\ndone < models.txt"
    }
}
actions {
  id: "track_training"
  description: "Evaluate checkpoints during training to monitor progress"
  trigger_phrases: "track training progress"
  trigger_phrases: "evaluate checkpoints"
  trigger_phrases: "training curve"
    rules {
      text: "Use quick benchmarks (HellaSwag, GSM8K) for frequent evaluation"
      priority: HIGH
    }
    rules {
      text: "Avoid MMLU for frequent eval — too slow (2 hours)"
      priority: HIGH
    }
    rules {
      text: "Use --num_fewshot 0 for speed during training monitoring"
      priority: NORMAL
    }
    examples {
      label: "checkpoint evaluation script"
      language: "bash"
      code: "lm_eval --model hf \\\n  --model_args pretrained=$CHECKPOINT_DIR/checkpoint-$STEP \\\n  --tasks gsm8k,hellaswag \\\n  --num_fewshot 0 \\\n  --batch_size 16 \\\n  --output_path results/step-$STEP.json"
    }
}
actions {
  id: "vllm_evaluation"
  description: "Use vLLM backend for 5-10x faster evaluation"
  trigger_phrases: "faster evaluation"
  trigger_phrases: "vllm evaluation"
  trigger_phrases: "speed up benchmark"
    rules {
      text: "vLLM is 5-10x faster than standard HuggingFace for evaluation"
      priority: HIGH
    }
    rules {
      text: "Use --batch_size auto with vLLM"
      priority: HIGH
    }
    rules {
      text: "Tensor parallelism for multi-GPU: tensor_parallel_size=2"
      priority: NORMAL
    }
    examples {
      label: "vllm evaluation"
      language: "bash"
      code: "lm_eval --model vllm \\\n  --model_args pretrained=meta-llama/Llama-2-7b-hf,tensor_parallel_size=2,dtype=auto,gpu_memory_utilization=0.8 \\\n  --tasks mmlu \\\n  --batch_size auto"
    }
}

guardrails {
  text: "Use exact task names — mmlu, not mmlu_direct or mmlu_fewshot"
  scope: ALWAYS
}

guardrails {
  text: "Standard few-shot is 5 for reproducibility against published results"
  scope: ALWAYS
}

guardrails {
  text: "7B bf16 needs ~16GB VRAM; 8-bit needs ~8GB"
  scope: ALWAYS
}

related {
  name: "weights-and-biases"
  relationship: "composes_with"
  description: "Log benchmark results to W&B for tracking"
}
