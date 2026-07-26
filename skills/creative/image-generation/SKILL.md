---
name: image-generation
description: Produce images and perform visual analysis using xAI (Grok Imagine) or FAL backends. Covers provider configuration, style-matched generation from reference images, vision analysis through the API, and prompt engineering for premium brand aesthetics.
tags: [images, vision, xai, fal, branding, design, ai-art]
---

# Image Generation & Vision Analysis

Generate images with the `image_generate` tool and analyze existing images through xAI's vision API. The primary backend is **xAI (Grok Imagine)**, with FAL as a fallback option.

## Provider Setup

### xAI (preferred)
```bash
ion config set XAI_API_KEY "<key>"
ion config set image_gen.provider xai
ion config set vision.provider xai
```

### FAL
```bash
ion config set FAL_KEY "<key>"
# provider auto-detects, or:
ion config set image_gen.provider fal
```

## Image Generation

Call the `image_generate` tool directly. Important parameters:
- `prompt` — a thorough text description
- `aspect_ratio` — `landscape` (16:9), `square` (1:1), `portrait` (16:9 tall)
- `image_url` — source image for editing (image-to-image mode)
- `reference_image_urls` — style/composition references (up to 2)

xAI produces a public URL that remains available (storage fees apply). Turn off with `image_gen.xai.storage.enabled: false`.

### Batch Generation

Produce several images simultaneously by issuing multiple `image_generate` calls within the same turn. Up to 5 concurrent requests work well.

## Vision Analysis (via xAI API)

The `vision_analyze` tool may not be present in every session. As a fallback, invoke xAI's chat completions API directly from `execute_code`.

### Pattern: Analyze images via execute_code
```python
import base64, json, os, urllib.request, urllib.error

# Read API key from .env
api_key = ""
with open("/data/.ion/.env") as f:
    for line in f:
        if line.startswith("XAI_API_KEY="):
            api_key = line.strip().split("=", 1)[1]
            break

# Encode image
with open("/path/to/image.jpg", "rb") as f:
    b64 = base64.b64encode(f.read()).decode()

payload = json.dumps({
    "model": "grok-4.5",  # multimodal, supports vision
    "messages": [{
        "role": "user",
        "content": [
            {"type": "image_url", "image_url": {"url": f"data:image/jpeg;base64,{b64}", "detail": "high"}},
            {"type": "text", "text": "Describe the visual style in detail."}
        ]
    }],
    "max_tokens": 600
})

req = urllib.request.Request(
    "https://api.x.ai/v1/chat/completions",
    data=payload.encode(),
    headers={"Content-Type": "application/json", "Authorization": f"Bearer {api_key}"}
)
resp = urllib.request.urlopen(req, timeout=60)
result = json.loads(resp.read())
print(result["choices"][0]["message"]["content"])
```

### Available xAI models (as of 2026-07)
```
grok-4.3, grok-4.5          # text + vision (multimodal)
grok-imagine-image           # image generation
grok-imagine-image-quality   # higher quality image gen
grok-imagine-video           # video generation
```

**Pitfall**: No `grok-2-vision` model exists. For vision tasks, use `grok-4.5` or `grok-4.3`.

### Listing models
```python
req = urllib.request.Request("https://api.x.ai/v1/models", headers={"Authorization": f"Bearer {api_key}"})
resp = urllib.request.urlopen(req, timeout=15)
models = json.loads(resp.read())
for m in models.get("data", []):
    print(m["id"])
```

## Style-Matched Generation Workflow

When the user supplies reference images and expects output that matches their style:

1. **Analyze** each reference image through the vision API (pattern above)
2. **Synthesize** the style DNA — identify shared patterns across all images:
   - Color palette (dominant, accent, background)
   - Composition (negative space, balance, framing)
   - Materials/textures (glass, metal, glow, etc.)
   - Lighting (rim, volumetric, bloom, direction)
   - Typography (if present — font style, hierarchy)
   - Mood/atmosphere
3. **Generate** new images with prompts that explicitly encode the extracted style DNA
4. **Present** results using image markdown with brief style notes for each image

### Prompt Engineering for Premium Tech Aesthetic

Essential prompt components for luxury tech branding (Apple-level):
- Define **background**: "deep black void", "pure matte black"
- Define **materials**: "frosted glass", "holographic", "subsurface scattering", "polished ceramic"
- Define **lighting**: "dramatic rim lighting", "soft bloom", "volumetric glow", "cinematic studio lighting"
- Define **composition**: "generous negative space", "asymmetric balance", "monumental scale"
- Define **quality**: "ultra-clean", "photorealistic", "8K", "editorial quality"
- Include "no text" to prevent unwanted typography artifacts

### User Style Preferences (this user)
- Apple-level luxury tech aesthetic
- Dark backgrounds (#0a0a0a or deeper)
- Electric blue-cyan dominant palette, warm gold accents
- Frosted glass / holographic / volumetric materials
- Dramatic rim lighting with bloom
- Generous negative space, minimal compositions
- Bold geometric sans-serif when typography is needed
- Mood: calm, intelligent, powerful, futuristic — quiet confidence over hype