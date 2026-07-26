package office

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrNotFound       = errors.New("office: not found")
	ErrNotConfigured  = errors.New("office: engine is not configured")
	ErrUnauthorized   = errors.New("office: unauthorized")
	ErrForbidden      = errors.New("office: forbidden")
	ErrConflict       = errors.New("office: conflict")
	ErrTooLarge       = errors.New("office: document exceeds maximum size")
	ErrInvalidFormat  = errors.New("office: unsupported or invalid format")
	ErrMacroDetected  = errors.New("office: macro-enabled format detected")
	ErrCallbackFailed = errors.New("office: save callback failed")
	ErrSessionExpired = errors.New("office: editor session expired")
)

// DocumentKind identifies the type of office document.
type DocumentKind string

const (
	KindDocument     DocumentKind = "document"
	KindSpreadsheet  DocumentKind = "spreadsheet"
	KindPresentation DocumentKind = "presentation"
	KindPDF          DocumentKind = "pdf"
	KindForm         DocumentKind = "form"
)

func (k DocumentKind) CanonicalExtension() string {
	switch k {
	case KindDocument:
		return ".docx"
	case KindSpreadsheet:
		return ".xlsx"
	case KindPresentation:
		return ".pptx"
	case KindPDF:
		return ".pdf"
	default:
		return ""
	}
}

func KindFromExtension(ext string) (DocumentKind, error) {
	switch strings.ToLower(ext) {
	case ".docx", ".doc":
		return KindDocument, nil
	case ".xlsx", ".xls":
		return KindSpreadsheet, nil
	case ".pptx", ".ppt":
		return KindPresentation, nil
	case ".pdf":
		return KindPDF, nil
	default:
		return "", fmt.Errorf("office: unsupported extension %q", ext)
	}
}

// VersionOrigin tracks how a version was created.
type VersionOrigin string

const (
	OriginBlankTemplate VersionOrigin = "blank_template"
	OriginUpload        VersionOrigin = "upload"
	OriginEditorSave    VersionOrigin = "editor_save"
	OriginForceSave     VersionOrigin = "force_save"
	OriginAgent         VersionOrigin = "agent"
	OriginRestore       VersionOrigin = "restore"
	OriginImport        VersionOrigin = "import"
)

// EditorSessionState tracks the lifecycle of an editor session.
type EditorSessionState string

const (
	SessionStateActive  EditorSessionState = "active"
	SessionStateClosed  EditorSessionState = "closed"
	SessionStateExpired EditorSessionState = "expired"
	SessionStateError   EditorSessionState = "error"
)

// CallbackStatus mirrors ONLYOFFICE callback status codes.
type CallbackStatus int

const (
	CallbackStatusEditing       CallbackStatus = 1
	CallbackStatusReady         CallbackStatus = 2
	CallbackStatusSaveError     CallbackStatus = 3
	CallbackStatusNoChanges     CallbackStatus = 4
	CallbackStatusForceSave     CallbackStatus = 6
	CallbackStatusForceSaveFail CallbackStatus = 7
)

// Document represents a managed office document.
type Document struct {
	ID               uuid.UUID    `json:"id"`
	ActorID          uuid.UUID    `json:"actor_id"`
	Title            string       `json:"title"`
	Kind             DocumentKind `json:"kind"`
	Extension        string       `json:"extension"`
	CurrentVersionID uuid.UUID    `json:"current_version_id"`
	Starred          bool         `json:"starred"`
	ArchivedAt       *time.Time   `json:"archived_at,omitempty"`
	CreatedAt        time.Time    `json:"created_at"`
	UpdatedAt        time.Time    `json:"updated_at"`
	DeletedAt        *time.Time   `json:"-"`
}

