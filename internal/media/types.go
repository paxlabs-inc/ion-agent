// Package media provides actor-scoped media generation jobs and assets.
package media

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrNotFound      = errors.New("media: not found")
	ErrNotConfigured = errors.New("media: provider is not configured")
)

type Kind string

const (
	KindTextToImage       Kind = "text-to-image"
	KindImageToImage      Kind = "image-to-image"
	KindTextToVideo       Kind = "text-to-video"
	KindImageToVideo      Kind = "image-to-video"
	KindInpainting        Kind = "inpainting"
	KindCleanup           Kind = "cleanup"
	KindRemoveBackground  Kind = "remove-background"
	KindReplaceBackground Kind = "replace-background"
	KindRemoveText        Kind = "remove-text"
	KindMergeFace         Kind = "merge-face"
	KindUpscale           Kind = "upscale"
)

func (kind Kind) valid() bool {
	switch kind {
	case KindTextToImage, KindImageToImage, KindTextToVideo, KindImageToVideo,
		KindInpainting, KindCleanup, KindRemoveBackground, KindReplaceBackground,
		KindRemoveText, KindMergeFace, KindUpscale:
		return true
	default:
		return false
	}
}

func (kind Kind) asynchronous() bool {
	switch kind {
	case KindTextToImage, KindImageToImage, KindTextToVideo, KindImageToVideo,
		KindInpainting, KindUpscale:
		return true
	default:
		return false
	}
}

type Status string

const (
	StatusQueued     Status = "queued"
	StatusSubmitting Status = "submitting"
	StatusRunning    Status = "running"
	StatusSucceeded  Status = "succeeded"
	StatusFailed     Status = "failed"
)

type Request struct {
	Kind           Kind    `json:"kind"`
	Prompt         string  `json:"prompt,omitempty"`
	NegativePrompt string  `json:"negative_prompt,omitempty"`
	Model          string  `json:"model,omitempty"`
	Sampler        string  `json:"sampler,omitempty"`
	Width          int     `json:"width,omitempty"`
	Height         int     `json:"height,omitempty"`
	Steps          int     `json:"steps,omitempty"`
	Guidance       float64 `json:"guidance,omitempty"`
	Seed           int64   `json:"seed,omitempty"`
	ImageCount     int     `json:"image_count,omitempty"`
	Strength       float64 `json:"strength,omitempty"`
	Frames         int     `json:"frames,omitempty"`
	FPS            int     `json:"fps,omitempty"`
	Scale          float64 `json:"scale,omitempty"`
	ResizeMode     string  `json:"resize_mode,omitempty"`
	ImageBase64    string  `json:"image_base64,omitempty"`
	MaskBase64     string  `json:"mask_base64,omitempty"`
	FaceBase64     string  `json:"face_base64,omitempty"`
	ImageName      string  `json:"image_name,omitempty"`
	MaskName       string  `json:"mask_name,omitempty"`
	FaceName       string  `json:"face_name,omitempty"`
}

type RequestView struct {
	Kind           Kind    `json:"kind"`
	Prompt         string  `json:"prompt,omitempty"`
	NegativePrompt string  `json:"negative_prompt,omitempty"`
	Model          string  `json:"model,omitempty"`
	Sampler        string  `json:"sampler,omitempty"`
	Width          int     `json:"width,omitempty"`
	Height         int     `json:"height,omitempty"`
	Steps          int     `json:"steps,omitempty"`
	Guidance       float64 `json:"guidance,omitempty"`
	Seed           int64   `json:"seed"`
	ImageCount     int     `json:"image_count,omitempty"`
	Strength       float64 `json:"strength,omitempty"`
	Frames         int     `json:"frames,omitempty"`
	FPS            int     `json:"fps,omitempty"`
	Scale          float64 `json:"scale,omitempty"`
	ResizeMode     string  `json:"resize_mode,omitempty"`
	ImageName      string  `json:"image_name,omitempty"`
	MaskName       string  `json:"mask_name,omitempty"`
	FaceName       string  `json:"face_name,omitempty"`
	HasImage       bool    `json:"has_image"`
	HasMask        bool    `json:"has_mask"`
	HasFace        bool    `json:"has_face"`
}

