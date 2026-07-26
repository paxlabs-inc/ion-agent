meta {
  name: "llama-cpp"
  version: "2.1.2"
  summary: "llama.cpp local GGUF inference + HuggingFace Hub model discovery"
  author: "Orchestra Research"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "llama.cpp"
  keywords: "gguf"
  keywords: "quantization"
  keywords: "local inference"
  keywords: "cpu inference"
  keywords: "llama-server"
  keywords: "llama-cli"
  keywords: "llama-cpp-python"
  keywords: "gguf model"
  intents: "run_local_model"
  intents: "find_gguf"
  intents: "quantize_model"
  intents: "local_inference"
  intents: "llama_server"
  patterns: "(run|serve|load) .*(gguf|llama|local model)"
  patterns: "llama\\.(cpp|server|cli)"
  patterns: "(q4|q5|q6|q8|iq4|iq3) .*(quant|gguf)"
  patterns: "(cpu|local|edge) .*(inference|llm)"
}

requires {
  tools {
    name: "terminal"
    required: true
  }
  tools {
    name: "webfetch"
    required: false
  }
  binaries: "curl"
}

provides {
  capabilities: "local_llm_inference"
  capabilities: "gguf_model_discovery"
  capabilities: "openai_compatible_server"
  capabilities: "quantization_guidance"
}