// DocumentVersion represents an immutable committed version.
type DocumentVersion struct {
	ID           uuid.UUID     `json:"id"`
	DocumentID   uuid.UUID     `json:"document_id"`
	ActorID      uuid.UUID     `json:"actor_id"`
	Sequence     int           `json:"sequence"`
	Extension    string        `json:"extension"`
	MIMEType     string        `json:"mime_type"`
	SHA256       string        `json:"sha256"`
	SizeBytes    int64         `json:"size_bytes"`
	Origin       VersionOrigin `json:"origin"`
	EngineDocKey string        `json:"engine_doc_key,omitempty"`
	CreatedAt    time.Time     `json:"created_at"`
	CreatedBy    string        `json:"created_by,omitempty"`
}

// EditorSession tracks an active editing session.
type EditorSession struct {
	ID             uuid.UUID          `json:"id"`
	ActorID        uuid.UUID          `json:"actor_id"`
	DocumentID     uuid.UUID          `json:"document_id"`
	VersionID      uuid.UUID          `json:"version_id"`
	EngineDocKey   string             `json:"engine_doc_key"`
	State          EditorSessionState `json:"state"`
	ExpiresAt      time.Time          `json:"expires_at"`
	LastCallbackAt *time.Time         `json:"last_callback_at,omitempty"`
	OpenedAt       time.Time          `json:"opened_at"`
	ClosedAt       *time.Time         `json:"closed_at,omitempty"`
}

// SaveCallback records a callback from the editor engine.
type SaveCallback struct {
	ID          string         `json:"id"`
	SessionID   uuid.UUID      `json:"session_id"`
	EngineKey   string         `json:"engine_key"`
	Status      CallbackStatus `json:"status"`
	URLDigest   string         `json:"url_digest"`
	Attempt     int            `json:"attempt"`
	Outcome     string         `json:"outcome"`
	VersionID   *uuid.UUID     `json:"version_id,omitempty"`
	ReceivedAt  time.Time      `json:"received_at"`
	CompletedAt *time.Time     `json:"completed_at,omitempty"`
}

// Template represents a bundled document template.
type Template struct {
	ID        uuid.UUID    `json:"id"`
	Kind      DocumentKind `json:"kind"`
	Name      string       `json:"name"`
	Extension string       `json:"extension"`
	SHA256    string       `json:"sha256"`
	SizeBytes int64        `json:"size_bytes"`
}

// StatusView reports engine status.
type StatusView struct {
	Configured bool   `json:"configured"`
	Available  bool   `json:"available"`
	Engine     string `json:"engine"`
	Message    string `json:"message"`
	Version    string `json:"version,omitempty"`
	PublicPath string `json:"public_path"`
}

// --- Request/Response types ---

type CreateDocumentRequest struct {
	Title      string       `json:"title"`
	Kind       DocumentKind `json:"kind"`
	TemplateID *uuid.UUID   `json:"template_id,omitempty"`
}

type UploadDocumentRequest struct {
	Title    string `json:"title"`
	Filename string `json:"filename"`
}

type RenameDocumentRequest struct {
	Title string `json:"title"`
}

type ExportRequest struct {
	Format string `json:"format"`
}

type DocumentView struct {
	ID             uuid.UUID    `json:"id"`
	Title          string       `json:"title"`
	Kind           DocumentKind `json:"kind"`
	Extension      string       `json:"extension"`
	Starred        bool         `json:"starred"`
	Archived       bool         `json:"archived"`
	CurrentVersion int          `json:"current_version"`
	CreatedAt      time.Time    `json:"created_at"`
	UpdatedAt      time.Time    `json:"updated_at"`
}

type VersionView struct {
	ID        uuid.UUID     `json:"id"`
	Sequence  int           `json:"sequence"`
	Extension string        `json:"extension"`
	SizeBytes int64         `json:"size_bytes"`
	Origin    VersionOrigin `json:"origin"`
	CreatedAt time.Time     `json:"created_at"`
	CreatedBy string        `json:"created_by"`
}

type SessionView struct {
	ID           uuid.UUID    `json:"id"`
	DocumentID   uuid.UUID    `json:"document_id"`
	EditorConfig EditorConfig `json:"editor_config"`
	ExpiresAt    time.Time    `json:"expires_at"`
}

func normalizeTitle(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Untitled"
	}
	runes := []rune(title)
	if len(runes) > 256 {
		title = string(runes[:256])
	}
	return title
}
