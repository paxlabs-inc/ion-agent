meta {
  name: "touchdesigner-mcp"
  version: "1.1.0"
  summary: "Control TouchDesigner via twozero MCP — operators, parameters, wiring, Python execution, real-time visuals"
  author: "kshitijk4poor"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "touchdesigner"
  keywords: "TD"
  keywords: "twozero"
  keywords: "MCP"
  keywords: "real-time visuals"
  keywords: "generative art"
  keywords: "audio reactive"
  keywords: "VJ"
  keywords: "GLSL"
  intents: "control_touchdesigner"
  intents: "create_td_network"
  intents: "audio_reactive"
  intents: "glsl_shader_td"
  patterns: "(touchdesigner|touch designer|TD)"
  patterns: "twozero"
  patterns: "(create|wire|connect) .*(operator|TOP|CHOP|SOP)"
  patterns: "(audio reactive|audio-reactive) .*(touchdesigner|TD|visual)"
}

requires {
  tools {
    name: "terminal"
    required: true
  }
  mcp_servers: "twozero_td"
}

provides {
  capabilities: "touchdesigner_control"
  capabilities: "operator_creation"
  capabilities: "glsl_shaders"
  capabilities: "audio_reactive_visuals"
  capabilities: "video_export"
  output_types: ".mov"
  output_types: ".mp4"
  output_types: ".png"
  output_types: ".glsl"
}

