package office

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

// Cipher encrypts and decrypts document content at rest.
type Cipher interface {
	Encrypt(plaintext []byte) ([]byte, error)
	Decrypt(ciphertext []byte) ([]byte, error)
}

// Config configures the Office service.
type Config struct {
	DataDirectory  string
	EngineURL      string
	JWTSecret      string
	PublicPath     string
	CallbackOrigin string
	MaxFileBytes   int64
	MaxVersions    int
	Cipher         Cipher
	Clock          types.Clock
	HTTPClient     *http.Client
}

// Service manages office documents, versions, and editor sessions.
type Service struct {
	store          *Store
	engine         OfficeEngine
	cipher         Cipher
	clock          types.Clock
	docRoot        string
	maxFileBytes   int64
	maxVersions    int
	callbackOrigin string
	jwtSecret      string
	publicPath     string
	httpClient     *http.Client
	versionMu      sync.Mutex
}

// Open creates or opens the Office service.
func Open(ctx context.Context, config Config) (*Service, error) {
	if config.DataDirectory == "" || config.Cipher == nil || config.Clock == nil {
		return nil, fmt.Errorf("office: data directory, cipher, and clock are required")
	}
	root := filepath.Join(filepath.Clean(config.DataDirectory), "office")
	docRoot := filepath.Join(root, "documents")
	if err := os.MkdirAll(docRoot, 0o700); err != nil {
		return nil, fmt.Errorf("office: create document directory: %w", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return nil, fmt.Errorf("office: secure data directory: %w", err)
	}
	store, err := OpenStore(ctx, filepath.Join(root, "office.db"), config.Clock)
	if err != nil {
		return nil, err
	}
	maxFileBytes := config.MaxFileBytes
	if maxFileBytes <= 0 {
		maxFileBytes = 100 << 20
	}
	if maxFileBytes > 1<<30 {
		_ = store.Close()
		return nil, fmt.Errorf("office: maximum file size cannot exceed 1 GiB")
	}
	maxVersions := config.MaxVersions
	if maxVersions <= 0 {
		maxVersions = 100
	}
	if maxVersions > 10_000 {
		_ = store.Close()
		return nil, fmt.Errorf("office: maximum versions cannot exceed 10000")
	}
	publicPath := strings.TrimSpace(config.PublicPath)
	if publicPath == "" {
		publicPath = "/office-engine/"
	}
	if !strings.HasPrefix(publicPath, "/") {
		_ = store.Close()
		return nil, fmt.Errorf("office: public path must be absolute")
	}
	if strings.Contains(publicPath, "..") ||
		strings.ContainsAny(publicPath, "?#") {
		_ = store.Close()
		return nil, fmt.Errorf("office: public path is invalid")
	}
	if !strings.HasSuffix(publicPath, "/") {
		publicPath += "/"
	}

	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Minute}
	}
	callbackOrigin := strings.TrimRight(strings.TrimSpace(config.CallbackOrigin), "/")
	if config.EngineURL != "" {
		parsed, parseErr := url.Parse(callbackOrigin)
		if parseErr != nil || parsed.Scheme == "" || parsed.Host == "" ||
			(parsed.Scheme != "http" && parsed.Scheme != "https") ||
			parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") ||
			parsed.RawQuery != "" || parsed.Fragment != "" {
			_ = store.Close()
			return nil, fmt.Errorf("office: callback origin is required when the engine is configured")
		}
		if parsed.Scheme != "https" && !officeLoopbackHost(parsed.Hostname()) {
			_ = store.Close()
			return nil, fmt.Errorf("office: callback origin must use HTTPS outside loopback")
		}
		if len(config.JWTSecret) < 32 {
			_ = store.Close()
			return nil, fmt.Errorf("office: JWT secret must contain at least 32 bytes")
		}
	}

	svc := &Service{
		store:          store,
		cipher:         config.Cipher,
		clock:          config.Clock,
		docRoot:        docRoot,
		maxFileBytes:   maxFileBytes,
		maxVersions:    maxVersions,
		callbackOrigin: callbackOrigin,
		jwtSecret:      config.JWTSecret,
		publicPath:     publicPath,
		httpClient:     httpClient,
	}

	// Initialize engine if URL is configured
	if config.EngineURL != "" {
		engine, err := NewOnlyOfficeEngine(OnlyOfficeEngineConfig{
			InternalURL:    config.EngineURL,
			JWTSecret:      config.JWTSecret,
			PublicPath:     publicPath,
			CallbackOrigin: config.CallbackOrigin,
			MaxFileSize:    maxFileBytes,
			HTTPClient:     httpClient,
		})
		if err != nil {
			_ = store.Close()
			return nil, err
		}
		svc.engine = engine
	}

	// Seed bundled templates
	if err := svc.seedTemplates(ctx); err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("office: seed templates: %w", err)
	}
	if err := svc.reconcileSessions(ctx); err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("office: reconcile sessions: %w", err)
	}

	return svc, nil
}

func officeLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

// Status returns the engine status.
func (s *Service) Status(ctx context.Context) StatusView {
	if s.engine == nil {
		return StatusView{
			Configured: false,
			Available:  false,
			Engine:     "ONLYOFFICE",
			Message:    "Office is not configured. Set ION_OFFICE_ENABLED=true and configure ION_OFFICE_INTERNAL_URL.",
			PublicPath: s.publicPath,
		}
	}
	health, err := s.engine.Health(ctx)
	if err != nil || !health.Available {
		message := health.Message
		if err != nil {
			message = "ONLYOFFICE health check failed."
		}
		return StatusView{
			Configured: true,
			Available:  false,
			Engine:     "ONLYOFFICE",
			Message:    message,
			PublicPath: s.publicPath,
		}
	}
	return StatusView{
		Configured: true,
		Available:  true,
		Engine:     "ONLYOFFICE",
		Message:    health.Message,
		Version:    health.Version,
		PublicPath: s.publicPath,
	}
}

// CreateDocument creates a new document from a template.
func (s *Service) CreateDocument(ctx context.Context, actorID uuid.UUID, input CreateDocumentRequest) (Document, error) {
	if s.engine == nil {
		return Document{}, ErrNotConfigured
	}
	title := normalizeTitle(input.Title)
	kind := input.Kind
	if kind == "" {
		kind = KindDocument
	}
	ext := kind.CanonicalExtension()
	if ext == "" {
		return Document{}, fmt.Errorf("office: unsupported document kind %q", kind)
	}

	// Load template content
	var content []byte
	if input.TemplateID != nil {
		tmpl, err := s.store.GetTemplate(ctx, *input.TemplateID)
		if err != nil {
			return Document{}, fmt.Errorf("office: template not found")
		}
		if tmpl.Kind != kind {
			return Document{}, fmt.Errorf("office: template does not match document kind")
		}
		tmplPath := filepath.Join(s.docRoot, "templates", tmpl.ID.String()+tmpl.Extension)
		content, err = os.ReadFile(tmplPath)
		if err != nil {
			return Document{}, fmt.Errorf("office: read template: %w", err)
		}
	} else {
		tmplContent, err := s.getBlankTemplate(kind)
		if err != nil {
			return Document{}, err
		}
		content = tmplContent
	}

	return s.createDocumentFromContent(
		ctx, actorID, title, kind, ext, OriginBlankTemplate, content,
	)
}

// CreateUploadedDocument validates and stores uploaded content as the first
// immutable version without substituting a blank template.
func (s *Service) CreateUploadedDocument(
	ctx context.Context,
	actorID uuid.UUID,
	title string,
	filename string,
	content []byte,
) (Document, error) {
	if s.engine == nil {
		return Document{}, ErrNotConfigured
	}
	ext, _, err := ValidateUploadedFile(filename, content, s.maxFileBytes)
	if err != nil {
		return Document{}, err
	}
	kind, err := KindFromExtension(ext)
	if err != nil {
		return Document{}, err
	}
	return s.createDocumentFromContent(
		ctx, actorID, normalizeTitle(title), kind, ext, OriginUpload, content,
	)
}

func (s *Service) createDocumentFromContent(
	ctx context.Context,
	actorID uuid.UUID,
	title string,
	kind DocumentKind,
	extension string,
	origin VersionOrigin,
	content []byte,
) (Document, error) {
	now := s.clock.Now().UTC()
	doc := Document{
		ID:        uuid.New(),
		ActorID:   actorID,
		Title:     title,
		Kind:      kind,
		Extension: extension,
		CreatedAt: now,
		UpdatedAt: now,
	}

	// Create initial version
	hash := sha256.Sum256(content)
	ver := DocumentVersion{
		ID:         uuid.New(),
		DocumentID: doc.ID,
		ActorID:    actorID,
		Sequence:   1,
		Extension:  extension,
		MIMEType:   mimeTypeForExt(extension),
		SHA256:     hex.EncodeToString(hash[:]),
		SizeBytes:  int64(len(content)),
		Origin:     origin,
		CreatedAt:  now,
		CreatedBy:  "system",
	}

	// Encrypt and store the document
	if err := s.storeVersionContent(ver, content); err != nil {
		return Document{}, err
	}

	doc.CurrentVersionID = ver.ID

	// Persist metadata
	if err := s.store.CreateDocumentWithVersion(ctx, doc, ver); err != nil {
		_ = s.removeVersionContent(ver)
		return Document{}, err
	}

	return doc, nil
}

