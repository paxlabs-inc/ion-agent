package media

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

type Config struct {
	DataDirectory string
	APIKey        string
	BaseURL       string
	HTTPClient    *http.Client
	Cipher        Cipher
	Clock         types.Clock
}

type Service struct {
	store     *Store
	client    *novitaClient
	clock     types.Clock
	assetRoot string
	ctx       context.Context
	cancel    context.CancelFunc
	running   sync.Map
	wg        sync.WaitGroup
}

func Open(ctx context.Context, config Config) (*Service, error) {
	if strings.TrimSpace(config.DataDirectory) == "" || config.Cipher == nil ||
		config.Clock == nil {
		return nil, fmt.Errorf("media: data directory, cipher, and clock are required")
	}
	root := filepath.Join(filepath.Clean(config.DataDirectory), "media")
	assetRoot := filepath.Join(root, "assets")
	if err := os.MkdirAll(assetRoot, 0o700); err != nil {
		return nil, fmt.Errorf("media: create asset directory: %w", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return nil, fmt.Errorf("media: secure data directory: %w", err)
	}
	store, err := OpenStore(ctx, filepath.Join(root, "media.db"), config.Cipher)
	if err != nil {
		return nil, err
	}
	client, err := newNovitaClient(config.BaseURL, config.APIKey, config.HTTPClient)
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	serviceCtx, cancel := context.WithCancel(ctx)
	service := &Service{
		store: store, client: client, clock: config.Clock, assetRoot: assetRoot,
		ctx: serviceCtx, cancel: cancel,
	}
	pending, err := store.Pending(ctx)
	if err != nil {
		_ = service.Close()
		return nil, err
	}
	for _, job := range pending {
		job := job
		if job.Status == StatusSubmitting && job.ProviderTaskID == "" {
			_ = service.store.Update(
				ctx, job.ID, StatusFailed, "", job.Progress,
				"Submission was interrupted before the provider task ID was recorded; outcome is unknown and the paid request was not repeated.",
				config.Clock.Now(),
			)
			continue
		}
		service.start(job)
	}
	return service, nil
}

func (service *Service) Status() StatusView {
	if service.client.configured() {
		return StatusView{
			Configured: true, Provider: "Novita",
			Message: "Novita media generation is ready.",
		}
	}
	return StatusView{
		Configured: false, Provider: "Novita",
		Message: "Add NOVITA_API_KEY to Ion's protected environment and restart the service.",
	}
}

func (service *Service) Create(
	ctx context.Context,
	actorID uuid.UUID,
	idempotencyKey string,
	input Request,
) (Job, error) {
	if !service.client.configured() {
		return Job{}, ErrNotConfigured
	}
	request, err := normalizeRequest(input)
	if err != nil {
		return Job{}, err
	}
	job, err := service.store.Create(
		ctx, actorID, idempotencyKey, request, service.clock.Now(),
	)
	if err != nil {
		return Job{}, err
	}
	service.start(job)
	return job, nil
}

func (service *Service) List(ctx context.Context, actorID uuid.UUID) ([]Job, error) {
	return service.store.List(ctx, actorID, 50)
}

func (service *Service) Get(
	ctx context.Context,
	actorID, jobID uuid.UUID,
) (Job, error) {
	return service.store.Get(ctx, actorID, jobID)
}

func (service *Service) Asset(
	ctx context.Context,
	actorID, assetID uuid.UUID,
) (Asset, error) {
	asset, err := service.store.Asset(ctx, actorID, assetID)
	if err != nil {
		return Asset{}, err
	}
	if !safeAssetPath(service.assetRoot, asset.Path) {
		return Asset{}, ErrNotFound
	}
	return asset, nil
}

func (service *Service) Delete(
	ctx context.Context,
	actorID, jobID uuid.UUID,
) error {
	job, err := service.store.Get(ctx, actorID, jobID)
	if err != nil {
		return err
	}
	if job.Status != StatusSucceeded && job.Status != StatusFailed {
		return fmt.Errorf("media: wait for the active job to finish before deleting it")
	}
	paths, err := service.store.Delete(ctx, actorID, jobID)
	if err != nil {
		return err
	}
	for _, storedPath := range paths {
		if safeAssetPath(service.assetRoot, storedPath) {
			_ = os.Remove(storedPath)
		}
	}
	_ = os.Remove(filepath.Join(service.assetRoot, jobID.String()))
	return nil
}

func (service *Service) start(job Job) {
	if _, loaded := service.running.LoadOrStore(job.ID, struct{}{}); loaded {
		return
	}
	service.wg.Add(1)
	go func() {
		defer service.wg.Done()
		defer service.running.Delete(job.ID)
		service.run(job)
	}()
}

func (service *Service) run(job Job) {
	request, err := service.store.Request(service.ctx, job.ID)
	if err != nil {
		service.fail(job, err)
		return
	}
	if job.ProviderTaskID != "" {
		service.poll(job, job.ProviderTaskID)
		return
	}
	if err := service.store.Update(
		service.ctx, job.ID, StatusSubmitting, "", 2, "", service.clock.Now(),
	); err != nil {
		return
	}
	submitCtx, cancel := context.WithTimeout(service.ctx, 2*time.Minute)
	result, err := service.client.submit(submitCtx, request)
	cancel()
	if err != nil {
		service.fail(job, err)
		return
	}
	if request.Kind.asynchronous() {
		if result.TaskID == "" {
			service.fail(job, fmt.Errorf("provider accepted generation without a task ID"))
			return
		}
		if err := service.store.Update(
			service.ctx, job.ID, StatusRunning, result.TaskID, 5, "",
			service.clock.Now(),
		); err != nil {
			return
		}
		service.poll(job, result.TaskID)
		return
	}
	if len(result.Outputs) == 0 {
		service.fail(job, fmt.Errorf("provider returned no media output"))
		return
	}
	if err := service.complete(job, "", result.Outputs); err != nil {
		service.fail(job, err)
	}
}

func (service *Service) poll(job Job, taskID string) {
	deadline := time.NewTimer(30 * time.Minute)
	defer deadline.Stop()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-service.ctx.Done():
			return
		case <-deadline.C:
			service.failWithTask(job, taskID, fmt.Errorf(
				"generation exceeded the 30 minute provider window",
			))
			return
		case <-ticker.C:
			pollCtx, cancel := context.WithTimeout(service.ctx, 45*time.Second)
			result, err := service.client.progress(pollCtx, taskID)
			cancel()
			if err != nil {
				continue
			}
			switch strings.ToUpper(result.Status) {
			case "TASK_STATUS_SUCCEED", "SUCCEED", "SUCCEEDED":
				if len(result.Outputs) == 0 {
					service.failWithTask(
						job, taskID, fmt.Errorf("provider completed without media output"),
					)
					return
				}
				if err := service.complete(job, taskID, result.Outputs); err != nil {
					service.failWithTask(job, taskID, err)
				}
				return
			case "TASK_STATUS_FAILED", "FAILED":
				reason := result.Reason
				if reason == "" {
					reason = "provider generation failed"
				}
				service.failWithTask(job, taskID, errors.New(reason))
				return
			default:
				progress := result.Progress
				if progress < 5 {
					progress = 5
				}
				if progress > 95 {
					progress = 95
				}
				_ = service.store.Update(
					service.ctx, job.ID, StatusRunning, taskID, progress, "",
					service.clock.Now(),
				)
			}
		}
	}
}

