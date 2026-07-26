package operatorapp

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	mediacontrol "github.com/paxlabs-inc/ion-agent/internal/media"
)

type mediaHandler struct {
	service       *mediacontrol.Service
	authenticator controlplane.BrowserAuthenticator
	origin        string
}

func (handler mediaHandler) ServeHTTP(
	writer http.ResponseWriter,
	request *http.Request,
) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	actor, err := handler.authenticator.Authenticate(request)
	if err != nil {
		http.Error(writer, "unauthorized", http.StatusUnauthorized)
		return
	}
	path := strings.Trim(strings.TrimPrefix(request.URL.Path, "/v1/media"), "/")
	if request.Method == http.MethodGet {
		handler.get(writer, request, actor.ActorID, path)
		return
	}
	if request.Header.Get("Origin") != handler.origin ||
		handler.authenticator.ValidateCSRF(request, actor) != nil {
		http.Error(writer, "forbidden", http.StatusForbidden)
		return
	}
	switch request.Method {
	case http.MethodPost:
		if path != "jobs" {
			http.NotFound(writer, request)
			return
		}
		handler.create(writer, request, actor.ActorID)
	case http.MethodDelete:
		parts := strings.Split(path, "/")
		if len(parts) != 2 || parts[0] != "jobs" {
			http.NotFound(writer, request)
			return
		}
		jobID, err := uuid.Parse(parts[1])
		if err != nil {
			writeMediaError(writer, http.StatusBadRequest, "A valid media job is required.")
			return
		}
		if err := handler.service.Delete(request.Context(), actor.ActorID, jobID); err != nil {
			writeMediaServiceError(writer, err)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	default:
		writer.Header().Set("Allow", "GET, POST, DELETE")
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (handler mediaHandler) get(
	writer http.ResponseWriter,
	request *http.Request,
	actorID uuid.UUID,
	path string,
) {
	switch path {
	case "status":
		writeMediaJSON(writer, http.StatusOK, handler.service.Status())
	case "jobs":
		jobs, err := handler.service.List(request.Context(), actorID)
		if err != nil {
			writeMediaServiceError(writer, err)
			return
		}
		writeMediaJSON(writer, http.StatusOK, jobs)
	default:
		parts := strings.Split(path, "/")
		if len(parts) != 2 {
			http.NotFound(writer, request)
			return
		}
		id, err := uuid.Parse(parts[1])
		if err != nil {
			http.NotFound(writer, request)
			return
		}
		switch parts[0] {
		case "jobs":
			job, err := handler.service.Get(request.Context(), actorID, id)
			if err != nil {
				writeMediaServiceError(writer, err)
				return
			}
			writeMediaJSON(writer, http.StatusOK, job)
		case "assets":
			handler.asset(writer, request, actorID, id)
		default:
			http.NotFound(writer, request)
		}
	}
}

func (handler mediaHandler) create(
	writer http.ResponseWriter,
	request *http.Request,
	actorID uuid.UUID,
) {
	request.Body = http.MaxBytesReader(writer, request.Body, 44<<20)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var input mediacontrol.Request
	if err := decoder.Decode(&input); err != nil {
		writeMediaError(writer, http.StatusBadRequest, "The media request is invalid or too large.")
		return
	}
	if err := ensureJSONEnd(decoder); err != nil {
		writeMediaError(writer, http.StatusBadRequest, "The media request must contain one object.")
		return
	}
	idempotencyKey := strings.TrimSpace(request.Header.Get("X-Ion-Idempotency-Key"))
	if idempotencyKey == "" || len(idempotencyKey) > 128 {
		writeMediaError(writer, http.StatusBadRequest, "A valid media idempotency key is required.")
		return
	}
	job, err := handler.service.Create(
		request.Context(), actorID, idempotencyKey, input,
	)
	if err != nil {
		writeMediaServiceError(writer, err)
		return
	}
	writeMediaJSON(writer, http.StatusAccepted, job)
}

func (handler mediaHandler) asset(
	writer http.ResponseWriter,
	request *http.Request,
	actorID, assetID uuid.UUID,
) {
	asset, err := handler.service.Asset(request.Context(), actorID, assetID)
	if err != nil {
		writeMediaServiceError(writer, err)
		return
	}
	file, err := os.Open(asset.Path)
	if err != nil {
		writeMediaError(writer, http.StatusNotFound, "The media asset is unavailable.")
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != asset.Size {
		writeMediaError(writer, http.StatusNotFound, "The media asset is unavailable.")
		return
	}
	writer.Header().Set("Content-Type", asset.MIMEType)
	writer.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	if request.URL.Query().Get("download") == "1" {
		writer.Header().Set(
			"Content-Disposition",
			`attachment; filename="`+strings.ReplaceAll(asset.Name, `"`, "")+`"`,
		)
	}
	http.ServeContent(writer, request, asset.Name, info.ModTime(), file)
}

func writeMediaServiceError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, mediacontrol.ErrNotConfigured):
		writeMediaError(
			writer, http.StatusServiceUnavailable,
			"Novita is not configured. Add NOVITA_API_KEY and restart Ion.",
		)
	case errors.Is(err, mediacontrol.ErrNotFound):
		writeMediaError(writer, http.StatusNotFound, "The media item was not found.")
	default:
		message := strings.TrimSpace(strings.TrimPrefix(err.Error(), "media: "))
		if message == "" {
			message = "The media request could not be completed."
		}
		writeMediaError(writer, http.StatusBadRequest, message)
	}
}

func writeMediaError(writer http.ResponseWriter, status int, message string) {
	writeMediaJSON(writer, status, map[string]string{"error": message})
}

func writeMediaJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}
