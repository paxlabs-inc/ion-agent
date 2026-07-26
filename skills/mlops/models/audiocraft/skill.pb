meta {
  name: "audiocraft-audio-generation"
  version: "1.0.0"
  summary: "AudioCraft: MusicGen text-to-music, AudioGen text-to-sound, EnCodec codec"
  author: "Orchestra Research"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
}

triggers {
  keywords: "audiocraft"
  keywords: "musicgen"
  keywords: "audiogen"
  keywords: "encodec"
  keywords: "text to music"
  keywords: "text to audio"
  keywords: "music generation"
  keywords: "sound effects"
  keywords: "melody conditioning"
  intents: "generate_music"
  intents: "generate_sound_effects"
  intents: "text_to_music"
  intents: "audio_generation"
  intents: "melody_conditioned"
  patterns: "(generate|create|make) .*(music|audio|sound|track) .*(text|description|prompt)"
  patterns: "(audiocraft|musicgen|audiogen|encodec)"
  patterns: "(text.?to.?music|text.?to.?audio|text.?to.?sound)"
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
  capabilities: "text_to_music"
  capabilities: "text_to_sound"
  capabilities: "melody_generation"
  capabilities: "stereo_audio"
  capabilities: "audio_compression"
  output_types: ".wav"
  output_types: ".mp3"
}

