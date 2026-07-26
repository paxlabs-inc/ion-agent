"""
xAI Vision API — analyze images via execute_code when vision_analyze tool is unavailable.

Usage: copy this into an execute_code block, update the image path and prompt.
Reads XAI_API_KEY from /data/.ion/.env automatically.
"""
import base64, json, urllib.request, urllib.error

def analyze_image(image_path: str, prompt: str = "Describe the visual style in detail.", model: str = "grok-4.5", max_tokens: int = 600) -> str:
    """Analyze a single image via xAI vision API. Returns the description text."""
    api_key = ""
    with open("/data/.ion/.env") as f:
        for line in f:
            if line.startswith("XAI_API_KEY="):
                api_key = line.strip().split("=", 1)[1]
                break
    if not api_key:
        raise RuntimeError("XAI_API_KEY not found in /data/.ion/.env")

    with open(image_path, "rb") as f:
        b64 = base64.b64encode(f.read()).decode()

    payload = json.dumps({
        "model": model,
        "messages": [{
            "role": "user",
            "content": [
                {"type": "image_url", "image_url": {"url": f"data:image/jpeg;base64,{b64}", "detail": "high"}},
                {"type": "text", "text": prompt}
            ]
        }],
        "max_tokens": max_tokens
    })

    req = urllib.request.Request(
        "https://api.x.ai/v1/chat/completions",
        data=payload.encode(),
        headers={"Content-Type": "application/json", "Authorization": f"Bearer {api_key}"}
    )
    resp = urllib.request.urlopen(req, timeout=60)
    result = json.loads(resp.read())
    return result["choices"][0]["message"]["content"]


def list_models() -> list[str]:
    """List available xAI models."""
    api_key = ""
    with open("/data/.ion/.env") as f:
        for line in f:
            if line.startswith("XAI_API_KEY="):
                api_key = line.strip().split("=", 1)[1]
                break
    req = urllib.request.Request("https://api.x.ai/v1/models", headers={"Authorization": f"Bearer {api_key}"})
    resp = urllib.request.urlopen(req, timeout=15)
    result = json.loads(resp.read())
    return [m["id"] for m in result.get("data", [])]


# Example: analyze multiple images in sequence
if __name__ == "__main__":
    images = [
        "/data/.ion/cache/images/img_example1.jpg",
        "/data/.ion/cache/images/img_example2.jpg",
    ]
    for i, path in enumerate(images):
        desc = analyze_image(path, "Describe the visual style, colors, composition, lighting, and overall aesthetic.")
        print(f"\n=== IMAGE {i+1} ===\n{desc}")
