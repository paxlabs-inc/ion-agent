meta {
  name: "songsee"
  version: "1.0.0"
  summary: "Audio spectrograms and feature visualizations (mel, chroma, MFCC) via CLI"
  author: "community"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "songsee"
  keywords: "spectrogram"
  keywords: "mel spectrogram"
  keywords: "chroma"
  keywords: "mfcc"
  keywords: "audio visualization"
  keywords: "audio analysis"
  keywords: "tempogram"
  keywords: "spectral flux"
  intents: "visualize_audio"
  intents: "create_spectrogram"
  intents: "analyze_audio"
  intents: "audio_features"
  patterns: "(create|generate|make|show) .*(spectrogram|visualization|mel|chroma|mfcc)"
  patterns: "songsee"
  patterns: "audio (analysis|features|visualization)"
}

requires {
  tools {
    name: "terminal"
    required: true
  }
  binaries: "songsee"
}

provides {
  capabilities: "audio_visualization"
  capabilities: "spectrogram_generation"
  capabilities: "audio_feature_extraction"
  output_types: ".png"
  output_types: ".jpg"
}

actions {
  id: "install"
  description: "Install songsee CLI"
  trigger_phrases: "install songsee"
  trigger_phrases: "set up songsee"
    rules {
      text: "Requires Go installed; install via: go install github.com/steipete/songsee/cmd/songsee@latest"
      priority: HIGH
    }
    rules {
      text: "Optional: ffmpeg for formats beyond WAV/MP3"
      priority: NORMAL
    }
    data {
      key: "install_command"
      string_value: "go install github.com/steipete/songsee/cmd/songsee@latest"
    }
}
actions {
  id: "create_spectrogram"
  description: "Generate spectrograms and multi-panel audio visualizations"
  trigger_phrases: "create a spectrogram"
  trigger_phrases: "visualize audio"
  trigger_phrases: "show mel spectrogram"
  trigger_phrases: "audio analysis chart"
    rules {
      text: "Multiple --viz types are comma-separated and arranged in a grid within a single image"
      priority: HIGH
    }
    rules {
      text: "WAV and MP3 decoded natively; other formats need ffmpeg"
      priority: HIGH
    }
    rules {
      text: "Output images can be fed to vision_analyze for automated audio analysis"
      priority: NORMAL
    }
    rules {
      text: "Use --start and --duration for time slicing"
      priority: NORMAL
    }
    data {
      key: "visualization_types"
      map_value {
        entries {
          key: "spectrogram"
          string_value: "Standard frequency spectrogram"
        }
        entries {
          key: "mel"
          string_value: "Mel-scaled spectrogram"
        }
        entries {
          key: "chroma"
          string_value: "Pitch class distribution"
        }
        entries {
          key: "hpss"
          string_value: "Harmonic/percussive separation"
        }
        entries {
          key: "selfsim"
          string_value: "Self-similarity matrix"
        }
        entries {
          key: "loudness"
          string_value: "Loudness over time"
        }
        entries {
          key: "tempogram"
          string_value: "Tempo estimation"
        }
        entries {
          key: "mfcc"
          string_value: "Mel-frequency cepstral coefficients"
        }
        entries {
          key: "flux"
          string_value: "Spectral flux (onset detection)"
        }
      }
    }
    data {
      key: "common_flags"
      map_value {
        entries {
          key: "viz"
          string_value: "Visualization types (comma-separated)"
        }
        entries {
          key: "style"
          string_value: "Color palette: classic, magma, inferno, viridis, gray"
        }
        entries {
          key: "width"
          string_value: "Output image width"
        }
        entries {
          key: "height"
          string_value: "Output image height"
        }
        entries {
          key: "format"
          string_value: "Output format: jpg or png"
        }
        entries {
          key: "o"
          string_value: "Output file path"
        }
      }
    }
    examples {
      label: "basic spectrogram"
      language: "bash"
      code: "songsee track.mp3 -o spectrogram.png"
    }
    examples {
      label: "multi-panel visualization"
      language: "bash"
      code: "songsee track.mp3 --viz spectrogram,mel,chroma,hpss,selfsim,loudness,tempogram,mfcc,flux"
    }
    examples {
      label: "time slice"
      language: "bash"
      code: "songsee track.mp3 --start 12.5 --duration 8 -o slice.jpg"
    }
    examples {
      label: "from stdin"
      language: "bash"
      code: "cat track.mp3 | songsee - --format png -o out.png"
    }
}

guardrails {
  text: "WAV/MP3 only natively — other formats require ffmpeg"
  scope: ALWAYS
}