func (service *Service) complete(
	job Job,
	taskID string,
	outputs []providerOutput,
) error {
	if len(outputs) > 8 {
		return fmt.Errorf("provider returned more than 8 outputs")
	}
	jobDirectory := filepath.Join(service.assetRoot, job.ID.String())
	if err := os.MkdirAll(jobDirectory, 0o700); err != nil {
		return fmt.Errorf("create media output directory: %w", err)
	}
	var stored []string
	for index, output := range outputs {
		downloadCtx, cancel := context.WithTimeout(service.ctx, 3*time.Minute)
		content, mime, err := decodeProviderOutput(downloadCtx, output)
		cancel()
		if err != nil {
			cleanupFiles(stored)
			return err
		}
		if err := verifyMedia(content, mime); err != nil {
			clear(content)
			cleanupFiles(stored)
			return err
		}
		extension := assetExtension(mime, output.Extension)
		name := fmt.Sprintf("%s-%02d%s", output.MediaType, index+1, extension)
		destination := filepath.Join(jobDirectory, name)
		if err := writeExclusive(destination, content); err != nil {
			clear(content)
			cleanupFiles(stored)
			return err
		}
		size := int64(len(content))
		clear(content)
		stored = append(stored, destination)
		asset := Asset{
			ID: uuid.New(), JobID: job.ID, MediaType: output.MediaType,
			MIMEType: mime, Name: name, Path: destination, Size: size,
		}
		if err := service.store.AddAsset(service.ctx, asset); err != nil {
			cleanupFiles(stored)
			return err
		}
	}
	return service.store.Update(
		service.ctx, job.ID, StatusSucceeded, taskID, 100, "", service.clock.Now(),
	)
}

func (service *Service) fail(job Job, err error) {
	service.failWithTask(job, job.ProviderTaskID, err)
}

func (service *Service) failWithTask(job Job, taskID string, err error) {
	if errors.Is(err, context.Canceled) || service.ctx.Err() != nil {
		return
	}
	message := boundedMessage(strings.TrimPrefix(err.Error(), "media: "))
	_ = service.store.Update(
		context.Background(), job.ID, StatusFailed, taskID, job.Progress,
		message, service.clock.Now(),
	)
}

func (service *Service) Close() error {
	service.cancel()
	service.wg.Wait()
	return service.store.Close()
}

func safeAssetPath(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != "." && relative != "" &&
		relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func writeExclusive(path string, content []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create media output: %w", err)
	}
	_, writeErr := file.Write(content)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(path)
		return fmt.Errorf("write media output: %w", errors.Join(writeErr, closeErr))
	}
	return nil
}

func verifyMedia(content []byte, mime string) error {
	if len(content) == 0 {
		return fmt.Errorf("media output is empty")
	}
	detected := http.DetectContentType(content)
	switch mime {
	case "image/jpeg":
		if detected != "image/jpeg" {
			return fmt.Errorf("media output does not match JPEG")
		}
	case "image/png":
		if detected != "image/png" {
			return fmt.Errorf("media output does not match PNG")
		}
	case "image/gif":
		if detected != "image/gif" {
			return fmt.Errorf("media output does not match GIF")
		}
	case "image/webp":
		if len(content) < 12 || string(content[:4]) != "RIFF" ||
			string(content[8:12]) != "WEBP" {
			return fmt.Errorf("media output does not match WebP")
		}
	case "video/mp4":
		if len(content) < 12 || string(content[4:8]) != "ftyp" {
			return fmt.Errorf("media output does not match MP4")
		}
	default:
		return fmt.Errorf("media output type is not supported")
	}
	return nil
}

func cleanupFiles(paths []string) {
	for _, path := range paths {
		_ = os.Remove(path)
	}
}
