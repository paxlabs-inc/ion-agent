package operatorapp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	officecontrol "github.com/paxlabs-inc/ion-agent/internal/office"
	workcontrol "github.com/paxlabs-inc/ion-agent/internal/work"
)

type officeHandler struct {
	service       *officecontrol.Service
	authenticator controlplane.BrowserAuthenticator
	origin        string
	maxUpload     int64
	work          *workcontrol.Service
	workspaceRoot string
}

func (handler officeHandler) ServeHTTP(
	writer http.ResponseWriter,
	request *http.Request,
) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")

	path := strings.Trim(strings.TrimPrefix(request.URL.Path, "/v1/office"), "/")
	// Engine endpoints are authenticated by short-lived, purpose-scoped tokens,
	// not by a browser cookie.
	if strings.HasPrefix(path, "machine/") {
		handler.machineEndpoints(writer, request, path)
		return
	}

	actor, err := handler.authenticator.Authenticate(request)
	if err != nil {
		http.Error(writer, "unauthorized", http.StatusUnauthorized)
		return
	}

	if request.Method == http.MethodGet {
		handler.get(writer, request, actor.ActorID, path)
		return
	}

	// Mutations require CSRF
	if request.Header.Get("Origin") != handler.origin ||
		handler.authenticator.ValidateCSRF(request, actor) != nil {
		http.Error(writer, "forbidden", http.StatusForbidden)
		return
	}

	switch request.Method {
	case http.MethodPost:
		handler.post(writer, request, actor.ActorID, path)
	case http.MethodPatch:
		handler.patch(writer, request, actor.ActorID, path)
	case http.MethodDelete:
		handler.del(writer, request, actor.ActorID, path)
	default:
		writer.Header().Set("Allow", "GET, POST, PATCH, DELETE")
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (handler officeHandler) get(
	writer http.ResponseWriter,
	request *http.Request,
	actorID uuid.UUID,
	path string,
) {
	switch {
	case path == "status":
		status := handler.service.Status(request.Context())
		writeOfficeJSON(writer, http.StatusOK, status)
	case path == "documents":
		archived := request.URL.Query().Get("archived") == "true"
		docs, err := handler.service.ListDocuments(request.Context(), actorID, archived)
		if err != nil {
			writeOfficeServiceError(writer, err)
			return
		}
		writeOfficeJSON(writer, http.StatusOK, docs)
	case path == "templates":
		templates, err := handler.service.ListTemplates(request.Context())
		if err != nil {
			writeOfficeServiceError(writer, err)
			return
		}
		writeOfficeJSON(writer, http.StatusOK, templates)
	default:
		parts := strings.Split(path, "/")
		if len(parts) < 2 || parts[0] != "documents" {
			http.NotFound(writer, request)
			return
		}
		docID, err := uuid.Parse(parts[1])
		if err != nil {
			http.NotFound(writer, request)
			return
		}
		if len(parts) == 2 {
			doc, err := handler.service.GetDocument(request.Context(), actorID, docID)
			if err != nil {
				writeOfficeServiceError(writer, err)
				return
			}
			writeOfficeJSON(writer, http.StatusOK, doc)
			return
		}
		switch parts[2] {
		case "versions":
			versions, err := handler.service.ListVersions(request.Context(), actorID, docID)
			if err != nil {
				writeOfficeServiceError(writer, err)
				return
			}
			writeOfficeJSON(writer, http.StatusOK, versions)
		case "download":
			versionID := uuid.Nil
			if len(parts) > 3 {
				versionID, _ = uuid.Parse(parts[3])
			}
			if versionID == uuid.Nil {
				// Download current version
				doc, err := handler.service.GetDocument(request.Context(), actorID, docID)
				if err != nil {
					writeOfficeServiceError(writer, err)
					return
				}
				versionID = doc.CurrentVersionID
			}
			content, contentType, filename, err := handler.service.DownloadVersion(
				request.Context(), actorID, docID, versionID,
			)
			if err != nil {
				writeOfficeServiceError(writer, err)
				return
			}
			writer.Header().Set("Content-Type", contentType)
			disposition := mime.FormatMediaType("attachment", map[string]string{
				"filename": filename,
			})
			writer.Header().Set("Content-Disposition", disposition)
			_, _ = writer.Write(content)
		default:
			http.NotFound(writer, request)
		}
	}
}

func (handler officeHandler) post(
	writer http.ResponseWriter,
	request *http.Request,
	actorID uuid.UUID,
	path string,
) {
	parts := strings.Split(path, "/")
	if path == "documents" {
		handler.handleCreate(writer, request, actorID)
		return
	}
	if path == "documents/upload" {
		handler.handleUpload(writer, request, actorID)
		return
	}
	if len(parts) == 3 && parts[0] == "sessions" && parts[2] == "events" {
		handler.handleClientLifecycle(writer, request, actorID, parts[1])
		return
	}
	if len(parts) < 3 || parts[0] != "documents" {
		http.NotFound(writer, request)
		return
	}
	docID, err := uuid.Parse(parts[1])
	if err != nil {
		http.NotFound(writer, request)
		return
	}

	switch parts[2] {
	case "session":
		sess, err := handler.service.CreateSession(request.Context(), actorID, docID)
		if err != nil {
			writeOfficeServiceError(writer, err)
			return
		}
		writeOfficeJSON(writer, http.StatusOK, sess)
	case "restore":
		if err := handler.service.RestoreDocument(request.Context(), actorID, docID); err != nil {
			writeOfficeServiceError(writer, err)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	case "versions":
		if len(parts) != 5 || parts[4] != "restore" {
			http.NotFound(writer, request)
			return
		}
		versionID, err := uuid.Parse(parts[3])
		if err != nil {
			http.NotFound(writer, request)
			return
		}
		version, err := handler.service.RestoreVersion(
			request.Context(), actorID, docID, versionID,
		)
		if err != nil {
			writeOfficeServiceError(writer, err)
			return
		}
		writeOfficeJSON(writer, http.StatusCreated, version)
	case "artifact":
		handler.registerArtifact(writer, request, actorID, docID)
	default:
		http.NotFound(writer, request)
	}
}

func (handler officeHandler) handleClientLifecycle(
	writer http.ResponseWriter,
	request *http.Request,
	actorID uuid.UUID,
	rawSessionID string,
) {
	sessionID, err := uuid.Parse(rawSessionID)
	if err != nil {
		http.NotFound(writer, request)
		return
	}
	if err := handler.service.ValidateSessionActor(
		request.Context(), actorID, sessionID,
	); err != nil {
		writeOfficeServiceError(writer, err)
		return
	}

	request.Body = http.MaxBytesReader(writer, request.Body, 4<<10)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var input struct {
		Event     string `json:"event"`
		ElapsedMS int64  `json:"elapsed_ms"`
		ErrorCode *int   `json:"error_code,omitempty"`
	}
	if err := decoder.Decode(&input); err != nil || ensureJSONEnd(decoder) != nil ||
		!validOfficeClientEvent(input.Event) ||
		input.ElapsedMS < 0 || input.ElapsedMS > int64((24*time.Hour)/time.Millisecond) {
		writeOfficeError(writer, http.StatusBadRequest, "Invalid Office editor lifecycle event.")
		return
	}

	errorCode := "none"
	if input.ErrorCode != nil {
		errorCode = fmt.Sprintf("%d", *input.ErrorCode)
	}
	log.Printf(
		"office editor lifecycle session=%s event=%s elapsed_ms=%d error_code=%s",
		sessionID.String(), input.Event, input.ElapsedMS, errorCode,
	)
	writer.WriteHeader(http.StatusNoContent)
}

func validOfficeClientEvent(event string) bool {
	switch event {
	case "mount_started",
		"api_loaded",
		"constructor_started",
		"constructor_returned",
		"app_ready",
		"document_ready",
		"editor_error",
		"outdated_version",
		"cleanup_before_ready",
		"cleanup_after_ready",
		"api_load_failed",
		"cache_reset_failed":
		return true
	default:
		return false
	}
}

func (handler officeHandler) registerArtifact(
	writer http.ResponseWriter,
	request *http.Request,
	actorID, documentID uuid.UUID,
) {
	if handler.work == nil || strings.TrimSpace(handler.workspaceRoot) == "" {
		writeOfficeError(writer, http.StatusServiceUnavailable, "Artifact registration is unavailable.")
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, 1<<20)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var input struct {
		ContractID      uuid.UUID `json:"contract_id"`
		CriteriaCovered []string  `json:"criteria_covered"`
	}
	if err := decoder.Decode(&input); err != nil || ensureJSONEnd(decoder) != nil ||
		input.ContractID == uuid.Nil || len(input.CriteriaCovered) == 0 {
		writeOfficeError(writer, http.StatusBadRequest, "A contract and criterion coverage are required.")
		return
	}
	document, err := handler.service.GetDocument(request.Context(), actorID, documentID)
	if err != nil {
		writeOfficeServiceError(writer, err)
		return
	}
	content, _, _, err := handler.service.DownloadVersion(
		request.Context(), actorID, documentID, document.CurrentVersionID,
	)
	if err != nil {
		writeOfficeServiceError(writer, err)
		return
	}
	if len(content) > 32<<20 {
		writeOfficeError(writer, http.StatusRequestEntityTooLarge,
			"Office artifacts are limited to 32 MiB.")
		return
	}
	reference := filepath.Join(
		"office-artifacts",
		document.ID.String(),
		document.CurrentVersionID.String()+document.Extension,
	)
	path := filepath.Join(handler.workspaceRoot, reference)
	artifact, err := handler.work.RecordArtifactInWorkspace(
		request.Context(),
		actorID,
		workcontrol.ArtifactInput{
			ContractID:      input.ContractID,
			Kind:            "office_document",
			Title:           document.Title,
			Reference:       reference,
			CriteriaCovered: input.CriteriaCovered,
		},
		handler.workspaceRoot,
	)
	if err != nil {
		writeOfficeError(writer, http.StatusBadRequest, "The artifact contract or criterion coverage is invalid.")
		return
	}
	if err := writeOfficeArtifact(path, content); err != nil {
		writeOfficeError(writer, http.StatusInternalServerError, "The Office artifact could not be written.")
		return
	}
	artifact, err = handler.work.VerifyArtifactInWorkspace(
		request.Context(), actorID, artifact.ID, handler.workspaceRoot,
	)
	if err != nil {
		writeOfficeError(writer, http.StatusInternalServerError, "The Office artifact could not be verified.")
		return
	}
	writeOfficeJSON(writer, http.StatusCreated, artifact)
}

func writeOfficeArtifact(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("office: create artifact directory: %w", err)
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, content, 0o600); err != nil {
		return fmt.Errorf("office: write artifact: %w", err)
	}
	file, err := os.Open(temporary)
	if err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("office: open artifact: %w", err)
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	if syncErr != nil || closeErr != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("office: sync artifact")
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("office: commit artifact: %w", err)
	}
	return nil
}