func (request Request) view() RequestView {
	return RequestView{
		Kind: request.Kind, Prompt: request.Prompt, NegativePrompt: request.NegativePrompt,
		Model: request.Model, Sampler: request.Sampler, Width: request.Width,
		Height: request.Height, Steps: request.Steps, Guidance: request.Guidance,
		Seed: request.Seed, ImageCount: request.ImageCount, Strength: request.Strength,
		Frames: request.Frames, FPS: request.FPS, Scale: request.Scale,
		ResizeMode: request.ResizeMode, ImageName: request.ImageName,
		MaskName: request.MaskName, FaceName: request.FaceName,
		HasImage: request.ImageBase64 != "", HasMask: request.MaskBase64 != "",
		HasFace: request.FaceBase64 != "",
	}
}

type Asset struct {
	ID        uuid.UUID `json:"id"`
	JobID     uuid.UUID `json:"job_id"`
	MediaType string    `json:"media_type"`
	MIMEType  string    `json:"mime_type"`
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	URL       string    `json:"url"`
	Path      string    `json:"-"`
}

type Job struct {
	ID             uuid.UUID   `json:"id"`
	ActorID        uuid.UUID   `json:"-"`
	Kind           Kind        `json:"kind"`
	Status         Status      `json:"status"`
	Provider       string      `json:"provider"`
	ProviderTaskID string      `json:"provider_task_id,omitempty"`
	Progress       int         `json:"progress"`
	Error          string      `json:"error,omitempty"`
	Request        RequestView `json:"request"`
	Assets         []Asset     `json:"assets"`
	CreatedAt      time.Time   `json:"created_at"`
	UpdatedAt      time.Time   `json:"updated_at"`
	CompletedAt    *time.Time  `json:"completed_at,omitempty"`
}

type StatusView struct {
	Configured bool   `json:"configured"`
	Provider   string `json:"provider"`
	Message    string `json:"message"`
}

func normalizeRequest(input Request) (Request, error) {
	input.Kind = Kind(strings.TrimSpace(string(input.Kind)))
	if !input.Kind.valid() {
		return Request{}, fmt.Errorf("media: unsupported workflow")
	}
	input.Prompt = strings.TrimSpace(input.Prompt)
	input.NegativePrompt = strings.TrimSpace(input.NegativePrompt)
	input.Model = strings.TrimSpace(input.Model)
	input.Sampler = strings.TrimSpace(input.Sampler)
	input.ResizeMode = strings.TrimSpace(input.ResizeMode)
	input.ImageBase64 = normalizeBase64(input.ImageBase64)
	input.MaskBase64 = normalizeBase64(input.MaskBase64)
	input.FaceBase64 = normalizeBase64(input.FaceBase64)
	input.ImageName = safeName(input.ImageName)
	input.MaskName = safeName(input.MaskName)
	input.FaceName = safeName(input.FaceName)

	if len(input.Prompt) > 2048 || len(input.NegativePrompt) > 2048 ||
		len(input.Model) > 256 || len(input.Sampler) > 128 {
		return Request{}, fmt.Errorf("media: text input exceeds its bound")
	}
	needsPrompt := input.Kind == KindTextToImage || input.Kind == KindTextToVideo ||
		input.Kind == KindImageToImage || input.Kind == KindReplaceBackground ||
		input.Kind == KindInpainting
	if needsPrompt && input.Prompt == "" {
		return Request{}, fmt.Errorf("media: a prompt is required")
	}
	needsImage := input.Kind != KindTextToImage && input.Kind != KindTextToVideo
	if needsImage && input.ImageBase64 == "" {
		return Request{}, fmt.Errorf("media: a source image is required")
	}
	if (input.Kind == KindCleanup || input.Kind == KindInpainting) && input.MaskBase64 == "" {
		return Request{}, fmt.Errorf("media: a mask image is required")
	}
	if input.Kind == KindMergeFace && input.FaceBase64 == "" {
		return Request{}, fmt.Errorf("media: a face reference is required")
	}
	for _, encoded := range []string{input.ImageBase64, input.MaskBase64, input.FaceBase64} {
		if len(encoded) > 40<<20 {
			return Request{}, fmt.Errorf("media: uploaded image exceeds 30 MB")
		}
		if encoded != "" {
			if err := validateImageBase64(encoded); err != nil {
				return Request{}, err
			}
		}
	}
	defaultRequest(&input)
	if input.Width < 128 || input.Width > 2048 || input.Height < 128 || input.Height > 2048 {
		return Request{}, fmt.Errorf("media: dimensions must be between 128 and 2048")
	}
	if input.Steps < 1 || input.Steps > 100 || input.Guidance < 1 || input.Guidance > 30 {
		return Request{}, fmt.Errorf("media: generation controls are out of range")
	}
	if input.ImageCount < 1 || input.ImageCount > 4 ||
		input.Strength < 0 || input.Strength > 1 ||
		input.Scale <= 1 || input.Scale > 4 {
		return Request{}, fmt.Errorf("media: output controls are out of range")
	}
	if input.Frames < 8 || input.Frames > 128 || input.FPS < 1 || input.FPS > 24 {
		return Request{}, fmt.Errorf("media: video controls are out of range")
	}
	if input.Kind == KindTextToVideo {
		if input.Frames > 64 || input.Width < 256 || input.Width > 1024 ||
			input.Height < 256 || input.Height > 1024 {
			return Request{}, fmt.Errorf("media: text-to-video dimensions or frames are out of range")
		}
	}
	if input.Kind == KindImageToVideo {
		expectedFrames := 25
		if strings.EqualFold(input.Model, "SVD") {
			expectedFrames = 14
		}
		if input.Frames != expectedFrames || input.FPS != 6 {
			return Request{}, fmt.Errorf(
				"media: %s requires %d frames at 6 FPS",
				input.Model, expectedFrames,
			)
		}
	}
	return input, nil
}

