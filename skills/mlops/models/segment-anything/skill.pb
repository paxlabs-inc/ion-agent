meta {
  name: "segment-anything-model"
  version: "1.0.0"
  summary: "SAM: zero-shot image segmentation via points, boxes, or masks"
  author: "Orchestra Research"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "segment anything"
  keywords: "sam"
  keywords: "image segmentation"
  keywords: "object segmentation"
  keywords: "zero-shot segmentation"
  keywords: "mask generation"
  keywords: "sam predictor"
  intents: "segment_image"
  intents: "generate_masks"
  intents: "interactive_segmentation"
  intents: "automatic_segmentation"
  patterns: "(segment|cut.?out|extract) .*(object|region|image|mask)"
  patterns: "(sam|segment.anything)"
  patterns: "(zero.?shot|automatic) .*(segmentation|mask)"
  patterns: "(point|box|bounding.box) .*(prompt|segment|select)"
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
  capabilities: "image_segmentation"
  capabilities: "automatic_mask_generation"
  capabilities: "interactive_segmentation"
  capabilities: "onnx_deployment"
  output_types: ".png"
  output_types: ".onnx"
}

actions {
  id: "install"
  description: "Install SAM and download model checkpoints"
  trigger_phrases: "install sam"
  trigger_phrases: "set up segment anything"
  trigger_phrases: "download sam model"
    rules {
      text: "pip install git+https://github.com/facebookresearch/segment-anything.git"
      priority: HIGH
    }
    rules {
      text: "Optional: pip install opencv-python pycocotools matplotlib"
      priority: HIGH
    }
    rules {
      text: "Alternative: pip install transformers for HuggingFace integration"
      priority: HIGH
    }
    rules {
      text: "Three model sizes: ViT-B (375MB, fastest), ViT-L (1.2GB), ViT-H (2.4GB, most accurate)"
      priority: NORMAL
    }
    data {
      key: "install_command"
      string_value: "pip install git+https://github.com/facebookresearch/segment-anything.git"
    }
    data {
      key: "model_checkpoints"
      map_value {
        entries {
          key: "vit_h"
          string_value: "sam_vit_h_4b8939.pth (2.4GB, best accuracy)"
        }
        entries {
          key: "vit_l"
          string_value: "sam_vit_l_0b3195.pth (1.2GB, balanced)"
        }
        entries {
          key: "vit_b"
          string_value: "sam_vit_b_01ec64.pth (375MB, fastest)"
        }
      }
    }
}
actions {
  id: "point_segmentation"
  description: "Segment objects using point prompts"
  trigger_phrases: "segment with point"
  trigger_phrases: "click to segment"
  trigger_phrases: "select object by clicking"
  trigger_phrases: "point prompt segmentation"
    rules {
      text: "Points are (x, y) coordinates with labels: 1=foreground, 0=background"
      priority: CRITICAL
    }
    rules {
      text: "multimask_output=True returns 3 mask options; select best by score"
      priority: HIGH
    }
    rules {
      text: "Call predictor.set_image() once, then predict() multiple times for same image"
      priority: HIGH
    }
    rules {
      text: "Combine foreground and background points for precise control"
      priority: NORMAL
    }
    examples {
      label: "single point segmentation"
      language: "python"
      code: "import numpy as np\nfrom segment_anything import sam_model_registry, SamPredictor\nsam = sam_model_registry[\"vit_h\"](checkpoint=\"sam_vit_h_4b8939.pth\")\nsam.to(device=\"cuda\")\npredictor = SamPredictor(sam)\npredictor.set_image(image)\ninput_point = np.array([[500, 375]])\ninput_label = np.array([1])\nmasks, scores, logits = predictor.predict(\n    point_coords=input_point, point_labels=input_label, multimask_output=True\n)\nbest_mask = masks[np.argmax(scores)]"
    }
    examples {
      label: "foreground + background points"
      language: "python"
      code: "input_points = np.array([[500, 375], [600, 400], [450, 300]])\ninput_labels = np.array([1, 1, 0])\nmasks, scores, logits = predictor.predict(\n    point_coords=input_points, point_labels=input_labels, multimask_output=False\n)"
    }
}
actions {
  id: "box_segmentation"
  description: "Segment objects using bounding box prompts"
  trigger_phrases: "segment with box"
  trigger_phrases: "bounding box segmentation"
  trigger_phrases: "box prompt"
  trigger_phrases: "rectangle selection"
    rules {
      text: "Box format: [x1, y1, x2, y2]"
      priority: HIGH
    }
    rules {
      text: "Use multimask_output=False for box prompts"
      priority: HIGH
    }
    rules {
      text: "Combine box + points for more precise control"
      priority: NORMAL
    }
    examples {
      label: "box prompt segmentation"
      language: "python"
      code: "input_box = np.array([425, 600, 700, 875])\nmasks, scores, logits = predictor.predict(box=input_box, multimask_output=False)"
    }
    examples {
      label: "box + points combined"
      language: "python"
      code: "masks, scores, logits = predictor.predict(\n    point_coords=np.array([[500, 375]]),\n    point_labels=np.array([1]),\n    box=np.array([400, 300, 700, 600]),\n    multimask_output=False\n)"
    }
}
actions {
  id: "automatic_segmentation"
  description: "Generate all object masks automatically"
  trigger_phrases: "automatic segmentation"
  trigger_phrases: "segment everything"
  trigger_phrases: "generate all masks"
  trigger_phrases: "auto mask"
    rules {
      text: "Use SamAutomaticMaskGenerator for zero-prompt segmentation"
      priority: HIGH
    }
    rules {
      text: "Filter by predicted_iou and stability_score for quality"
      priority: HIGH
    }
    rules {
      text: "points_per_side=32 default; reduce for speed, increase for more masks"
      priority: NORMAL
    }
    data {
      key: "mask_output_fields"
      list_value {
        items {
          string_value: "segmentation: H×W binary mask"
        }
        items {
          string_value: "bbox: [x, y, w, h]"
        }
        items {
          string_value: "area: pixel count"
        }
        items {
          string_value: "predicted_iou: 0-1 quality score"
        }
        items {
          string_value: "stability_score: 0-1 robustness score"
        }
      }
    }
    examples {
      label: "automatic mask generation"
      language: "python"
      code: "from segment_anything import SamAutomaticMaskGenerator\nmask_generator = SamAutomaticMaskGenerator(sam)\nmasks = mask_generator.generate(image)\nhigh_quality = [m for m in masks if m['predicted_iou'] > 0.9]"
    }
}
actions {
  id: "iterative_refinement"
  description: "Refine segmentation with additional prompts"
  trigger_phrases: "refine segmentation"
  trigger_phrases: "improve mask"
  trigger_phrases: "add more points"
    rules {
      text: "Pass previous mask logits via mask_input parameter for refinement"
      priority: HIGH
    }
    rules {
      text: "Add background points (label=0) to exclude regions"
      priority: NORMAL
    }
    examples {
      label: "iterative refinement"
      language: "python"
      code: "# Initial prediction\nmasks, scores, logits = predictor.predict(\n    point_coords=np.array([[500, 375]]), point_labels=np.array([1]), multimask_output=True\n)\n# Refine with additional background point\nmasks, scores, logits = predictor.predict(\n    point_coords=np.array([[500, 375], [550, 400]]),\n    point_labels=np.array([1, 0]),\n    mask_input=logits[np.argmax(scores)][None, :, :],\n    multimask_output=False\n)"
    }
}
actions {
  id: "onnx_deployment"
  description: "Export SAM to ONNX for browser/edge deployment"
  trigger_phrases: "export sam onnx"
  trigger_phrases: "onnx deployment"
  trigger_phrases: "browser segmentation"
    rules {
      text: "Use scripts/export_onnx_model.py with --return-single-mask for faster inference"
      priority: HIGH
    }
    rules {
      text: "ONNX model takes pre-computed image embeddings"
      priority: NORMAL
    }
    examples {
      label: "onnx export"
      language: "bash"
      code: "python scripts/export_onnx_model.py \\\n  --checkpoint sam_vit_h_4b8939.pth \\\n  --model-type vit_h \\\n  --output sam_onnx.onnx \\\n  --return-single-mask"
    }
}

guardrails {
  text: "ViT-B for limited VRAM; ViT-H for best accuracy — don't default to ViT-H without checking GPU"
  scope: ALWAYS
}

guardrails {
  text: "Always call predictor.set_image() before predict() — computes embeddings once per image"
  scope: ALWAYS
}

guardrails {
  text: "Filter masks by stability_score > 0.95 and predicted_iou > 0.88 for quality"
  scope: ALWAYS
}

related {
  name: "audiocraft-audio-generation"
  relationship: "composes_with"
  description: "Both use Meta AI models; SAM for visual, AudioCraft for audio"
}
