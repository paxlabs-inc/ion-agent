package office

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

// OfficeEngine is the provider-neutral contract for an office editing engine.
// Implementations wrap ONLYOFFICE Docs, Developer, or a future alternative.
type OfficeEngine interface {
	// Health checks engine availability and returns version info.
	Health(ctx context.Context) (EngineHealth, error)

	// BuildEditorConfig produces the configuration map for the editor iframe.
	BuildEditorConfig(ctx context.Context, req EditorConfigRequest) (EditorConfig, error)

	// ForceSave triggers an immediate save for the given document key.
	ForceSave(ctx context.Context, documentKey string) error

	// QueryDocumentStatus reports the engine's view of a document.
	QueryDocumentStatus(ctx context.Context, documentKey string) (DocumentEngineStatus, error)

	// Disconnect requests the engine to release resources for a document.
	Disconnect(ctx context.Context, documentKey string) error

	// Capabilities reports what the engine supports.
	Capabilities() EngineCapabilities
}

// EngineHealth reports engine status.
type EngineHealth struct {
	Available bool   `json:"available"`
	Version   string `json:"version,omitempty"`
	Message   string `json:"message"`
}

// EditorConfigRequest contains the data needed to build an editor config.
type EditorConfigRequest struct {
	DocumentID   uuid.UUID
	VersionID    uuid.UUID
	DocumentKey  string
	SourceURL    string
	CallbackURL  string
	Title        string
	DocumentType string // "word", "cell", "slide"
	FileType     string // "docx", "xlsx", "pptx"
	Mode         string // "edit" or "view"
	UserID       string
	DisplayName  string
	CanEdit      bool
	CanDownload  bool
	CanPrint     bool
	Language     string
}

// EditorConfig is the generated editor configuration.
type EditorConfig struct {
	Document     EditorDocument     `json:"document"`
	EditorConfig EditorEditorConfig `json:"editorConfig"`
	DocumentType string             `json:"documentType"`
	Token        string             `json:"token,omitempty"`
}

// EditorDocument represents the document section of editor config.
type EditorDocument struct {
	Key         string            `json:"key"`
	Title       string            `json:"title"`
	URL         string            `json:"url"`
	FileType    string            `json:"fileType"`
	Permissions EditorPermissions `json:"permissions"`
}

// EditorEditorConfig represents the editor configuration section.
type EditorEditorConfig struct {
	Mode          string              `json:"mode"`
	CallbackURL   string              `json:"callbackUrl"`
	User          EditorUser          `json:"user"`
	Lang          string              `json:"lang"`
	Customization EditorCustomization `json:"customization"`
}

// EditorUser represents the user in the editor.
type EditorUser struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// EditorPermissions represents document permissions.
type EditorPermissions struct {
	Edit     bool `json:"edit"`
	Download bool `json:"download"`
	Print    bool `json:"print"`
	Comment  bool `json:"comment"`
	Chat     bool `json:"chat"`
}

// EditorCustomization represents editor UI customization.
type EditorCustomization struct {
	CompactHeader  bool `json:"compactHeader"`
	CompactToolbar bool `json:"compactToolbar"`
	Feedback       bool `json:"feedback"`
	Help           bool `json:"help"`
	HideRightMenu  bool `json:"hideRightMenu"`
	ToolbarNoTabs  bool `json:"toolbarNoTabs"`
}

// DocumentEngineStatus represents the engine's view of a document.
type DocumentEngineStatus struct {
	Key      string     `json:"key"`
	URL      string     `json:"url,omitempty"`
	Editing  bool       `json:"editing"`
	LastSave *time.Time `json:"last_save,omitempty"`
}

// EngineCapabilities reports what the engine supports.
type EngineCapabilities struct {
	Engine           string   `json:"engine"`
	SupportedFormats []string `json:"supported_formats"`
	CanConvert       bool     `json:"can_convert"`
	CanForceSave     bool     `json:"can_force_save"`
	CanCoauthoring   bool     `json:"can_coauthoring"`
	MobileWebEditing bool     `json:"mobile_web_editing"`
	ConnectionLimit  int      `json:"connection_limit"`
	LicenseType      string   `json:"license_type"`
}

