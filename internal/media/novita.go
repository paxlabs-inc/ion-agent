package media

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/paxlabs-inc/ion-agent/internal/security/ssrf"
)

const (
	maxProviderResponse = 48 << 20
	maxProviderAsset    = 120 << 20
)

type novitaClient struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

type providerOutput struct {
	Source    string
	MediaType string
	MIMEType  string
	Extension string
	Base64    bool
}

type providerResult struct {
	TaskID   string
	Status   string
	Reason   string
	Progress int
	Outputs  []providerOutput
}

func newNovitaClient(baseURL, apiKey string, client *http.Client) (*novitaClient, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = "https://api.novita.ai"
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Hostname() == "" || !strings.EqualFold(parsed.Scheme, "https") {
		return nil, fmt.Errorf("media: Novita base URL must be a valid HTTPS origin")
	}
	if client == nil {
		dispatcher, err := ssrf.New(ssrf.Config{
			AllowedHosts: []string{parsed.Hostname()}, RequestTimeout: 90 * time.Second,
		})
		if err != nil {
			return nil, fmt.Errorf("media: secure Novita client: %w", err)
		}
		client = dispatcher.Client()
	}
	return &novitaClient{baseURL: baseURL, apiKey: apiKey, client: client}, nil
}

func (client *novitaClient) configured() bool {
	return client != nil && client.apiKey != ""
}

func (client *novitaClient) submit(ctx context.Context, input Request) (providerResult, error) {
	if !client.configured() {
		return providerResult{}, ErrNotConfigured
	}
	endpoint, payload := novitaPayload(input)
	return client.call(ctx, http.MethodPost, endpoint, payload)
}

func (client *novitaClient) progress(ctx context.Context, taskID string) (providerResult, error) {
	if !client.configured() {
		return providerResult{}, ErrNotConfigured
	}
	target := "/v3/async/task-result?task_id=" + url.QueryEscape(taskID)
	return client.call(ctx, http.MethodGet, target, nil)
}