// ListDocuments returns documents for an actor.
func (s *Service) ListDocuments(ctx context.Context, actorID uuid.UUID, archived bool) ([]Document, error) {
	return s.store.ListDocuments(ctx, actorID, archived, 50)
}

// GetDocument returns a specific document.
func (s *Service) GetDocument(ctx context.Context, actorID, docID uuid.UUID) (Document, error) {
	return s.store.GetDocument(ctx, actorID, docID)
}

// RenameDocument updates a document's title.
func (s *Service) RenameDocument(ctx context.Context, actorID, docID uuid.UUID, title string) error {
	t := normalizeTitle(title)
	return s.store.UpdateDocument(ctx, actorID, docID, &t, nil)
}

// StarDocument toggles the starred state.
func (s *Service) StarDocument(ctx context.Context, actorID, docID uuid.UUID, starred bool) error {
	return s.store.UpdateDocument(ctx, actorID, docID, nil, &starred)
}

// ArchiveDocument archives a document.
func (s *Service) ArchiveDocument(ctx context.Context, actorID, docID uuid.UUID) error {
	return s.store.ArchiveDocument(ctx, actorID, docID)
}

// RestoreDocument restores an archived document.
func (s *Service) RestoreDocument(ctx context.Context, actorID, docID uuid.UUID) error {
	return s.store.RestoreDocument(ctx, actorID, docID)
}

// DeleteDocument soft-deletes a document.
func (s *Service) DeleteDocument(ctx context.Context, actorID, docID uuid.UUID) error {
	return s.store.DeleteDocument(ctx, actorID, docID)
}

// CreateSession creates an editor session for a document.
func (s *Service) CreateSession(ctx context.Context, actorID, docID uuid.UUID) (SessionView, error) {
	if s.engine == nil {
		return SessionView{}, ErrNotConfigured
	}
	doc, err := s.store.GetDocument(ctx, actorID, docID)
	if err != nil {
		return SessionView{}, err
	}
	ver, err := s.store.GetVersion(ctx, docID, doc.CurrentVersionID)
	if err != nil {
		return SessionView{}, err
	}

	now := s.clock.Now().UTC()
	sess, activeErr := s.store.GetActiveSession(ctx, actorID, docID)
	if activeErr == nil &&
		(!sess.ExpiresAt.After(now) || sess.VersionID != ver.ID) {
		state := SessionStateClosed
		if !sess.ExpiresAt.After(now) {
			state = SessionStateExpired
		}
		if err := s.store.UpdateSessionState(ctx, sess.ID, state); err != nil {
			return SessionView{}, err
		}
		activeErr = ErrNotFound
	}
	if activeErr != nil && !errors.Is(activeErr, ErrNotFound) {
		return SessionView{}, activeErr
	}
	if errors.Is(activeErr, ErrNotFound) {
		sessionID := uuid.New()
		sess = EditorSession{
			ID:         sessionID,
			ActorID:    actorID,
			DocumentID: docID,
			VersionID:  ver.ID,
			EngineDocKey: fmt.Sprintf(
				"ion-%s-%d-%s",
				doc.ID.String()[:8],
				ver.Sequence,
				sessionID.String()[:12],
			),
			State:     SessionStateActive,
			ExpiresAt: now.Add(24 * time.Hour),
			OpenedAt:  now,
		}
		if err := s.store.CreateSession(ctx, sess); err != nil {
			return SessionView{}, err
		}
	}

	// Build source URL (signed, short-lived)
	sourceURL, err := s.buildSourceURL(
		doc.ID, ver.ID, sess.ExpiresAt.Sub(s.clock.Now().UTC()),
	)
	if err != nil {
		_ = s.store.UpdateSessionState(ctx, sess.ID, SessionStateError)
		return SessionView{}, err
	}

	// Build callback URL
	callbackURL, err := s.buildCallbackURL(sess)
	if err != nil {
		_ = s.store.UpdateSessionState(ctx, sess.ID, SessionStateError)
		return SessionView{}, err
	}

	fileType := strings.TrimPrefix(doc.Extension, ".")

	mode := "edit"
	canEdit := doc.Kind != KindPDF
	if !canEdit {
		mode = "view"
	}

	editorConfig, err := s.engine.BuildEditorConfig(ctx, EditorConfigRequest{
		DocumentID:   doc.ID,
		VersionID:    ver.ID,
		DocumentKey:  sess.EngineDocKey,
		SourceURL:    sourceURL,
		CallbackURL:  callbackURL,
		Title:        doc.Title,
		DocumentType: "",
		FileType:     fileType,
		Mode:         mode,
		UserID:       actorID.String(),
		DisplayName:  "User",
		CanEdit:      canEdit,
		CanDownload:  true,
		CanPrint:     true,
		Language:     "en",
	})
	if err != nil {
		_ = s.store.UpdateSessionState(ctx, sess.ID, SessionStateError)
		return SessionView{}, err
	}

	return SessionView{
		ID:           sess.ID,
		DocumentID:   docID,
		EditorConfig: editorConfig,
		ExpiresAt:    sess.ExpiresAt,
	}, nil
}