// --- ONLYOFFICE Engine Implementation ---

// OnlyOfficeEngineConfig configures the ONLYOFFICE adapter.
type OnlyOfficeEngineConfig struct {
	InternalURL    string
	JWTSecret      string
	PublicPath     string
	CallbackOrigin string
	MaxFileSize    int64
	HTTPClient     *http.Client
}

// OnlyOfficeEngine implements OfficeEngine for ONLYOFFICE Docs.
type OnlyOfficeEngine struct {
	internalURL    string
	jwtSecret      string
	publicPath     string
	callbackOrigin string
	maxFileSize    int64
	client         *http.Client
}

// NewOnlyOfficeEngine creates a new ONLYOFFICE engine adapter.
func NewOnlyOfficeEngine(config OnlyOfficeEngineConfig) (*OnlyOfficeEngine, error) {
	if config.InternalURL == "" {
		return nil, fmt.Errorf("office: ONLYOFFICE internal URL is required")
	}
	if config.JWTSecret == "" {
		return nil, fmt.Errorf("office: ONLYOFFICE JWT secret is required")
	}
	if config.PublicPath == "" {
		config.PublicPath = "/office-engine/"
	}
	if config.MaxFileSize <= 0 {
		config.MaxFileSize = 100 << 20 // 100 MiB
	}
	internalURL, err := url.Parse(strings.TrimRight(config.InternalURL, "/"))
	if err != nil || internalURL.Scheme == "" || internalURL.Host == "" ||
		(internalURL.Scheme != "http" && internalURL.Scheme != "https") ||
		internalURL.User != nil || internalURL.RawQuery != "" ||
		internalURL.Fragment != "" {
		return nil, fmt.Errorf("office: ONLYOFFICE internal URL is invalid")
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &OnlyOfficeEngine{
		internalURL:    internalURL.String(),
		jwtSecret:      config.JWTSecret,
		publicPath:     config.PublicPath,
		callbackOrigin: config.CallbackOrigin,
		maxFileSize:    config.MaxFileSize,
		client:         config.HTTPClient,
	}, nil
}

func (e *OnlyOfficeEngine) Health(ctx context.Context) (EngineHealth, error) {
	statusCode, body, err := e.request(ctx, http.MethodGet, "/healthcheck", nil, nil)
	if err != nil {
		return EngineHealth{
			Available: false,
			Message:   fmt.Sprintf("ONLYOFFICE health check failed: %v", err),
		}, nil
	}
	if statusCode != 200 {
		return EngineHealth{
			Available: false,
			Message:   fmt.Sprintf("ONLYOFFICE returned status %d", statusCode),
		}, nil
	}
	_ = body
	return EngineHealth{
		Available: true,
		Version:   "community",
		Message:   "ONLYOFFICE Docs Community Edition is running.",
	}, nil
}

func (e *OnlyOfficeEngine) BuildEditorConfig(ctx context.Context, req EditorConfigRequest) (EditorConfig, error) {
	docType := req.DocumentType
	if docType == "" {
		switch req.FileType {
		case "docx", "doc":
			docType = "word"
		case "xlsx", "xls":
			docType = "cell"
		case "pptx", "ppt":
			docType = "slide"
		case "pdf":
			docType = "pdf"
		default:
			docType = "word"
		}
	}

	editorConfig := EditorConfig{
		Document: EditorDocument{
			Key:      req.DocumentKey,
			Title:    req.Title,
			URL:      req.SourceURL,
			FileType: req.FileType,
			Permissions: EditorPermissions{
				Edit:     req.CanEdit,
				Download: req.CanDownload,
				Print:    req.CanPrint,
				Comment:  req.CanEdit,
				Chat:     false,
			},
		},
		DocumentType: docType,
		EditorConfig: EditorEditorConfig{
			Mode:        req.Mode,
			CallbackURL: req.CallbackURL,
			User: EditorUser{
				ID:   req.UserID,
				Name: req.DisplayName,
			},
			Lang: req.Language,
			Customization: EditorCustomization{
				CompactHeader:  true,
				CompactToolbar: true,
				Feedback:       false,
				Help:           true,
			},
		},
	}

	// Sign with JWT if secret is available
	if e.jwtSecret != "" {
		token, err := signEditorJWT(editorConfig, e.jwtSecret)
		if err != nil {
			return EditorConfig{}, fmt.Errorf("office: sign editor config: %w", err)
		}
		editorConfig.Token = token
	}

	return editorConfig, nil
}

func (e *OnlyOfficeEngine) ForceSave(ctx context.Context, documentKey string) error {
	_, err := e.runCommand(ctx, map[string]any{
		"c": "forcesave", "key": documentKey,
	})
	return err
}

func (e *OnlyOfficeEngine) QueryDocumentStatus(ctx context.Context, documentKey string) (DocumentEngineStatus, error) {
	result, err := e.runCommand(ctx, map[string]any{
		"c": "info", "key": documentKey,
	})
	if err != nil {
		return DocumentEngineStatus{}, err
	}
	return DocumentEngineStatus{
		Key: result.Key, Editing: len(result.Users) > 0,
	}, nil
}

func (e *OnlyOfficeEngine) Disconnect(ctx context.Context, documentKey string) error {
	status, err := e.runCommand(ctx, map[string]any{
		"c": "info", "key": documentKey,
	})
	if err != nil {
		return err
	}
	if len(status.Users) == 0 {
		return nil
	}
	_, err = e.runCommand(ctx, map[string]any{
		"c": "drop", "key": documentKey, "users": status.Users,
	})
	return err
}

func (e *OnlyOfficeEngine) Capabilities() EngineCapabilities {
	return EngineCapabilities{
		Engine:           "ONLYOFFICE Docs Community Edition",
		SupportedFormats: []string{"docx", "xlsx", "pptx", "pdf"},
		CanConvert:       false,
		CanForceSave:     true,
		CanCoauthoring:   true,
		MobileWebEditing: false, // Community limitation
		ConnectionLimit:  20,    // Community concurrent connection limit
		LicenseType:      "AGPLv3",
	}
}

type commandResult struct {
	Error int      `json:"error"`
	Key   string   `json:"key"`
	Users []string `json:"users"`
}

func (e *OnlyOfficeEngine) runCommand(
	ctx context.Context,
	command map[string]any,
) (commandResult, error) {
	var result commandResult
	key, _ := command["key"].(string)
	if key == "" {
		return result, fmt.Errorf("office: command document key is required")
	}
	claims, err := json.Marshal(command)
	if err != nil {
		return result, err
	}
	token, err := signJWTBytes(claims, e.jwtSecret)
	if err != nil {
		return result, err
	}
	payload, err := json.Marshal(map[string]string{"token": token})
	if err != nil {
		return result, err
	}
	statusCode, body, err := e.request(
		ctx,
		http.MethodPost,
		"/command?shardkey="+url.QueryEscape(key),
		payload,
		nil,
	)
	if err != nil {
		return result, fmt.Errorf("office: command request: %w", err)
	}
	if statusCode != http.StatusOK {
		return result, fmt.Errorf("office: command returned status %d", statusCode)
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return result, fmt.Errorf("office: invalid command response")
	}
	if result.Error != 0 {
		return result, fmt.Errorf("office: command rejected with code %d", result.Error)
	}
	return result, nil
}

// signEditorJWT creates a JWT token for the editor configuration.
func (e *OnlyOfficeEngine) request(
	ctx context.Context,
	method string,
	path string,
	body []byte,
	headers map[string]string,
) (int, []byte, error) {
	request, err := http.NewRequestWithContext(
		ctx, method, e.internalURL+path, bytes.NewReader(body),
	)
	if err != nil {
		return 0, nil, err
	}
	if len(body) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := e.client.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	content, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return 0, nil, err
	}
	return response.StatusCode, content, nil
}
