package office

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/google/uuid"
)

// CallbackRequest represents a parsed ONLYOFFICE save callback.
type CallbackRequest struct {
	Status        CallbackStatus   `json:"status"`
	URL           string           `json:"url"`
	Key           string           `json:"key"`
	Token         string           `json:"token,omitempty"`
	Users         []string         `json:"users,omitempty"`
	Actions       []CallbackAction `json:"actions,omitempty"`
	ChangesURL    string           `json:"changesurl,omitempty"`
	Changes       json.RawMessage  `json:"changeshistory,omitempty"`
	History       json.RawMessage  `json:"history,omitempty"`
	FileType      string           `json:"filetype,omitempty"`
	ForceSaveType int              `json:"forcesavetype,omitempty"`
	FormsDataURL  string           `json:"formsdataurl,omitempty"`
	UserData      json.RawMessage  `json:"userdata,omitempty"`
}

type CallbackAction struct {
	UserID string `json:"userid"`
	Action int    `json:"type"`
}

// HandleCallback processes an ONLYOFFICE save callback.
func (s *Service) HandleCallback(ctx context.Context, sessionID uuid.UUID, req CallbackRequest) error {
	s.versionMu.Lock()
	defer s.versionMu.Unlock()
	sess, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("office: callback session not found")
	}
	if req.Key == "" || req.Key != sess.EngineDocKey {
		return fmt.Errorf("office: callback document key mismatch")
	}
	urlDigest := callbackURLDigest(req.URL)
	if sess.State != SessionStateActive {
		if req.Status == CallbackStatusNoChanges &&
			sess.State == SessionStateClosed {
			return nil
		}
		if (req.Status == CallbackStatusReady ||
			req.Status == CallbackStatusForceSave) && urlDigest != "" {
			existing, lookupErr := s.store.GetCallbackByDigest(
				ctx, sess.ID, urlDigest,
			)
			if lookupErr == nil && existing.ID != "" {
				return s.finalizeCallbackSession(
					ctx, sess, req.Status, existing.VersionID,
				)
			}
		}
		return fmt.Errorf("office: callback for inactive session")
	}

	// Record callback receipt
	if err := s.store.UpdateSessionCallback(ctx, sessionID); err != nil {
		return fmt.Errorf("office: update session callback: %w", err)
	}

	switch req.Status {
	case CallbackStatusEditing:
		// Connection/disconnection, no action needed
		return nil

	case CallbackStatusReady, CallbackStatusForceSave:
		// Download and commit a new version
		return s.handleSaveCallback(ctx, sess, req, urlDigest)

	case CallbackStatusNoChanges:
		return s.store.UpdateSessionState(ctx, sessionID, SessionStateClosed)

	case CallbackStatusSaveError:
		return s.store.UpdateSessionState(ctx, sessionID, SessionStateError)

	case CallbackStatusForceSaveFail:
		return nil

	default:
		return fmt.Errorf("office: unknown callback status %d", req.Status)
	}
}

func callbackURLDigest(value string) string {
	if value == "" {
		return ""
	}
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}