// ValidateSessionActor confirms that an editor session belongs to the actor.
func (s *Service) ValidateSessionActor(
	ctx context.Context,
	actorID, sessionID uuid.UUID,
) error {
	session, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}
	if session.ActorID != actorID {
		return ErrNotFound
	}
	return nil
}

// ListVersions returns versions for a document.
func (s *Service) ListVersions(ctx context.Context, actorID, docID uuid.UUID) ([]DocumentVersion, error) {
	// Verify access
	if _, err := s.store.GetDocument(ctx, actorID, docID); err != nil {
		return nil, err
	}
	return s.store.ListVersions(ctx, docID, 50)
}

// RestoreVersion creates a new immutable current version from historical
// content; it never rewrites or re-labels the historical record.
func (s *Service) RestoreVersion(
	ctx context.Context,
	actorID, documentID, versionID uuid.UUID,
) (DocumentVersion, error) {
	s.versionMu.Lock()
	defer s.versionMu.Unlock()
	if _, err := s.store.GetDocument(ctx, actorID, documentID); err != nil {
		return DocumentVersion{}, err
	}
	source, err := s.store.GetVersion(ctx, documentID, versionID)
	if err != nil {
		return DocumentVersion{}, err
	}
	versionCount, err := s.store.CountVersions(ctx, documentID)
	if err != nil {
		return DocumentVersion{}, err
	}
	if versionCount >= s.maxVersions {
		return DocumentVersion{}, fmt.Errorf("office: document version limit reached")
	}
	content, err := s.loadVersionContent(documentID, source)
	if err != nil {
		return DocumentVersion{}, err
	}
	sequence, err := s.store.NextSequence(ctx, documentID)
	if err != nil {
		return DocumentVersion{}, err
	}
	now := s.clock.Now().UTC()
	version := DocumentVersion{
		ID:         uuid.New(),
		DocumentID: documentID,
		ActorID:    actorID,
		Sequence:   sequence,
		Extension:  source.Extension,
		MIMEType:   source.MIMEType,
		SHA256:     source.SHA256,
		SizeBytes:  source.SizeBytes,
		Origin:     OriginRestore,
		CreatedAt:  now,
		CreatedBy:  actorID.String(),
	}
	if err := s.storeVersionContent(version, content); err != nil {
		return DocumentVersion{}, err
	}
	if err := s.store.CommitVersion(ctx, actorID, version); err != nil {
		_ = s.removeVersionContent(version)
		return DocumentVersion{}, err
	}
	return version, nil
}

// DownloadVersion returns the content of a version.
func (s *Service) DownloadVersion(ctx context.Context, actorID, docID, versionID uuid.UUID) ([]byte, string, string, error) {
	doc, err := s.store.GetDocument(ctx, actorID, docID)
	if err != nil {
		return nil, "", "", err
	}
	ver, err := s.store.GetVersion(ctx, docID, versionID)
	if err != nil {
		return nil, "", "", err
	}
	content, err := s.loadVersionContent(doc.ID, ver)
	if err != nil {
		return nil, "", "", err
	}
	return content, ver.MIMEType, doc.Title + ver.Extension, nil
}

