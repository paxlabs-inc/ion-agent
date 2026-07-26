meta {
  name: "comfyui"
  version: "5.1.0"
  summary: "Generate images, video, and audio with ComfyUI — install, manage nodes/models, run workflows"
  author: "kshitijk4poor, alt-glitch, purzbeats"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "comfyui"
  keywords: "stable diffusion"
  keywords: "SDXL"
  keywords: "Flux"
  keywords: "SD3"
  keywords: "image generation"
  keywords: "video generation"
  keywords: "comfy-cli"
  intents: "generate_image_comfyui"
  intents: "run_comfyui_workflow"
  intents: "manage_comfyui"
  intents: "install_comfyui"
  patterns: "(generate|create) .*(image|video|audio) .*(comfyui|comfy|stable diffusion|SDXL|flux)"
  patterns: "(run|execute) .*(comfyui|comfy) .*(workflow|pipeline)"
  patterns: "(install|setup|launch) .*(comfyui|comfy)"
}

requires {
  env_optional: "COMFY_CLOUD_API_KEY"
  tools {
    name: "terminal"
    required: true
  }
  tools {
    name: "write_file"
    required: false
  }
  binaries: "python3"
  binaries: "curl"
}

provides {
  capabilities: "image_generation"
  capabilities: "video_generation"
  capabilities: "workflow_execution"
  capabilities: "comfyui_management"
  output_types: ".png"
  output_types: ".jpg"
  output_types: ".mp4"
}

actions {
  id: "detect_environment"
  description: "Check what's available and whether hardware can run ComfyUI locally"
  trigger_phrases: "check environment"
  trigger_phrases: "detect hardware"
  trigger_phrases: "what's available"
    rules {
      text: "Run hardware_check.py FIRST to decide local vs Comfy Cloud"
      priority: HIGH
    }
    rules {
      text: "Ask user: local or Cloud? Don't start installing until they answer"
      priority: HIGH
    }
    rules {
      text: "Verdict: ok (local install), marginal (light workflows), cloud (switch to cloud)"
      priority: NORMAL
    }
    data {
      key: "hardware_check_command"
      string_value: "python3 scripts/hardware_check.py --json"
    }
    data {
      key: "verdicts"
      list_value {
        items {
          string_value: "ok"
        }
        items {
          string_value: "marginal"
        }
        items {
          string_value: "cloud"
        }
      }
    }
}
actions {
  id: "setup_comfyui"
  description: "Install and configure ComfyUI"
  trigger_phrases: "install comfyui"
  trigger_phrases: "setup comfyui"
  trigger_phrases: "set up comfy"
    rules {
      text: "Ask Local vs Cloud FIRST — don't start install until user answers"
      priority: CRITICAL
    }
    rules {
      text: "Cloud: sign up at comfy.org, generate API key, set COMFY_CLOUD_API_KEY"
      priority: HIGH
    }
    rules {
      text: "Local: use comfy-cli — pipx install comfy-cli, then comfy install --nvidia"
      priority: HIGH
    }
    rules {
      text: "Default install: ~/comfy/ComfyUI (Linux), ~/Documents/comfy/ComfyUI (macOS/Win)"
      priority: NORMAL
    }
    data {
      key: "install_commands"
      list_value {
        items {
          string_value: "pipx install comfy-cli"
        }
        items {
          string_value: "comfy --skip-prompt install --nvidia"
        }
        items {
          string_value: "comfy launch --background"
        }
      }
    }
    data {
      key: "cloud_setup"
      list_value {
        items {
          string_value: "export COMFY_CLOUD_API_KEY='comfyui-...'"
        }
        items {
          string_value: "--host https://cloud.comfy.org"
        }
      }
    }
}
actions {
  id: "get_workflow"
  description: "Obtain a workflow JSON in API format"
  trigger_phrases: "get workflow"
  trigger_phrases: "load workflow"
  trigger_phrases: "export workflow"
    rules {
      text: "Workflows MUST be in API format (each node has class_type) — editor format is NOT executable"
      priority: CRITICAL
    }
    rules {
      text: "Sources: ComfyUI web UI → Export (API), skill workflows/ directory, community downloads"
      priority: HIGH
    }
    rules {
      text: "extract_schema.py lists controllable params + model deps"
      priority: NORMAL
    }
}
actions {
  id: "run_workflow"
  description: "Execute workflow with parameter injection"
  trigger_phrases: "run workflow"
  trigger_phrases: "generate image"
  trigger_phrases: "execute workflow"
    rules {
      text: "Server must be running — verify with curl http://127.0.0.1:8188/system_stats"
      priority: CRITICAL
    }
    rules {
      text: "Use run_workflow.py for single runs, run_batch.py for sweeps/parallel"
      priority: HIGH
    }
    rules {
      text: "seed: -1 generates fresh random seed per run"
      priority: HIGH
    }
    rules {
      text: "Use --input-image for img2img/inpaint workflows"
      priority: NORMAL
    }
    data {
      key: "run_command"
      string_value: "python3 scripts/run_workflow.py --workflow W --args '{...}' --output-dir ./outputs"
    }
    data {
      key: "batch_command"
      string_value: "python3 scripts/run_batch.py --workflow W --args '{...}' --count 8 --randomize-seed"
    }
    data {
      key: "health_check"
      string_value: "python3 scripts/health_check.py"
    }
}
actions {
  id: "manage_nodes_models"
  description: "Install custom nodes and download models"
  trigger_phrases: "install node"
  trigger_phrases: "download model"
  trigger_phrases: "manage comfyui"
    rules {
      text: "Nodes: comfy node install <name>. Models: comfy model download --url <url> --relative-path models/checkpoints"
      priority: HIGH
    }
    rules {
      text: "check_deps.py reports missing nodes/models; auto_fix_deps.py installs them"
      priority: NORMAL
    }
    data {
      key: "node_commands"
      list_value {
        items {
          string_value: "comfy node install comfyui-impact-pack"
        }
        items {
          string_value: "comfy node install comfyui-animatediff-evolved"
        }
        items {
          string_value: "comfy node install-deps --workflow=workflow.json"
        }
        items {
          string_value: "comfy node update all"
        }
      }
    }
    data {
      key: "model_command"
      string_value: "comfy model download --url URL --relative-path models/checkpoints"
    }
}

guardrails {
  text: "API format required — scripts detect editor format and tell you to re-export"
  scope: ALWAYS
}

guardrails {
  text: "Server must be running for all execution — verify with system_stats"
  scope: ALWAYS
}

guardrails {
  text: "Workflow JSON is arbitrary code — inspect workflows from untrusted sources before running"
  scope: ALWAYS
}

guardrails {
  text: "Path traversal protection: keep safe_path_join on for output filenames"
  scope: ALWAYS
}

related {
  name: "stable-diffusion-image-generation"
}

related {
  name: "image_gen"
}