actions {
  id: "install"
  description: "Install llama.cpp"
  trigger_phrases: "install llama.cpp"
  trigger_phrases: "set up llama.cpp"
  trigger_phrases: "build llama.cpp"
    rules {
      text: "macOS/Linux: brew install llama.cpp"
      priority: HIGH
    }
    rules {
      text: "Windows: winget install llama.cpp"
      priority: HIGH
    }
    rules {
      text: "From source: git clone, cmake -B build, cmake --build build --config Release"
      priority: NORMAL
    }
    data {
      key: "install_methods"
      map_value {
        entries {
          key: "brew"
          string_value: "brew install llama.cpp"
        }
        entries {
          key: "winget"
          string_value: "winget install llama.cpp"
        }
        entries {
          key: "source"
          string_value: "git clone https://github.com/ggml-org/llama.cpp && cmake -B build && cmake --build build --config Release"
        }
      }
    }
}
actions {
  id: "model_discovery"
  description: "Find GGUF models on HuggingFace Hub"
  trigger_phrases: "find gguf model"
  trigger_phrases: "search llama.cpp models"
  trigger_phrases: "what gguf files exist"
  trigger_phrases: "discover models"
    rules {
      text: "Prefer URL workflows: use HuggingFace local-app view before asking for CLI/scripts"
      priority: CRITICAL
    }
    rules {
      text: "Open repo with ?local-app=llama.cpp view — copy exact command from HF snippet"
      priority: CRITICAL
    }
    rules {
      text: "Use tree API to confirm what GGUF files exist: /api/models/<repo>/tree/main?recursive=true"
      priority: HIGH
    }
    rules {
      text: "Keep repo-specific labels like UD-Q4_K_M or IQ4_NL_XL — don't normalize"
      priority: HIGH
    }
    rules {
      text: "Separate main model files from mmproj-*.gguf projector files and BF16 shards"
      priority: HIGH
    }
    rules {
      text: "Only suggest conversion from Transformers weights if repo lacks GGUF files"
      priority: NORMAL
    }
    data {
      key: "search_urls"
      list_value {
        items {
          string_value: "https://huggingface.co/models?apps=llama.cpp&sort=trending"
        }
        items {
          string_value: "https://huggingface.co/models?search=<term>&apps=llama.cpp&sort=trending"
        }
        items {
          string_value: "https://huggingface.co/<repo>?local-app=llama.cpp"
        }
        items {
          string_value: "https://huggingface.co/api/models/<repo>/tree/main?recursive=true"
        }
      }
    }
    data {
      key: "output_format"
      string_value: "Repo, Recommended quant, llama-server command, other GGUFs, source URLs"
    }
}
actions {
  id: "run_model"
  description: "Run a GGUF model locally with llama-server or llama-cli"
  trigger_phrases: "run gguf model"
  trigger_phrases: "start llama server"
  trigger_phrases: "serve a model"
  trigger_phrases: "run local llm"
    rules {
      text: "Shorthand quant selection: llama-server -hf <repo>:<QUANT>"
      priority: CRITICAL
    }
    rules {
      text: "Exact file fallback: llama-server --hf-repo <repo> --hf-file <filename.gguf>"
      priority: CRITICAL
    }
    rules {
      text: "llama-server provides OpenAI-compatible API on localhost:8080"
      priority: HIGH
    }
    rules {
      text: "llama-cli for one-shot generation, llama-server for persistent API"
      priority: NORMAL
    }
    examples {
      label: "run from hub with quant shorthand"
      language: "bash"
      code: "llama-server -hf bartowski/Llama-3.2-3B-Instruct-GGUF:Q8_0"
    }
    examples {
      label: "run exact file from hub"
      language: "bash"
      code: "llama-server \\\n  --hf-repo microsoft/Phi-3-mini-4k-instruct-gguf \\\n  --hf-file Phi-3-mini-4k-instruct-q4.gguf \\\n  -c 4096"
    }
    examples {
      label: "openai-compatible check"
      language: "bash"
      code: "curl http://localhost:8080/v1/chat/completions \\\n  -H \"Content-Type: application/json\" \\\n  -d '{\"messages\": [{\"role\": \"user\", \"content\": \"Hello!\"}]}'"
    }
}
actions {
  id: "python_bindings"
  description: "Use llama-cpp-python for programmatic inference"
  trigger_phrases: "llama-cpp-python"
  trigger_phrases: "python llama inference"
  trigger_phrases: "python gguf"
    rules {
      text: "pip install llama-cpp-python (CUDA: CMAKE_ARGS='-DGGML_CUDA=on' pip install ...)"
      priority: HIGH
    }
    rules {
      text: "Load from hub: Llama.from_pretrained(repo_id=..., filename='*Q4_K.gguf')"
      priority: HIGH
    }
    rules {
      text: "n_gpu_layers: 0 for CPU, 99 to offload everything to GPU"
      priority: NORMAL
    }
    examples {
      label: "basic python generation"
      language: "python"
      code: "from llama_cpp import Llama\nllm = Llama(model_path=\"./model-q4_k_m.gguf\", n_ctx=4096, n_gpu_layers=35)\nout = llm(\"What is ML?\", max_tokens=256, temperature=0.7)\nprint(out[\"choices\"][0][\"text\"])"
    }
    examples {
      label: "load from hub"
      language: "python"
      code: "from llama_cpp import Llama\nllm = Llama.from_pretrained(\n    repo_id=\"bartowski/Llama-3.2-3B-Instruct-GGUF\",\n    filename=\"*Q4_K_M.gguf\",\n    n_gpu_layers=35,\n)"
    }
}
actions {
  id: "quantization_guidance"
  description: "Help choose the right quantization for hardware"
  trigger_phrases: "which quant"
  trigger_phrases: "choose quantization"
  trigger_phrases: "q4 vs q5"
  trigger_phrases: "best quant for my ram"
    rules {
      text: "Prefer the exact quant HF marks as compatible for user's hardware"
      priority: CRITICAL
    }
    rules {
      text: "General chat: start with Q4_K_M"
      priority: HIGH
    }
    rules {
      text: "Code/technical: prefer Q5_K_M or Q6_K if memory allows"
      priority: HIGH
    }
    rules {
      text: "Tight RAM: Q3_K_M or IQ variants only if user explicitly prioritizes fit over quality"
      priority: HIGH
    }
    rules {
      text: "For multimodal repos, mention mmproj-*.gguf separately — it's not the main model"
      priority: NORMAL
    }
    data {
      key: "quant_heuristics"
      map_value {
        entries {
          key: "general_chat"
          string_value: "Q4_K_M"
        }
        entries {
          key: "code_technical"
          string_value: "Q5_K_M or Q6_K"
        }
        entries {
          key: "tight_ram"
          string_value: "Q3_K_M or IQ variants"
        }
      }
    }
}

guardrails {
  text: "Prefer HF local-app view and tree API over guessing filenames"
  scope: ALWAYS
}

guardrails {
  text: "Don't normalize repo-native quant labels — report exactly as HF shows"
  scope: ALWAYS
}

guardrails {
  text: "Separate main model files from mmproj projector files and BF16 shards"
  scope: ALWAYS
}

related {
  name: "huggingface-hub"
  relationship: "composes_with"
  description: "Use hf CLI for Hub interactions"
}

related {
  name: "serving-llms-vllm"
  relationship: "alternative_to"
  description: "vLLM for high-throughput GPU serving"
}