func (handler officeHandler) patch(
	writer http.ResponseWriter,
	request *http.Request,
	actorID uuid.UUID,
	path string,
) {
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[0] != "documents" {
		http.NotFound(writer, request)
		return
	}
	docID, err := uuid.Parse(parts[1])
	if err != nil {
		http.NotFound(writer, request)
		return
	}

	request.Body = http.MaxBytesReader(writer, request.Body, 1<<20)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()

	var input struct {
		Title   *string `json:"title,omitempty"`
		Star    *bool   `json:"star,omitempty"`
		Archive *bool   `json:"archive,omitempty"`
	}
	if err := decoder.Decode(&input); err != nil {
		writeOfficeError(writer, http.StatusBadRequest, "Invalid request body.")
		return
	}
	if err := ensureJSONEnd(decoder); err != nil {
		writeOfficeError(writer, http.StatusBadRequest, "The request must contain one object.")
		return
	}

	if input.Title != nil {
		if err := handler.service.RenameDocument(request.Context(), actorID, docID, *input.Title); err != nil {
			writeOfficeServiceError(writer, err)
			return
		}
	}
	if input.Star != nil {
		if err := handler.service.StarDocument(request.Context(), actorID, docID, *input.Star); err != nil {
			writeOfficeServiceError(writer, err)
			return
		}
	}
	if input.Archive != nil {
		if *input.Archive {
			if err := handler.service.ArchiveDocument(request.Context(), actorID, docID); err != nil {
				writeOfficeServiceError(writer, err)
				return
			}
		} else {
			if err := handler.service.RestoreDocument(request.Context(), actorID, docID); err != nil {
				writeOfficeServiceError(writer, err)
				return
			}
		}
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (handler officeHandler) del(
	writer http.ResponseWriter,
	request *http.Request,
	actorID uuid.UUID,
	path string,
) {
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[0] != "documents" {
		http.NotFound(writer, request)
		return
	}
	docID, err := uuid.Parse(parts[1])
	if err != nil {
		http.NotFound(writer, request)
		return
	}
	if err := handler.service.DeleteDocument(request.Context(), actorID, docID); err != nil {
		writeOfficeServiceError(writer, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

// --- Machine endpoints (authenticated by signed tokens, not browser session) ---

func (handler officeHandler) machineEndpoints(
	writer http.ResponseWriter,
	request *http.Request,
	path string,
) {
	parts := strings.SplitN(strings.TrimPrefix(path, "machine/"), "/", 2)
	if len(parts) < 2 {
		http.NotFound(writer, request)
		return
	}
	endpoint := parts[0]
	token := parts[1]

	switch endpoint {
	case "files":
		handler.machineFileEndpoint(writer, request, token)
	case "callback":
		handler.machineCallbackEndpoint(writer, request, token)
	default:
		http.NotFound(writer, request)
	}
}

func (handler officeHandler) machineFileEndpoint(
	writer http.ResponseWriter,
	request *http.Request,
	token string,
) {
	if request.Method != http.MethodGet {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	content, contentType, filename, err := handler.service.SourceDocument(
		request.Context(), token,
	)
	if err != nil {
		writeOfficeServiceError(writer, err)
		return
	}
	writer.Header().Set("Content-Type", contentType)
	disposition := mime.FormatMediaType("inline", map[string]string{
		"filename": filename,
	})
	writer.Header().Set("Content-Disposition", disposition)
	writer.Header().Set("Content-Length", fmt.Sprintf("%d", len(content)))
	_, _ = writer.Write(content)
}

func (handler officeHandler) machineCallbackEndpoint(
	writer http.ResponseWriter,
	request *http.Request,
	token string,
) {
	if request.Method != http.MethodPost {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse callback body
	request.Body = http.MaxBytesReader(writer, request.Body, 1<<20)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var callbackReq officecontrol.CallbackRequest
	if err := decoder.Decode(&callbackReq); err != nil {
		writeOfficeError(writer, http.StatusBadRequest, "Invalid callback body.")
		return
	}
	if err := ensureJSONEnd(decoder); err != nil {
		writeOfficeError(writer, http.StatusBadRequest, "The callback must contain one object.")
		return
	}
	if callbackReq.Token == "" {
		callbackReq.Token = strings.TrimSpace(
			strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer "),
		)
	}
	if err := handler.service.ProcessCallback(request.Context(), token, callbackReq); err != nil {
		writeOfficeServiceError(writer, err)
		return
	}
	writeOfficeJSON(writer, http.StatusOK, map[string]int{"error": 0})
}

// --- Document creation and upload ---

func (handler officeHandler) handleCreate(
	writer http.ResponseWriter,
	request *http.Request,
	actorID uuid.UUID,
) {
	request.Body = http.MaxBytesReader(writer, request.Body, 1<<20)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var input officecontrol.CreateDocumentRequest
	if err := decoder.Decode(&input); err != nil {
		writeOfficeError(writer, http.StatusBadRequest, "Invalid document creation request.")
		return
	}
	if err := ensureJSONEnd(decoder); err != nil {
		writeOfficeError(writer, http.StatusBadRequest, "The request must contain one object.")
		return
	}
	doc, err := handler.service.CreateDocument(request.Context(), actorID, input)
	if err != nil {
		writeOfficeServiceError(writer, err)
		return
	}
	writeOfficeJSON(writer, http.StatusCreated, doc)
}

func (handler officeHandler) handleUpload(
	writer http.ResponseWriter,
	request *http.Request,
	actorID uuid.UUID,
) {
	request.Body = http.MaxBytesReader(writer, request.Body, handler.maxUpload+(1<<20))
	if err := request.ParseMultipartForm(8 << 20); err != nil {
		writeOfficeError(writer, http.StatusBadRequest, "Upload too large or invalid.")
		return
	}
	file, header, err := request.FormFile("file")
	if err != nil {
		writeOfficeError(writer, http.StatusBadRequest, "A file is required.")
		return
	}
	defer file.Close()

	content, err := io.ReadAll(io.LimitReader(file, handler.maxUpload+1))
	if err != nil || int64(len(content)) > handler.maxUpload {
		writeOfficeError(writer, http.StatusRequestEntityTooLarge, "File exceeds maximum size.")
		return
	}

	_, _, err = officecontrol.ValidateUploadedFile(header.Filename, content, handler.maxUpload)
	if err != nil {
		if errors.Is(err, officecontrol.ErrMacroDetected) {
			writeOfficeError(writer, http.StatusUnprocessableEntity,
				"Macro-enabled documents are not supported for editing. The file can be viewed in read-only mode.")
			return
		}
		writeOfficeError(writer, http.StatusBadRequest, err.Error())
		return
	}

	title := request.FormValue("title")
	if title == "" {
		title = header.Filename
	}

	doc, err := handler.service.CreateUploadedDocument(
		request.Context(), actorID, title, header.Filename, content,
	)
	if err != nil {
		writeOfficeServiceError(writer, err)
		return
	}
	writeOfficeJSON(writer, http.StatusCreated, doc)
}

// --- Routing helper ---

// RegisterOfficeRoutes mounts the office handler on the given mux.
func RegisterOfficeRoutes(
	mux *http.ServeMux,
	service *officecontrol.Service,
	authenticator controlplane.BrowserAuthenticator,
	origin string,
	work *workcontrol.Service,
	workspaceRoot string,
) {
	handler := officeHandler{
		service:       service,
		authenticator: authenticator,
		origin:        origin,
		maxUpload:     service.MaxFileBytes(),
		work:          work,
		workspaceRoot: workspaceRoot,
	}

	mux.Handle("/v1/office/", handler)
}

// --- Error helpers ---

func writeOfficeServiceError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, officecontrol.ErrNotConfigured):
		writeOfficeError(writer, http.StatusServiceUnavailable,
			"Office is not configured. Set ION_OFFICE_ENABLED=true and configure the engine.")
	case errors.Is(err, officecontrol.ErrNotFound):
		writeOfficeError(writer, http.StatusNotFound, "The document was not found.")
	case errors.Is(err, officecontrol.ErrUnauthorized):
		writeOfficeError(writer, http.StatusUnauthorized, "Unauthorized.")
	case errors.Is(err, officecontrol.ErrTooLarge):
		writeOfficeError(writer, http.StatusRequestEntityTooLarge, "The document exceeds the maximum size.")
	case errors.Is(err, officecontrol.ErrMacroDetected):
		writeOfficeError(writer, http.StatusUnprocessableEntity,
			"Macro-enabled documents are not supported for editing.")
	default:
		message := strings.TrimSpace(strings.TrimPrefix(err.Error(), "office: "))
		if message == "" {
			message = "The request could not be completed."
		}
		writeOfficeError(writer, http.StatusBadRequest, message)
	}
}

func writeOfficeError(writer http.ResponseWriter, status int, message string) {
	writeOfficeJSON(writer, status, map[string]string{"error": message})
}

func writeOfficeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