actions {
  id: "discover_environment"
  description: "Discover TD state before building anything"
  trigger_phrases: "discover TD environment"
  trigger_phrases: "check touchdesigner"
  trigger_phrases: "what's in the network"
    rules {
      text: "Call td_get_par_info for each op type BEFORE setting params. Training data is wrong for TD 2025.32."
      priority: CRITICAL
    }
    rules {
      text: "If tdAttributeError fires, STOP. Call td_get_operator_info on the failing node before continuing."
      priority: CRITICAL
    }
    rules {
      text: "Call td_get_hints before building — returns patterns specific to the op type."
      priority: HIGH
    }
    rules {
      text: "Call td_get_focus to see where user is and what's selected. Call td_get_network to see existing structure."
      priority: HIGH
    }
    rules {
      text: "Hub health check: GET http://localhost:40404/mcp returns JSON with PID, project name, TD version."
      priority: NORMAL
    }
}
actions {
  id: "build_network"
  description: "Create operators, set parameters, wire connections"
  trigger_phrases: "build TD network"
  trigger_phrases: "create operators"
  trigger_phrases: "wire up nodes"
    rules {
      text: "Split cleanup and creation into SEPARATE MCP calls. Destroying and recreating same-named nodes in one td_execute_python causes 'Invalid OP object' errors."
      priority: CRITICAL
    }
    rules {
      text: "NEVER hardcode absolute paths in script callbacks. Use me.parent() / scriptOp.parent()."
      priority: CRITICAL
    }
    rules {
      text: "Prefer native MCP tools over td_execute_python. Use td_create_operator, td_set_operator_pars, td_get_errors."
      priority: HIGH
    }
    rules {
      text: "td_create_operator handles viewport positioning automatically. Use td_execute_python for bulk creation or wiring."
      priority: HIGH
    }
    rules {
      text: "Non-Commercial TD caps resolution at 1280x1280. Use outputresolution='custom' and set width/height explicitly."
      priority: NORMAL
    }
    data {
      key: "operator_families"
      map_value {
        entries {
          key: "TOP"
          string_value: "Purple — noiseTOP, glslTOP, compositeTOP, levelTOP, blurTOP, textTOP, nullTOP"
        }
        entries {
          key: "CHOP"
          string_value: "Green — audiofileinCHOP, audiospectrumCHOP, mathCHOP, lfoCHOP, constantCHOP"
        }
        entries {
          key: "SOP"
          string_value: "Blue — gridSOP, sphereSOP, transformSOP, noiseSOP"
        }
        entries {
          key: "DAT"
          string_value: "White — textDAT, tableDAT, scriptDAT, webserverDAT"
        }
        entries {
          key: "MAT"
          string_value: "Yellow — phongMAT, pbrMAT, glslMAT, constMAT"
        }
        entries {
          key: "COMP"
          string_value: "Gray — geometryCOMP, containerCOMP, cameraCOMP, lightCOMP, windowCOMP"
        }
      }
    }
    examples {
      label: "create and wire operators"
      language: "python"
      code: "# Via td_execute_python for bulk operations:\nroot = op('/project1')\nnodes = []\nfor name, optype in [('bg', noiseTOP), ('fx', levelTOP), ('out', nullTOP)]:\n    n = root.create(optype, name)\n    nodes.append(n.path)\nfor i in range(len(nodes)-1):\n    op(nodes[i]).outputConnectors[0].connect(op(nodes[i+1]).inputConnectors[0])"
    }
}
actions {
  id: "glsl_shader"
  description: "Build GLSL shaders in TouchDesigner"
  trigger_phrases: "GLSL shader TD"
  trigger_phrases: "write shader touchdesigner"
  trigger_phrases: "custom shader"
    rules {
      text: "No uTDCurrentTime in GLSL TOP. Use Values page: set value0name='uTime', then expression 'absTime.seconds'."
      priority: CRITICAL
    }
    rules {
      text: "Fallback: Constant TOP in rgba32float format (8-bit clamps to 0-1, freezing shader)."
      priority: HIGH
    }
    rules {
      text: "Large shaders: write GLSL to /tmp/file.glsl, then use td_write_dat or td_execute_python to load."
      priority: HIGH
    }
    rules {
      text: "Vertex/Point access in TD 2025.32: point.P[0], point.P[1], point.P[2] — NOT .x, .y, .z."
      priority: NORMAL
    }
    data {
      key: "time_setup"
      map_value {
        entries {
          key: "step1"
          string_value: "td_set_operator_pars(path, {'value0name': 'uTime'})"
        }
        entries {
          key: "step2"
          string_value: "op(path).par.value0.expr = 'absTime.seconds'"
        }
        entries {
          key: "glsl"
          string_value: "uniform float uTime;"
        }
      }
    }
}
actions {
  id: "audio_reactive"
  description: "Build audio-reactive GLSL visuals with proven signal chain"
  trigger_phrases: "audio reactive"
  trigger_phrases: "music visualizer"
  trigger_phrases: "audio driven visuals"
    rules {
      text: "Correct chain: AudioFileIn CHOP → AudioSpectrum CHOP (FFT=512, outlength=256, timeslice=ON) → Math CHOP (gain=10) → CHOP to TOP (dataformat=r, layout=rowscropped) → GLSL TOP."
      priority: CRITICAL
    }
    rules {
      text: "TimeSlice must stay ON. OFF processes entire file → 24000+ samples → overflow."
      priority: CRITICAL
    }
    rules {
      text: "DO NOT use Lag CHOP or Filter CHOP for spectrum smoothing — they expand samples to 2400+, averaging to near-zero. Smooth in GLSL shader instead."
      priority: CRITICAL
    }
    rules {
      text: "Set Output Length manually to 256 via outputmenu='setmanually' and outlength=256."
      priority: HIGH
    }
    rules {
      text: "Math gain = 10 (not 5). Raw spectrum ~0.19 in bass. Gain 10 gives usable ~5.0."
      priority: HIGH
    }
    rules {
      text: "CHOP to TOP: y=0.25 for first channel (stereo texture is 256x2, first row center is 0.25)."
      priority: NORMAL
    }
    examples {
      label: "GLSL spectrum sampling"
      language: "glsl"
      code: "float iTime = texture(sTD2DInputs[0], vec2(0.5)).r;\nfloat bass = (texture(sTD2DInputs[1], vec2(0.02, 0.25)).r +\n              texture(sTD2DInputs[1], vec2(0.05, 0.25)).r) / 2.0;\nfloat mid  = (texture(sTD2DInputs[1], vec2(0.2, 0.25)).r +\n              texture(sTD2DInputs[1], vec2(0.35, 0.25)).r) / 2.0;\nfloat hi   = (texture(sTD2DInputs[1], vec2(0.6, 0.25)).r +\n              texture(sTD2DInputs[1], vec2(0.8, 0.25)).r) / 2.0;"
    }
}
actions {
  id: "export_video"
  description: "Record and export video from TouchDesigner"
  trigger_phrases: "record TD"
  trigger_phrases: "export video touchdesigner"
  trigger_phrases: "capture TD output"
    rules {
      text: "Use MovieFileOut TOP — TOP.save() captures same GPU texture every time, useless for animation."
      priority: HIGH
    }
    rules {
      text: "Codecs: prores (preferred macOS) or mjpa fallback. H.264/H.265/AV1 require Commercial license."
      priority: HIGH
    }
    rules {
      text: "Before recording: verify FPS > 0 via td_get_perf, verify shader output not black via td_get_screenshot."
      priority: HIGH
    }
    rules {
      text: "Set output path before starting record — setting both in same script can race."
      priority: NORMAL
    }
    data {
      key: "recording_code"
      string_value: "rec = root.create(moviefileoutTOP, 'recorder'); rec.par.type='movie'; rec.par.file='/tmp/output.mov'; rec.par.videocodec='prores'; rec.par.record=True"
    }
}

guardrails {
  text: "NEVER guess parameter names — always call td_get_par_info first"
  scope: ALWAYS
}

guardrails {
  text: "MCP runs on localhost only (port 40404) — no authentication, any local process can send commands"
  scope: ALWAYS
}

guardrails {
  text: "td_execute_python has unrestricted filesystem access as the TD process user"
  scope: ALWAYS
}
