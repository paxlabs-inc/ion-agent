meta {
  name: "heartmula"
  version: "1.0.0"
  summary: "HeartMuLa: Suno-like song generation from lyrics + tags"
  author: "community"
  license: "Apache-2.0"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "heartmula"
  keywords: "heartlib"
  keywords: "heartcodec"
  keywords: "music generation"
  keywords: "song generation"
  keywords: "text to music"
  keywords: "ai music"
  keywords: "lyrics to song"
  intents: "generate_music"
  intents: "generate_song"
  intents: "create_song_from_lyrics"
  patterns: "(generate|create|make) .*(music|song|track) .*(lyrics|text|tags)"
  patterns: "heartmula|heartlib|heartcodec"
  patterns: "(open.?source|alternative) .*(suno|music generation)"
}

requires {
  tools {
    name: "terminal"
    required: true
  }
  binaries: "git"
  binaries: "python3"
  binaries: "uv"
}

provides {
  capabilities: "music_generation"
  capabilities: "song_generation"
  capabilities: "lyrics_to_audio"
  output_types: ".mp3"
  output_types: ".wav"
}

actions {
  id: "setup"
  description: "Install HeartMuLa and download model checkpoints"
  trigger_phrases: "set up heartmula"
  trigger_phrases: "install heartmula"
  trigger_phrases: "get heartmula running"
    rules {
      text: "Python 3.10 required — use uv venv --python 3.10"
      priority: CRITICAL
    }
    rules {
      text: "After pip install, upgrade datasets and transformers to fix dependency conflicts"
      priority: CRITICAL
    }
    rules {
      text: "Apply two source patches: RoPE cache fix in modeling_heartmula.py and ignore_mismatched_sizes in music_generation.py"
      priority: CRITICAL
    }
    rules {
      text: "Download three checkpoints: HeartMuLaGen, HeartMuLa-oss-3B, HeartCodec-oss (can run in parallel)"
      priority: HIGH
    }
    rules {
      text: "Minimum 8GB VRAM with --lazy_load true; recommended 16GB+"
      priority: HIGH
    }
    rules {
      text: "No GPU? CPU mode works but expect 30-60+ minutes per song vs ~4 minutes on GPU"
      priority: NORMAL
    }
    data {
      key: "clone_command"
      string_value: "git clone https://github.com/HeartMuLa/heartlib.git && cd heartlib"
    }
    data {
      key: "venv_setup"
      list_value {
        items {
          string_value: "uv venv --python 3.10 .venv"
        }
        items {
          string_value: ". .venv/bin/activate"
        }
        items {
          string_value: "uv pip install -e ."
        }
        items {
          string_value: "uv pip install --upgrade datasets transformers"
        }
      }
    }
    data {
      key: "checkpoint_downloads"
      list_value {
        items {
          string_value: "hf download --local-dir './ckpt' 'HeartMuLa/HeartMuLaGen'"
        }
        items {
          string_value: "hf download --local-dir './ckpt/HeartMuLa-oss-3B' 'HeartMuLa/HeartMuLa-oss-3B-happy-new-year'"
        }
        items {
          string_value: "hf download --local-dir './ckpt/HeartCodec-oss' 'HeartMuLa/HeartCodec-oss-20260123'"
        }
      }
    }
    data {
      key: "patch_1_rope"
      string_value: "In HeartMuLa.setup_caches: reinitialize Llama3ScaledRoPE after reset_caches and before device block"
    }
    data {
      key: "patch_2_codec"
      string_value: "Add ignore_mismatched_sizes=True to both HeartCodec.from_pretrained() calls"
    }
}
actions {
  id: "generate_song"
  description: "Generate a song from lyrics and style tags"
  trigger_phrases: "generate a song"
  trigger_phrases: "create music from lyrics"
  trigger_phrases: "make a song with tags"
  trigger_phrases: "write me a song"
    rules {
      text: "Do NOT use bf16 for HeartCodec — degrades audio quality. Use fp32 (default)"
      priority: CRITICAL
    }
    rules {
      text: "Tags must be comma-separated with no spaces: piano,happy,wedding,synthesizer"
      priority: CRITICAL
    }
    rules {
      text: "Lyrics use bracketed structural tags: [Intro], [Verse], [Chorus], [Bridge], [Outro]"
      priority: HIGH
    }
    rules {
      text: "Default max_audio_length_ms is 240000 (4 minutes)"
      priority: HIGH
    }
    rules {
      text: "Output is MP3, 48kHz stereo, 128kbps"
      priority: HIGH
    }
    rules {
      text: "Tags may be ignored (known issue #90) — lyrics tend to dominate"
      priority: NORMAL
    }
    rules {
      text: "RTF ~1.0 — a 4-minute song takes ~4 minutes to generate on GPU"
      priority: NORMAL
    }
    data {
      key: "generation_command"
      string_value: "python ./examples/run_music_generation.py --model_path=./ckpt --version=3B --lyrics=./lyrics.txt --tags=./tags.txt --save_path=./output.mp3 --lazy_load true"
    }
    data {
      key: "key_parameters"
      map_value {
        entries {
          key: "max_audio_length_ms"
          string_value: "240000 (4 min)"
        }
        entries {
          key: "topk"
          string_value: "50"
        }
        entries {
          key: "temperature"
          string_value: "1.0"
        }
        entries {
          key: "cfg_scale"
          string_value: "1.5"
        }
        entries {
          key: "lazy_load"
          string_value: "false (set true for <16GB VRAM)"
        }
        entries {
          key: "mula_dtype"
          string_value: "bfloat16 (recommended)"
        }
        entries {
          key: "codec_dtype"
          string_value: "float32 (recommended for quality)"
        }
      }
    }
    data {
      key: "multi_gpu_flags"
      string_value: "--mula_device cuda:0 --codec_device cuda:1"
    }
    examples {
      label: "basic song generation"
      language: "bash"
      code: "cd heartlib && . .venv/bin/activate\npython ./examples/run_music_generation.py \\\n  --model_path=./ckpt \\\n  --version=\"3B\" \\\n  --lyrics=\"./assets/lyrics.txt\" \\\n  --tags=\"./assets/tags.txt\" \\\n  --save_path=\"./assets/output.mp3\" \\\n  --lazy_load true"
    }
    examples {
      label: "tags format"
      language: "text"
      code: "piano,happy,wedding,synthesizer,romantic"
    }
    examples {
      label: "lyrics format"
      language: "text"
      code: "[Intro]\n\n[Verse]\nYour lyrics here...\n\n[Chorus]\nChorus lyrics...\n\n[Bridge]\nBridge lyrics...\n\n[Outro]"
    }
}

guardrails {
  text: "Never use bf16 for HeartCodec — always use fp32 for audio quality"
  scope: WRITE_OPS
}

guardrails {
  text: "Triton not available on macOS — GPU acceleration Linux/CUDA only"
  scope: ALWAYS
}

guardrails {
  text: "Suggest cloud GPU (Colab T4, Lambda Labs) if user lacks NVIDIA GPU"
  scope: ALWAYS
}

related {
  name: "audiocraft"
  relationship: "alternative_to"
  description: "Meta AudioCraft for text-to-music (different approach)"
}