func validateImageBase64(encoded string) error {
	decoded := make([]byte, base64.StdEncoding.DecodedLen(len(encoded)))
	length, err := base64.StdEncoding.Decode(decoded, []byte(encoded))
	if err != nil {
		clear(decoded)
		return fmt.Errorf("media: uploaded image is not valid base64")
	}
	decoded = decoded[:length]
	if len(decoded) == 0 || len(decoded) > 30<<20 {
		clear(decoded)
		return fmt.Errorf("media: uploaded image exceeds 30 MB")
	}
	detected := http.DetectContentType(decoded)
	valid := detected == "image/jpeg" || detected == "image/png"
	if !valid && len(decoded) >= 12 {
		valid = string(decoded[:4]) == "RIFF" && string(decoded[8:12]) == "WEBP"
	}
	clear(decoded)
	if !valid {
		return fmt.Errorf("media: uploaded content is not a PNG, JPEG, or WebP image")
	}
	return nil
}

func defaultRequest(input *Request) {
	if input.Width == 0 {
		input.Width = 1024
	}
	if input.Height == 0 {
		input.Height = 1024
	}
	if input.Steps == 0 {
		input.Steps = 20
	}
	if input.Guidance == 0 {
		input.Guidance = 7.5
	}
	if input.Seed == 0 {
		input.Seed = -1
	}
	if input.ImageCount == 0 {
		input.ImageCount = 1
	}
	if input.Strength == 0 {
		input.Strength = 0.7
	}
	if input.Scale == 0 {
		input.Scale = 2
	}
	if input.Frames == 0 {
		input.Frames = 25
	}
	if input.FPS == 0 {
		input.FPS = 6
	}
	if input.Sampler == "" {
		input.Sampler = "DPM++ 2M Karras"
	}
	if input.ResizeMode == "" {
		input.ResizeMode = "ORIGINAL_RESOLUTION"
	}
	if input.Model == "" {
		switch input.Kind {
		case KindTextToVideo:
			input.Model = "darkSushiMixMix_225D_64380.safetensors"
		case KindImageToVideo:
			input.Model = "SVD-XT"
		case KindUpscale:
			input.Model = "RealESRNet_x4plus"
		case KindInpainting:
			input.Model = "realisticVisionV51_v51VAE-inpainting_94324.safetensors"
		default:
			input.Model = "sd_xl_base_1.0.safetensors"
		}
	}
}

func normalizeBase64(value string) string {
	value = strings.TrimSpace(value)
	if comma := strings.IndexByte(value, ','); strings.HasPrefix(value, "data:") && comma >= 0 {
		return value[comma+1:]
	}
	return value
}

func safeName(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "\\", "/")
	if slash := strings.LastIndexByte(value, '/'); slash >= 0 {
		value = value[slash+1:]
	}
	if len(value) > 160 {
		value = value[len(value)-160:]
	}
	return value
}
