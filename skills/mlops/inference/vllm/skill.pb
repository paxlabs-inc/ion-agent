meta {
  name: "serving-llms-vllm"
  version: "1.0.0"
  summary: "vLLM: high-throughput LLM serving with OpenAI-compatible API, PagedAttention, quantization"
  author: "Orchestra Research"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
}

triggers {
  keywords: "vllm"
  keywords: "vllm serve"
  keywords: "pagedattention"
  keywords: "continuous batching"
  keywords: "llm serving"
  keywords: "openai api server"
  keywords: "tensor parallelism"
  keywords: "high throughput inference"
  intents: "serve_model"
  intents: "deploy_llm"
  intents: "batch_inference"
  intents: "quantized_serving"
  patterns: "(serve|deploy|run) .*(vllm|vllm|llm server)"
  patterns: "vllm (serve|start|run)"
  patterns: "(openai|compatible) .*(api|server|endpoint)"
  patterns: "(high.?throughput|production) .*(inference|serving)"
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
  capabilities: "llm_serving"
  capabilities: "openai_compatible_api"
  capabilities: "batch_inference"
  capabilities: "quantized_serving"
}

actions {
  id: "install"
  description: "Install vLLM"
  trigger_phrases: "install vllm"
  trigger_phrases: "set up vllm"
    rules {
      text: "pip install vllm"
      priority: HIGH
    }
    rules {
      text: "Requires NVIDIA GPU with CUDA support"
      priority: NORMAL
    }
    data {
      key: "install_command"
      string_value: "pip install vllm"
    }
}
actions {
  id: "offline_inference"
  description: "Run batch inference without server overhead"
  trigger_phrases: "batch inference"
  trigger_phrases: "offline inference"
  trigger_phrases: "process prompts in batch"
    rules {
      text: "Use LLM class for offline inference — no server needed"
      priority: HIGH
    }
    rules {
      text: "vLLM handles batching internally — no need to manually chunk prompts"
      priority: HIGH
    }
    rules {
      text: "Use SamplingParams for temperature, max_tokens, stop sequences"
      priority: NORMAL
    }
    examples {
      label: "basic offline inference"
      language: "python"
      code: "from vllm import LLM, SamplingParams\nllm = LLM(model=\"meta-llama/Llama-3-8B-Instruct\")\nsampling = SamplingParams(temperature=0.7, max_tokens=256)\noutputs = llm.generate([\"Explain quantum computing\"], sampling)\nprint(outputs[0].outputs[0].text)"
    }
}
actions {
  id: "serve_model"
  description: "Deploy a model as an OpenAI-compatible API server"
  trigger_phrases: "serve a model"
  trigger_phrases: "start vllm server"
  trigger_phrases: "deploy llm api"
  trigger_phrases: "openai compatible server"
    rules {
      text: "vllm serve MODEL starts OpenAI-compatible server on port 8000"
      priority: CRITICAL
    }
    rules {
      text: "For 7B-13B: single GPU, gpu-memory-utilization 0.9, max-model-len 8192"
      priority: HIGH
    }
    rules {
      text: "For 30B-70B: use --tensor-parallel-size with multiple GPUs"
      priority: HIGH
    }
    rules {
      text: "Enable --enable-prefix-caching for repeated prompt patterns"
      priority: HIGH
    }
    rules {
      text: "Use Docker: docker run --gpus all -p 8000:8000 vllm/vllm-openai:latest"
      priority: NORMAL
    }
    data {
      key: "server_configs"
      map_value {
        entries {
          key: "small_model"
          string_value: "vllm serve MODEL --gpu-memory-utilization 0.9 --max-model-len 8192 --port 8000"
        }
        entries {
          key: "large_model"
          string_value: "vllm serve MODEL --tensor-parallel-size 4 --quantization awq --port 8000"
        }
        entries {
          key: "production"
          string_value: "vllm serve MODEL --enable-prefix-caching --enable-metrics --metrics-port 9090 --host 0.0.0.0"
        }
      }
    }
    examples {
      label: "serve 7B model"
      language: "bash"
      code: "vllm serve meta-llama/Llama-3-8B-Instruct \\\n  --gpu-memory-utilization 0.9 \\\n  --max-model-len 8192 \\\n  --port 8000"
    }
    examples {
      label: "query with openai sdk"
      language: "python"
      code: "from openai import OpenAI\nclient = OpenAI(base_url='http://localhost:8000/v1', api_key='EMPTY')\nprint(client.chat.completions.create(\n    model='meta-llama/Llama-3-8B-Instruct',\n    messages=[{'role': 'user', 'content': 'Hello!'}]\n).choices[0].message.content)"
    }
}
actions {
  id: "quantized_serving"
  description: "Serve quantized models to fit large models in limited VRAM"
  trigger_phrases: "quantized serving"
  trigger_phrases: "serve quantized model"
  trigger_phrases: "awq model"
  trigger_phrases: "gptq model"
    rules {
      text: "AWQ: best for 70B models, minimal accuracy loss"
      priority: HIGH
    }
    rules {
      text: "GPTQ: wide model support, good compression"
      priority: HIGH
    }
    rules {
      text: "FP8: fastest on H100 GPUs"
      priority: HIGH
    }
    rules {
      text: "Use pre-quantized models from HuggingFace (e.g. TheBloke/Llama-2-70B-AWQ)"
      priority: NORMAL
    }
    data {
      key: "quantization_methods"
      map_value {
        entries {
          key: "awq"
          string_value: "Best for 70B, minimal accuracy loss"
        }
        entries {
          key: "gptq"
          string_value: "Wide support, good compression"
        }
        entries {
          key: "fp8"
          string_value: "Fastest on H100 GPUs"
        }
      }
    }
    examples {
      label: "serve AWQ model"
      language: "bash"
      code: "vllm serve TheBloke/Llama-2-70B-AWQ \\\n  --quantization awq \\\n  --gpu-memory-utilization 0.95"
    }
}
actions {
  id: "monitoring"
  description: "Monitor vLLM server performance metrics"
  trigger_phrases: "monitor vllm"
  trigger_phrases: "vllm metrics"
  trigger_phrases: "check server performance"
    rules {
      text: "Prometheus metrics on port 9090 with --enable-metrics"
      priority: HIGH
    }
    rules {
      text: "Key metrics: vllm:time_to_first_token_seconds, vllm:num_requests_running, vllm:gpu_cache_usage_perc"
      priority: HIGH
    }
    rules {
      text: "Target: TTFT < 500ms, GPU utilization > 80%, throughput > 100 req/sec"
      priority: NORMAL
    }
    data {
      key: "key_metrics"
      list_value {
        items {
          string_value: "vllm:time_to_first_token_seconds — latency"
        }
        items {
          string_value: "vllm:num_requests_running — active requests"
        }
        items {
          string_value: "vllm:gpu_cache_usage_perc — KV cache utilization"
        }
      }
    }
}

guardrails {
  text: "vLLM requires NVIDIA GPU with CUDA — no CPU-only mode"
  scope: ALWAYS
}

guardrails {
  text: "Tensor parallelism should use power-of-2 GPU counts (2, 4, 8)"
  scope: ALWAYS
}

guardrails {
  text: "Always test with load before production deployment"
  scope: WRITE_OPS
}

related {
  name: "llama-cpp"
  relationship: "alternative_to"
  description: "llama.cpp for CPU/edge/single-user inference"
}

related {
  name: "evaluating-llms-harness"
  relationship: "composes_with"
  description: "Use vLLM backend for faster evaluation"
}