actions {
  id: "install"
  description: "Install AudioCraft"
  trigger_phrases: "install audiocraft"
  trigger_phrases: "set up audiocraft"
  trigger_phrases: "install musicgen"
    rules {
      text: "pip install audiocraft (or pip install git+https://github.com/facebookresearch/audiocraft.git)"
      priority: HIGH
    }
    rules {
      text: "Alternative: pip install transformers torch torchaudio for HuggingFace integration"
      priority: HIGH
    }
    rules {
      text: "GPU recommended but not required"
      priority: NORMAL
    }
    data {
      key: "install_commands"
      list_value {
        items {
          string_value: "pip install audiocraft"
        }
        items {
          string_value: "pip install git+https://github.com/facebookresearch/audiocraft.git"
        }
        items {
          string_value: "pip install transformers torch torchaudio"
        }
      }
    }
}
actions {
  id: "generate_music"
  description: "Generate music from text descriptions with MusicGen"
  trigger_phrases: "generate music"
  trigger_phrases: "create a track"
  trigger_phrases: "text to music"
  trigger_phrases: "make a beat"
  trigger_phrases: "compose music"
    rules {
      text: "Default sample rate is 32000 Hz for MusicGen"
      priority: CRITICAL
    }
    rules {
      text: "Duration: 1-120 seconds (default 8). Longer = more VRAM"
      priority: HIGH
    }
    rules {
      text: "cfg_coef controls text adherence: higher = stricter (default 3.0)"
      priority: HIGH
    }
    rules {
      text: "Multiple descriptions can be batched for efficiency"
      priority: HIGH
    }
    rules {
      text: "Model sizes: small (300M, ~4GB VRAM), medium (1.5B, ~8GB), large (3.3B, ~16GB)"
      priority: NORMAL
    }
    data {
      key: "model_variants"
      map_value {
        entries {
          key: "musicgen-small"
          string_value: "300M, quick generation, ~4GB VRAM"
        }
        entries {
          key: "musicgen-medium"
          string_value: "1.5B, balanced, ~8GB VRAM"
        }
        entries {
          key: "musicgen-large"
          string_value: "3.3B, best quality, ~16GB VRAM"
        }
        entries {
          key: "musicgen-melody"
          string_value: "1.5B, text + melody conditioning"
        }
        entries {
          key: "musicgen-stereo"
          string_value: "Stereo output variants"
        }
        entries {
          key: "musicgen-style"
          string_value: "1.5B, style transfer from reference"
        }
      }
    }
    data {
      key: "generation_params"
      map_value {
        entries {
          key: "duration"
          string_value: "1-120 seconds (default 8)"
        }
        entries {
          key: "top_k"
          string_value: "250"
        }
        entries {
          key: "temperature"
          string_value: "1.0"
        }
        entries {
          key: "cfg_coef"
          string_value: "3.0 (text adherence)"
        }
      }
    }
    examples {
      label: "basic text-to-music"
      language: "python"
      code: "import torchaudio\nfrom audiocraft.models import MusicGen\nmodel = MusicGen.get_pretrained('facebook/musicgen-small')\nmodel.set_generation_params(duration=8, top_k=250, temperature=1.0)\nwav = model.generate([\"happy upbeat electronic dance music with synths\"])\ntorchaudio.save(\"output.wav\", wav[0].cpu(), sample_rate=32000)"
    }
    examples {
      label: "huggingface transformers"
      language: "python"
      code: "from transformers import AutoProcessor, MusicgenForConditionalGeneration\nprocessor = AutoProcessor.from_pretrained(\"facebook/musicgen-small\")\nmodel = MusicgenForConditionalGeneration.from_pretrained(\"facebook/musicgen-small\")\ninputs = processor(text=[\"80s pop track with bassy drums\"], padding=True, return_tensors=\"pt\")\naudio_values = model.generate(**inputs, max_new_tokens=256)"
    }
}
actions {
  id: "melody_conditioned"
  description: "Generate music conditioned on a melody input"
  trigger_phrases: "melody conditioned"
  trigger_phrases: "generate with melody"
  trigger_phrases: "music from melody"
  trigger_phrases: "style transfer music"
    rules {
      text: "Use musicgen-melody or musicgen-melody-large model"
      priority: HIGH
    }
    rules {
      text: "Load melody audio with torchaudio.load() and pass to generate_with_chroma()"
      priority: HIGH
    }
    rules {
      text: "For style transfer: use musicgen-style with set_style_conditioner_params()"
      priority: NORMAL
    }
    examples {
      label: "melody-conditioned generation"
      language: "python"
      code: "from audiocraft.models import MusicGen\nimport torchaudio\nmodel = MusicGen.get_pretrained('facebook/musicgen-melody')\nmodel.set_generation_params(duration=30)\nmelody, sr = torchaudio.load(\"melody.wav\")\nwav = model.generate_with_chroma([\"acoustic guitar folk song\"], melody, sr)\ntorchaudio.save(\"output.wav\", wav[0].cpu(), sample_rate=32000)"
    }
}
actions {
  id: "generate_sounds"
  description: "Generate sound effects with AudioGen"
  trigger_phrases: "generate sound effects"
  trigger_phrases: "create ambient audio"
  trigger_phrases: "text to sound"
  trigger_phrases: "sound design"
    rules {
      text: "AudioGen sample rate is 16000 Hz (not 32000 like MusicGen)"
      priority: HIGH
    }
    rules {
      text: "Use facebook/audiogen-medium model"
      priority: HIGH
    }
    rules {
      text: "Good for: thunderstorms, traffic, nature, mechanical sounds"
      priority: NORMAL
    }
    examples {
      label: "sound effect generation"
      language: "python"
      code: "from audiocraft.models import AudioGen\nimport torchaudio\nmodel = AudioGen.get_pretrained('facebook/audiogen-medium')\nmodel.set_generation_params(duration=5)\nwav = model.generate([\"dog barking in a park with birds chirping\"])\ntorchaudio.save(\"sound.wav\", wav[0].cpu(), sample_rate=16000)"
    }
}
actions {
  id: "encodec_compression"
  description: "Compress and decompress audio with EnCodec"
  trigger_phrases: "encodec compression"
  trigger_phrases: "audio codec"
  trigger_phrases: "compress audio"
  trigger_phrases: "audio to tokens"
    rules {
      text: "EnCodec: facebook/encodec_32khz for 32kHz audio"
      priority: HIGH
    }
    rules {
      text: "Encode to tokens, then decode back to audio waveform"
      priority: NORMAL
    }
    examples {
      label: "encodec encode/decode"
      language: "python"
      code: "from audiocraft.models import CompressionModel\nimport torchaudio\nmodel = CompressionModel.get_pretrained('facebook/encodec_32khz')\nwav, sr = torchaudio.load(\"audio.wav\")\nencoded = model.encode(wav.unsqueeze(0))\ncodes = encoded[0]\ndecoded = model.decode(codes)\ntorchaudio.save(\"reconstructed.wav\", decoded[0].cpu(), sample_rate=32000)"
    }
}

guardrails {
  text: "MusicGen sample rate is 32000 Hz; AudioGen is 16000 Hz — don't mix up"
  scope: ALWAYS
}

guardrails {
  text: "Don't use bf16 for AudioCodec if audio quality matters"
  scope: WRITE_OPS
}

guardrails {
  text: "Clear CUDA cache between large batch generations"
  scope: ALWAYS
}

related {
  name: "heartmula"
  relationship: "alternative_to"
  description: "HeartMuLa for lyrics-conditioned song generation"
}