func (s *Service) handleSaveCallback(ctx context.Context, sess EditorSession, req CallbackRequest, urlDigest string) error {
	// Check idempotency
	existing, err := s.store.GetCallbackByDigest(ctx, sess.ID, urlDigest)
	if err == nil && existing.ID != "" {
		return s.finalizeCallbackSession(ctx, sess, req.Status, existing.VersionID)
	}

	cb := SaveCallback{
		ID:         uuid.New().String(),
		SessionID:  sess.ID,
		EngineKey:  req.Key,
		Status:     req.Status,
		URLDigest:  urlDigest,
		Attempt:    1,
		ReceivedAt: s.clock.Now().UTC(),
	}
	if err := s.store.CreateCallback(ctx, cb); err != nil {
		return fmt.Errorf("office: record callback: %w", err)
	}

	// Validate and download the file
	if req.URL == "" {
		return fmt.Errorf("office: callback missing save URL")
	}

	// Validate URL against allowed origins
	if err := s.validateCallbackURL(req.URL); err != nil {
		_ = s.store.CompleteCallback(ctx, cb.ID, "url_rejected", nil)
		return err
	}

	// Download the document
	content, err := s.downloadFromEngine(ctx, req.URL)
	if err != nil {
		_ = s.store.CompleteCallback(ctx, cb.ID, "download_failed", nil)
		return fmt.Errorf("office: download from engine: %w", err)
	}

	// Validate the downloaded file
	current, err := s.store.GetVersion(ctx, sess.DocumentID, sess.VersionID)
	if err != nil {
		_ = s.store.CompleteCallback(ctx, cb.ID, "version_missing", nil)
		return fmt.Errorf("office: callback version not found")
	}
	if req.FileType != "" &&
		!strings.EqualFold(strings.TrimPrefix(current.Extension, "."), req.FileType) {
		_ = s.store.CompleteCallback(ctx, cb.ID, "format_mismatch", nil)
		return fmt.Errorf("office: callback file type mismatch")
	}
	ext, _, err := ValidateUploadedFile("save"+current.Extension, content, s.maxFileBytes)
	if err != nil {
		_ = s.store.CompleteCallback(ctx, cb.ID, "validation_failed", nil)
		return fmt.Errorf("office: validate downloaded file: %w", err)
	}

	// Get document info (verify access)
	_, err = s.store.GetDocument(ctx, sess.ActorID, sess.DocumentID)
	if err != nil {
		_ = s.store.CompleteCallback(ctx, cb.ID, "error", nil)
		return err
	}

	// Create new immutable version
	versionCount, err := s.store.CountVersions(ctx, sess.DocumentID)
	if err != nil {
		_ = s.store.CompleteCallback(ctx, cb.ID, "error", nil)
		return err
	}
	if versionCount >= s.maxVersions {
		_ = s.store.CompleteCallback(ctx, cb.ID, "version_limit", nil)
		return fmt.Errorf("office: document version limit reached")
	}
	sequence, err := s.store.NextSequence(ctx, sess.DocumentID)
	if err != nil {
		_ = s.store.CompleteCallback(ctx, cb.ID, "error", nil)
		return err
	}

	hash := sha256.Sum256(content)
	origin := OriginEditorSave
	if req.Status == CallbackStatusForceSave {
		origin = OriginForceSave
	}

	ver := DocumentVersion{
		ID:           uuid.New(),
		DocumentID:   sess.DocumentID,
		ActorID:      sess.ActorID,
		Sequence:     sequence,
		Extension:    ext,
		MIMEType:     mimeTypeForExt(ext),
		SHA256:       hex.EncodeToString(hash[:]),
		SizeBytes:    int64(len(content)),
		Origin:       origin,
		EngineDocKey: req.Key,
		CreatedAt:    s.clock.Now().UTC(),
		CreatedBy:    sess.ActorID.String(),
	}

	// Encrypt and store atomically
	if err := s.storeVersionContent(ver, content); err != nil {
		_ = s.store.CompleteCallback(ctx, cb.ID, "storage_failed", nil)
		return err
	}

	// Commit the immutable version and current pointer together.
	if err := s.store.CommitVersion(ctx, sess.ActorID, ver); err != nil {
		_ = s.removeVersionContent(ver)
		_ = s.store.CompleteCallback(ctx, cb.ID, "error", nil)
		return err
	}

	// Mark callback completed
	_ = s.store.CompleteCallback(ctx, cb.ID, "committed", &ver.ID)

	return s.finalizeCallbackSession(ctx, sess, req.Status, &ver.ID)
}

func (s *Service) finalizeCallbackSession(
	ctx context.Context,
	session EditorSession,
	status CallbackStatus,
	versionID *uuid.UUID,
) error {
	if status == CallbackStatusReady {
		return s.store.UpdateSessionState(ctx, session.ID, SessionStateClosed)
	}
	if status == CallbackStatusForceSave && versionID != nil {
		return s.store.UpdateSessionVersion(ctx, session.ID, *versionID)
	}
	return nil
}

func (s *Service) validateCallbackURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.User != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("office: invalid callback URL")
	}
	if s.engine != nil {
		if eng, ok := s.engine.(*OnlyOfficeEngine); ok {
			allowed, parseErr := url.Parse(eng.internalURL)
			if parseErr == nil && sameOrigin(parsed, allowed) &&
				pathWithin(parsed.EscapedPath(), allowed.EscapedPath()) {
				return nil
			}
		}
	}
	public, err := url.Parse(s.callbackOrigin)
	if err == nil && sameOrigin(parsed, public) &&
		pathWithin(parsed.EscapedPath(), strings.TrimSuffix(s.publicPath, "/")) {
		return nil
	}
	return fmt.Errorf("office: callback URL not from allowed origin")
}

func (s *Service) downloadFromEngine(ctx context.Context, downloadURL string) ([]byte, error) {
	client := *s.httpClient
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > 5 {
			return fmt.Errorf("office: too many redirects")
		}
		if err := s.validateCallbackURL(req.URL.String()); err != nil {
			return err
		}
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, "GET", downloadURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("office: engine returned status %d", resp.StatusCode)
	}
	// Bound the read
	limited := io.LimitReader(resp.Body, s.maxFileBytes+1)
	content, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > s.maxFileBytes {
		return nil, ErrTooLarge
	}
	return content, nil
}

func sameOrigin(left, right *url.URL) bool {
	return strings.EqualFold(left.Scheme, right.Scheme) &&
		strings.EqualFold(left.Host, right.Host)
}

func pathWithin(candidate, prefix string) bool {
	candidate = path.Clean("/" + strings.TrimPrefix(candidate, "/"))
	prefix = path.Clean("/" + strings.TrimPrefix(prefix, "/"))
	return candidate == prefix || strings.HasPrefix(candidate, prefix+"/")
}