// SourceDocument resolves a short-lived, purpose-scoped machine token and
// returns exactly the immutable version bound into that token.
func (s *Service) SourceDocument(
	ctx context.Context,
	token string,
) ([]byte, string, string, error) {
	payload, err := verifyFileToken(token, s.jwtSecret, s.clock.Now().UTC())
	if err != nil {
		return nil, "", "", ErrUnauthorized
	}
	documentID, err := uuid.Parse(payload.DocID)
	if err != nil {
		return nil, "", "", ErrUnauthorized
	}
	versionID, err := uuid.Parse(payload.VersionID)
	if err != nil {
		return nil, "", "", ErrUnauthorized
	}
	version, err := s.store.GetVersion(ctx, documentID, versionID)
	if err != nil {
		return nil, "", "", err
	}
	content, err := s.loadVersionContent(documentID, version)
	if err != nil {
		return nil, "", "", err
	}
	return content, version.MIMEType, "document" + version.Extension, nil
}

// ProcessCallback authenticates both Ion's scoped callback URL and the engine
// JWT before dispatching the idempotent callback state machine.
func (s *Service) ProcessCallback(
	ctx context.Context,
	token string,
	request CallbackRequest,
) error {
	payload, err := verifyCallbackToken(token, s.jwtSecret, s.clock.Now().UTC())
	if err != nil {
		return ErrUnauthorized
	}
	sessionID, err := uuid.Parse(payload.SessionID)
	if err != nil {
		return ErrUnauthorized
	}
	session, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return ErrUnauthorized
	}
	if payload.ActorID != session.ActorID.String() ||
		payload.DocumentID != session.DocumentID.String() ||
		payload.EngineKey != session.EngineDocKey ||
		request.Key != session.EngineDocKey {
		return ErrUnauthorized
	}
	claims, err := verifyEditorJWT(
		request.Token, s.jwtSecret, s.clock.Now().UTC(),
	)
	if err != nil || !callbackClaimsMatch(claims, request) {
		return ErrUnauthorized
	}
	if !session.ExpiresAt.After(s.clock.Now().UTC()) {
		_ = s.store.UpdateSessionState(ctx, session.ID, SessionStateExpired)
		return ErrSessionExpired
	}
	return s.HandleCallback(ctx, session.ID, request)
}

func callbackClaimsMatch(claims map[string]any, request CallbackRequest) bool {
	if nested, ok := claims["payload"].(map[string]any); ok {
		claims = nested
	}
	key, keyOK := claims["key"].(string)
	status, statusOK := numericClaim(claims, "status")
	return keyOK && key == request.Key &&
		statusOK && int(status) == int(request.Status)
}

// ListTemplates returns available templates.
func (s *Service) ListTemplates(ctx context.Context) ([]Template, error) {
	return s.store.ListTemplates(ctx)
}

// Capabilities returns engine capabilities.
func (s *Service) Capabilities() EngineCapabilities {
	if s.engine == nil {
		return EngineCapabilities{
			Engine: "none",
		}
	}
	return s.engine.Capabilities()
}

func (s *Service) MaxFileBytes() int64 {
	return s.maxFileBytes
}

// Close releases resources.
func (s *Service) Close() error {
	return s.store.Close()
}

// --- Internal helpers ---

func (s *Service) storeVersionContent(ver DocumentVersion, content []byte) error {
	encrypted, err := s.cipher.Encrypt(content)
	if err != nil {
		return fmt.Errorf("office: encrypt version: %w", err)
	}
	dir := filepath.Join(s.docRoot, ver.DocumentID.String())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("office: create version directory: %w", err)
	}
	filename := fmt.Sprintf("%d%s.enc", ver.Sequence, ver.Extension)
	path := filepath.Join(dir, filename)
	tmpPath := path + ".tmp"

	if err := os.WriteFile(tmpPath, encrypted, 0o600); err != nil {
		return fmt.Errorf("office: write version: %w", err)
	}
	if err := syncFile(tmpPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("office: atomic rename: %w", err)
	}
	return nil
}