func (client *novitaClient) call(
	ctx context.Context,
	method string,
	endpoint string,
	payload any,
) (providerResult, error) {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return providerResult{}, fmt.Errorf("media: encode provider request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, client.baseURL+endpoint, body)
	if err != nil {
		return providerResult{}, fmt.Errorf("media: construct provider request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+client.apiKey)
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.client.Do(request)
	if err != nil {
		return providerResult{}, fmt.Errorf("media: provider request failed: %w", err)
	}
	defer response.Body.Close()
	encoded, err := io.ReadAll(io.LimitReader(response.Body, maxProviderResponse+1))
	if err != nil {
		return providerResult{}, fmt.Errorf("media: read provider response: %w", err)
	}
	if len(encoded) > maxProviderResponse {
		return providerResult{}, fmt.Errorf("media: provider response exceeds 48 MB")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return providerResult{}, fmt.Errorf(
			"media: provider rejected the request (%d): %s",
			response.StatusCode, safeProviderMessage(encoded),
		)
	}
	result, err := parseProviderResult(encoded)
	if err != nil {
		return providerResult{}, err
	}
	return result, nil
}

func novitaPayload(input Request) (string, any) {
	extraImage := map[string]any{
		"response_image_type": "jpeg", "enable_nsfw_detection": true,
		"nsfw_detection_level": 0,
	}
	switch input.Kind {
	case KindTextToImage:
		return "/v3/async/txt2img", map[string]any{
			"extra":   extraImage,
			"request": imageGenerationRequest(input, false),
		}
	case KindImageToImage:
		return "/v3/async/img2img", map[string]any{
			"extra":   extraImage,
			"request": imageGenerationRequest(input, true),
		}
	case KindTextToVideo:
		return "/v3/async/txt2video", map[string]any{
			"extra":      map[string]any{"response_video_type": "mp4"},
			"model_name": input.Model, "width": input.Width, "height": input.Height,
			"guidance_scale": input.Guidance, "steps": input.Steps, "seed": input.Seed,
			"prompts":         []map[string]any{{"prompt": input.Prompt, "frames": input.Frames}},
			"negative_prompt": input.NegativePrompt,
		}
	case KindImageToVideo:
		return "/v3/async/img2video", map[string]any{
			"extra":      map[string]any{"response_video_type": "mp4"},
			"model_name": input.Model, "image_file": input.ImageBase64,
			"frames_num": input.Frames, "frames_per_second": input.FPS,
			"seed": input.Seed, "image_file_resize_mode": input.ResizeMode,
			"steps": input.Steps, "enable_frame_interpolation": true,
		}
	case KindInpainting:
		return "/v3/async/inpainting", map[string]any{
			"model_name": input.Model, "image_base64": input.ImageBase64,
			"mask_image_base64": input.MaskBase64, "prompt": input.Prompt,
			"negative_prompt": input.NegativePrompt, "image_num": input.ImageCount,
			"sampler_name": input.Sampler, "guidance_scale": input.Guidance,
			"steps": input.Steps, "strength": input.Strength, "seed": input.Seed,
			"mask_blur": 4, "inpainting_full_res": true,
		}
	case KindCleanup:
		return "/v3/cleanup", map[string]any{
			"image_file": input.ImageBase64, "mask_file": input.MaskBase64,
		}
	case KindRemoveBackground:
		return "/v3/remove-background", map[string]any{
			"extra":      map[string]any{"response_image_type": "png"},
			"image_file": input.ImageBase64,
		}
	case KindReplaceBackground:
		return "/v3/replace-background", map[string]any{
			"image_file": input.ImageBase64, "prompt": input.Prompt,
		}
	case KindRemoveText:
		return "/v3/remove-text", map[string]any{"image_file": input.ImageBase64}
	case KindMergeFace:
		return "/v3/merge-face", map[string]any{
			"image_file": input.ImageBase64, "face_image_file": input.FaceBase64,
		}
	case KindUpscale:
		return "/v3/async/upscale", map[string]any{
			"extra": map[string]any{"response_image_type": "png"},
			"request": map[string]any{
				"model_name": input.Model, "image_base64": input.ImageBase64,
				"scale_factor": input.Scale,
			},
		}
	default:
		return "", map[string]any{}
	}
}

func imageGenerationRequest(input Request, withImage bool) map[string]any {
	request := map[string]any{
		"model_name": input.Model, "prompt": input.Prompt,
		"negative_prompt": input.NegativePrompt, "width": input.Width,
		"height": input.Height, "sampler_name": input.Sampler,
		"guidance_scale": input.Guidance, "steps": input.Steps,
		"image_num": input.ImageCount, "clip_skip": 1, "seed": input.Seed,
		"loras": []any{},
	}
	if withImage {
		request["image_base64"] = input.ImageBase64
		request["strength"] = input.Strength
		request["controlnet"] = map[string]any{"units": []any{}}
		request["ip_adapters"] = []any{}
	}
	return request
}

func parseProviderResult(encoded []byte) (providerResult, error) {
	var root map[string]any
	if err := json.Unmarshal(encoded, &root); err != nil {
		return providerResult{}, fmt.Errorf("media: provider returned invalid JSON")
	}
	if data, ok := root["data"].(map[string]any); ok {
		for key, value := range data {
			if _, exists := root[key]; !exists {
				root[key] = value
			}
		}
	}
	result := providerResult{
		TaskID: stringValue(root["task_id"]),
		Status: stringValue(root["status"]),
		Reason: stringValue(root["reason"]),
	}
	if task, ok := root["task"].(map[string]any); ok {
		result.TaskID = firstString(result.TaskID, stringValue(task["task_id"]))
		result.Status = firstString(stringValue(task["status"]), result.Status)
		result.Reason = firstString(stringValue(task["reason"]), result.Reason)
		result.Progress = intValue(task["progress_percent"])
	}
	result.Outputs = append(result.Outputs, parseOutputList(root["images"], "image")...)
	result.Outputs = append(result.Outputs, parseOutputList(root["videos"], "video")...)
	if file := stringValue(root["image_file"]); file != "" {
		mime, extension := imageFormat(stringValue(root["image_type"]))
		result.Outputs = append(result.Outputs, providerOutput{
			Source: file, MediaType: "image", MIMEType: mime,
			Extension: extension, Base64: !strings.HasPrefix(file, "https://"),
		})
	}
	if result.TaskID == "" && len(result.Outputs) == 0 {
		message := firstString(stringValue(root["message"]), stringValue(root["msg"]))
		if message == "" {
			message = "provider response contained no task or output"
		}
		return providerResult{}, fmt.Errorf("media: %s", boundedMessage(message))
	}
	return result, nil
}

func parseOutputList(value any, mediaType string) []providerOutput {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	outputs := make([]providerOutput, 0, len(items))
	for _, item := range items {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		source := stringValue(entry[mediaType+"_url"])
		if source == "" {
			source = stringValue(entry[mediaType+"_file"])
		}
		if source == "" {
			continue
		}
		format := stringValue(entry[mediaType+"_type"])
		mime, extension := outputFormat(mediaType, format, source)
		outputs = append(outputs, providerOutput{
			Source: source, MediaType: mediaType, MIMEType: mime,
			Extension: extension, Base64: !strings.HasPrefix(source, "https://"),
		})
	}
	return outputs
}

func outputFormat(mediaType, format, source string) (string, string) {
	if mediaType == "video" {
		format = strings.ToLower(strings.TrimPrefix(format, "."))
		if format == "gif" || strings.HasSuffix(strings.ToLower(source), ".gif") {
			return "image/gif", ".gif"
		}
		return "video/mp4", ".mp4"
	}
	return imageFormat(format)
}

func imageFormat(format string) (string, string) {
	switch strings.ToLower(strings.TrimPrefix(format, ".")) {
	case "png":
		return "image/png", ".png"
	case "webp":
		return "image/webp", ".webp"
	default:
		return "image/jpeg", ".jpg"
	}
}

func decodeProviderOutput(ctx context.Context, output providerOutput) ([]byte, string, error) {
	if output.Base64 {
		encoded := output.Source
		if comma := strings.IndexByte(encoded, ','); strings.HasPrefix(encoded, "data:") && comma >= 0 {
			encoded = encoded[comma+1:]
		}
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			decoded, err = base64.RawStdEncoding.DecodeString(encoded)
			if err != nil {
				return nil, "", fmt.Errorf("media: provider returned invalid base64 output")
			}
		}
		if len(decoded) > maxProviderAsset {
			return nil, "", fmt.Errorf("media: provider output exceeds 120 MB")
		}
		return decoded, output.MIMEType, nil
	}
	target, err := url.Parse(output.Source)
	if err != nil || !strings.EqualFold(target.Scheme, "https") || target.Hostname() == "" {
		return nil, "", fmt.Errorf("media: provider output URL is invalid")
	}
	dispatcher, err := ssrf.New(ssrf.Config{
		AllowedHosts: []string{target.Hostname()}, RequestTimeout: 2 * time.Minute,
	})
	if err != nil {
		return nil, "", fmt.Errorf("media: secure output download: %w", err)
	}
	defer dispatcher.CloseIdleConnections()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, "", err
	}
	response, err := dispatcher.Do(request)
	if err != nil {
		return nil, "", fmt.Errorf("media: download provider output: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, "", fmt.Errorf("media: provider output download failed (%d)", response.StatusCode)
	}
	decoded, err := io.ReadAll(io.LimitReader(response.Body, maxProviderAsset+1))
	if err != nil {
		return nil, "", fmt.Errorf("media: read provider output: %w", err)
	}
	if len(decoded) > maxProviderAsset {
		return nil, "", fmt.Errorf("media: provider output exceeds 120 MB")
	}
	mime := strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0])
	if mime == "" || mime == "application/octet-stream" {
		mime = output.MIMEType
	}
	if !supportedMIME(mime) {
		return nil, "", fmt.Errorf("media: provider output type is not supported")
	}
	return decoded, mime, nil
}

func supportedMIME(value string) bool {
	switch value {
	case "image/jpeg", "image/png", "image/webp", "image/gif", "video/mp4":
		return true
	default:
		return false
	}
}

func safeProviderMessage(encoded []byte) string {
	var root map[string]any
	if json.Unmarshal(encoded, &root) == nil {
		message := firstString(
			stringValue(root["message"]), stringValue(root["msg"]),
			stringValue(root["error"]),
		)
		if message != "" {
			return boundedMessage(message)
		}
	}
	return "request was rejected"
}

func boundedMessage(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 300 {
		return value[:300]
	}
	return value
}

func stringValue(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func firstString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func intValue(value any) int {
	number, _ := value.(float64)
	return int(number)
}

func assetExtension(mime, fallback string) string {
	switch mime {
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "image/jpeg":
		return ".jpg"
	case "video/mp4":
		return ".mp4"
	default:
		if extension := path.Ext(fallback); extension != "" && len(extension) <= 8 {
			return extension
		}
		return ".bin"
	}
}