func (s *Service) loadVersionContent(docID uuid.UUID, ver DocumentVersion) ([]byte, error) {
	dir := filepath.Join(s.docRoot, docID.String())
	filename := fmt.Sprintf("%d%s.enc", ver.Sequence, ver.Extension)
	path := filepath.Join(dir, filename)
	encrypted, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("office: read version: %w", err)
	}
	content, err := s.cipher.Decrypt(encrypted)
	if err != nil {
		return nil, fmt.Errorf("office: decrypt version: %w", err)
	}
	hash := sha256.Sum256(content)
	actualHash := hex.EncodeToString(hash[:])
	if actualHash != ver.SHA256 {
		return nil, fmt.Errorf("office: version integrity mismatch")
	}
	return content, nil
}

func (s *Service) removeVersionContent(ver DocumentVersion) error {
	return os.Remove(filepath.Join(
		s.docRoot,
		ver.DocumentID.String(),
		fmt.Sprintf("%d%s.enc", ver.Sequence, ver.Extension),
	))
}

func (s *Service) buildSourceURL(
	docID, versionID uuid.UUID,
	ttl time.Duration,
) (string, error) {
	// Returns a signed URL for ONLYOFFICE to download the source document
	if ttl <= 0 || ttl > 24*time.Hour {
		return "", ErrSessionExpired
	}
	token, err := signFileTokenAt(
		docID, versionID, s.jwtSecret, s.clock.Now().UTC(), ttl,
	)
	if err != nil {
		return "", err
	}
	return s.callbackOrigin + "/v1/office/machine/files/" + token, nil
}

func (s *Service) buildCallbackURL(session EditorSession) (string, error) {
	token, err := signCallbackToken(
		session, s.jwtSecret, s.clock.Now().UTC(), 24*time.Hour,
	)
	if err != nil {
		return "", err
	}
	return s.callbackOrigin + "/v1/office/machine/callback/" + token, nil
}

func (s *Service) getBlankTemplate(kind DocumentKind) ([]byte, error) {
	dir := filepath.Join(s.docRoot, "templates")
	switch kind {
	case KindDocument:
		return os.ReadFile(filepath.Join(dir, "blank.docx"))
	case KindSpreadsheet:
		return os.ReadFile(filepath.Join(dir, "blank.xlsx"))
	case KindPresentation:
		return os.ReadFile(filepath.Join(dir, "blank.pptx"))
	default:
		return nil, fmt.Errorf("office: no blank template for kind %q", kind)
	}
}

func (s *Service) seedTemplates(ctx context.Context) error {
	dir := filepath.Join(s.docRoot, "templates")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	for _, tmpl := range bundledTemplates {
		if _, _, err := ValidateUploadedFile(
			tmpl.Filename, tmpl.Content, s.maxFileBytes,
		); err != nil {
			return fmt.Errorf("office: invalid bundled template %s: %w", tmpl.Filename, err)
		}
		path := filepath.Join(dir, tmpl.Filename)
		hash := sha256.Sum256(tmpl.Content)
		current, err := os.ReadFile(path)
		if err != nil || sha256.Sum256(current) != hash {
			if err := os.WriteFile(path, tmpl.Content, 0o600); err != nil {
				return err
			}
		}
		t := Template{
			ID:        tmpl.ID,
			Kind:      tmpl.Kind,
			Name:      tmpl.Name,
			Extension: tmpl.Extension,
			SHA256:    hex.EncodeToString(hash[:]),
			SizeBytes: int64(len(tmpl.Content)),
		}
		if err := s.store.UpsertTemplate(ctx, t); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) reconcileSessions(ctx context.Context) error {
	sessions, err := s.store.ListActiveSessions(ctx)
	if err != nil {
		return err
	}
	now := s.clock.Now().UTC()
	for _, session := range sessions {
		if !session.ExpiresAt.After(now) {
			if err := s.store.UpdateSessionState(
				ctx, session.ID, SessionStateExpired,
			); err != nil {
				return err
			}
			continue
		}
		if s.engine == nil {
			continue
		}
		status, err := s.engine.QueryDocumentStatus(ctx, session.EngineDocKey)
		if err != nil {
			continue
		}
		if !status.Editing {
			if err := s.store.UpdateSessionState(
				ctx, session.ID, SessionStateClosed,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

func syncFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func mimeTypeForExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case ".xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case ".pptx":
		return "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	case ".pdf":
		return "application/pdf"
	case ".doc":
		return "application/msword"
	case ".xls":
		return "application/vnd.ms-excel"
	case ".ppt":
		return "application/vnd.ms-powerpoint"
	default:
		return "application/octet-stream"
	}
}
