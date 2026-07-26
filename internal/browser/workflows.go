package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	"github.com/paxlabs-inc/ion-agent/internal/security/vault"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

const workflowStateVersion = 1

type WorkflowStatus string

const (
	WorkflowActive          WorkflowStatus = "active"
	WorkflowPaused          WorkflowStatus = "paused"
	WorkflowWaitingForHuman WorkflowStatus = "waiting_for_human"
	WorkflowCancelled       WorkflowStatus = "cancelled"
	WorkflowRestartRequired WorkflowStatus = "restart_required"
)

type HandoffKind string

const (
	HandoffCAPTCHA          HandoffKind = "captcha"
	HandoffPasskey          HandoffKind = "passkey"
	HandoffLegalIdentity    HandoffKind = "legal_identity"
	HandoffTerms            HandoffKind = "terms"
	HandoffPayment          HandoffKind = "payment"
	HandoffRecovery         HandoffKind = "recovery"
	HandoffAmbiguousControl HandoffKind = "ambiguous_control"
)

type Handoff struct {
	Kind        HandoffKind `json:"kind"`
	Consequence string      `json:"consequence"`
	RequestedAt time.Time   `json:"requested_at"`
}

type Workflow struct {
	ID        uuid.UUID      `json:"id"`
	ActorID   uuid.UUID      `json:"actor_id"`
	SessionID *uuid.UUID     `json:"session_id,omitempty"`
	Status    WorkflowStatus `json:"status"`
	Origin    string         `json:"origin"`
	Revision  uint64         `json:"revision"`
	Preview   Snapshot       `json:"preview"`
	Handoff   *Handoff       `json:"handoff,omitempty"`
	Reason    string         `json:"reason,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

type CredentialReference struct {
	ID        uuid.UUID `json:"id"`
	ActorID   uuid.UUID `json:"actor_id"`
	Origin    string    `json:"origin"`
	Label     string    `json:"label"`
	CreatedAt time.Time `json:"created_at"`
}

type storedCredential struct {
	CredentialReference
	Secret string `json:"secret"`
}

type workflowState struct {
	Version     int                         `json:"version"`
	Workflows   map[string]Workflow         `json:"workflows"`
	Credentials map[string]storedCredential `json:"credentials"`
}

type Supervisor struct {
	browser *Service
	path    string
	vault   *vault.Vault
	clock   types.Clock

	mu    sync.Mutex
	state workflowState
}

func OpenSupervisor(
	browser *Service,
	path string,
	cipher *vault.Vault,
	clock types.Clock,
) (*Supervisor, error) {
	if browser == nil || cipher == nil || clock == nil {
		return nil, errors.New("browser workflow: browser, vault, and clock are required")
	}
	absolute, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil || absolute == "." {
		return nil, errors.New("browser workflow: encrypted state path is required")
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
		return nil, err
	}
	supervisor := &Supervisor{
		browser: browser, path: absolute, vault: cipher, clock: clock,
		state: workflowState{
			Version: workflowStateVersion, Workflows: make(map[string]Workflow),
			Credentials: make(map[string]storedCredential),
		},
	}
	if err := supervisor.load(); err != nil {
		return nil, err
	}
	supervisor.mu.Lock()
	if supervisor.reconcileRestartLocked() {
		if err := supervisor.saveLocked(); err != nil {
			supervisor.mu.Unlock()
			return nil, err
		}
	}
	supervisor.mu.Unlock()
	return supervisor, nil
}

func (supervisor *Supervisor) Start(
	ctx context.Context,
	rawURL string,
) (Workflow, error) {
	actorID, sessionID, err := workflowScope(ctx)
	if err != nil {
		return Workflow{}, err
	}
	origin, err := normalizedOrigin(rawURL)
	if err != nil {
		return Workflow{}, err
	}
	preview, err := supervisor.browser.Navigate(ctx, rawURL)
	if err != nil {
		return Workflow{}, err
	}
	now := supervisor.clock.Now().UTC()
	workflow := Workflow{
		ID: uuid.New(), ActorID: actorID, SessionID: cloneWorkflowUUID(sessionID),
		Status: WorkflowActive, Origin: origin, Revision: 1,
		Preview: preview, CreatedAt: now, UpdatedAt: now,
	}
	supervisor.mu.Lock()
	defer supervisor.mu.Unlock()
	supervisor.state.Workflows[workflow.ID.String()] = workflow
	if err := supervisor.saveLocked(); err != nil {
		delete(supervisor.state.Workflows, workflow.ID.String())
		return Workflow{}, err
	}
	return cloneWorkflow(workflow), nil
}

func (supervisor *Supervisor) Observe(
	ctx context.Context,
	id uuid.UUID,
) (Workflow, error) {
	workflow, err := supervisor.Get(ctx, id)
	if err != nil {
		return Workflow{}, err
	}
	if workflow.Status != WorkflowActive {
		return Workflow{}, fmt.Errorf("browser workflow: workflow is %s", workflow.Status)
	}
	preview, err := supervisor.browser.Observe(ctx)
	if err != nil {
		return Workflow{}, err
	}
	return supervisor.updatePreview(ctx, id, preview)
}

func (supervisor *Supervisor) Interact(
	ctx context.Context,
	id uuid.UUID,
	action string,
	ref string,
	value string,
) (Workflow, error) {
	workflow, err := supervisor.Get(ctx, id)
	if err != nil {
		return Workflow{}, err
	}
	if workflow.Status != WorkflowActive {
		return Workflow{}, fmt.Errorf("browser workflow: workflow is %s", workflow.Status)
	}
	preview, err := supervisor.browser.Interact(ctx, action, ref, value)
	if err != nil {
		return Workflow{}, err
	}
	return supervisor.updatePreview(ctx, id, preview)
}

func (supervisor *Supervisor) Submit(
	ctx context.Context,
	id uuid.UUID,
	ref string,
) (Workflow, error) {
	workflow, err := supervisor.Get(ctx, id)
	if err != nil {
		return Workflow{}, err
	}
	if workflow.Status != WorkflowActive {
		return Workflow{}, fmt.Errorf("browser workflow: workflow is %s", workflow.Status)
	}
	preview, err := supervisor.browser.Submit(ctx, ref)
	if err != nil {
		return Workflow{}, err
	}
	return supervisor.updatePreview(ctx, id, preview)
}

func (supervisor *Supervisor) Pause(
	ctx context.Context,
	id uuid.UUID,
) (Workflow, error) {
	return supervisor.transition(ctx, id, []WorkflowStatus{WorkflowActive},
		WorkflowPaused, "", nil)
}

func (supervisor *Supervisor) Resume(
	ctx context.Context,
	id uuid.UUID,
) (Workflow, error) {
	workflow, err := supervisor.Get(ctx, id)
	if err != nil {
		return Workflow{}, err
	}
	if workflow.Status == WorkflowRestartRequired {
		return Workflow{}, errors.New(
			"browser workflow: volatile browser state was cleared on restart; start a new workflow",
		)
	}
	if workflow.Status != WorkflowPaused && workflow.Status != WorkflowWaitingForHuman {
		return Workflow{}, fmt.Errorf("browser workflow: workflow is %s", workflow.Status)
	}
	preview, err := supervisor.browser.Observe(ctx)
	if err != nil {
		return Workflow{}, err
	}
	supervisor.mu.Lock()
	defer supervisor.mu.Unlock()
	current, err := supervisor.scopedWorkflowLocked(ctx, id)
	if err != nil {
		return Workflow{}, err
	}
	current.Status = WorkflowActive
	current.Handoff = nil
	current.Reason = ""
	current.Preview = preview
	current.Revision++
	current.UpdatedAt = supervisor.clock.Now().UTC()
	supervisor.state.Workflows[id.String()] = current
	if err := supervisor.saveLocked(); err != nil {
		return Workflow{}, err
	}
	return cloneWorkflow(current), nil
}

func (supervisor *Supervisor) Cancel(
	ctx context.Context,
	id uuid.UUID,
) (Workflow, error) {
	workflow, err := supervisor.transition(
		ctx, id,
		[]WorkflowStatus{
			WorkflowActive, WorkflowPaused, WorkflowWaitingForHuman,
			WorkflowRestartRequired,
		},
		WorkflowCancelled, "cancelled by the authenticated operator", nil,
	)
	if err != nil {
		return Workflow{}, err
	}
	if err := supervisor.browser.CloseScope(ctx); err != nil {
		return Workflow{}, err
	}
	return workflow, nil
}

func (supervisor *Supervisor) RequestHandoff(
	ctx context.Context,
	id uuid.UUID,
	kind HandoffKind,
	consequence string,
) (Workflow, error) {
	if err := validateHandoff(kind); err != nil {
		return Workflow{}, err
	}
	consequence = strings.TrimSpace(consequence)
	if consequence == "" || len(consequence) > 500 {
		return Workflow{}, errors.New("browser workflow: bounded handoff consequence is required")
	}
	handoff := &Handoff{
		Kind: kind, Consequence: consequence,
		RequestedAt: supervisor.clock.Now().UTC(),
	}
	return supervisor.transition(ctx, id, []WorkflowStatus{WorkflowActive},
		WorkflowWaitingForHuman, "human action is required", handoff)
}

func (supervisor *Supervisor) Get(
	ctx context.Context,
	id uuid.UUID,
) (Workflow, error) {
	supervisor.mu.Lock()
	defer supervisor.mu.Unlock()
	found, err := supervisor.scopedWorkflowLocked(ctx, id)
	return cloneWorkflow(found), err
}

func (supervisor *Supervisor) List(ctx context.Context) ([]Workflow, error) {
	actorID, _, err := workflowScope(ctx)
	if err != nil {
		return nil, err
	}
	supervisor.mu.Lock()
	defer supervisor.mu.Unlock()
	found := make([]Workflow, 0)
	for _, workflow := range supervisor.state.Workflows {
		if workflow.ActorID == actorID {
			found = append(found, cloneWorkflow(workflow))
		}
	}
	sort.Slice(found, func(left, right int) bool {
		return found[left].UpdatedAt.After(found[right].UpdatedAt)
	})
	return found, nil
}

func (supervisor *Supervisor) Latest(ctx context.Context) (Workflow, error) {
	workflows, err := supervisor.List(ctx)
	if err != nil {
		return Workflow{}, err
	}
	for _, workflow := range workflows {
		if workflow.Status != WorkflowCancelled {
			return workflow, nil
		}
	}
	return Workflow{}, errors.New("browser workflow: no workflow is available")
}

func (supervisor *Supervisor) PutCredential(
	ctx context.Context,
	origin string,
	label string,
	secret string,
) (CredentialReference, error) {
	actorID, _, err := workflowScope(ctx)
	if err != nil {
		return CredentialReference{}, err
	}
	origin, err = normalizedOrigin(origin)
	if err != nil {
		return CredentialReference{}, err
	}
	label = strings.TrimSpace(label)
	if label == "" || len(label) > 120 || secret == "" || len(secret) > 16<<10 {
		return CredentialReference{}, errors.New(
			"browser workflow: bounded credential label and secret are required",
		)
	}
	reference := CredentialReference{
		ID: uuid.New(), ActorID: actorID, Origin: origin, Label: label,
		CreatedAt: supervisor.clock.Now().UTC(),
	}
	supervisor.mu.Lock()
	defer supervisor.mu.Unlock()
	supervisor.state.Credentials[reference.ID.String()] = storedCredential{
		CredentialReference: reference, Secret: secret,
	}
	if err := supervisor.saveLocked(); err != nil {
		delete(supervisor.state.Credentials, reference.ID.String())
		return CredentialReference{}, err
	}
	return reference, nil
}

func (supervisor *Supervisor) Credentials(
	ctx context.Context,
) ([]CredentialReference, error) {
	actorID, _, err := workflowScope(ctx)
	if err != nil {
		return nil, err
	}
	supervisor.mu.Lock()
	defer supervisor.mu.Unlock()
	found := make([]CredentialReference, 0)
	for _, credential := range supervisor.state.Credentials {
		if credential.ActorID == actorID {
			found = append(found, credential.CredentialReference)
		}
	}
	sort.Slice(found, func(left, right int) bool {
		return found[left].CreatedAt.Before(found[right].CreatedAt)
	})
	return found, nil
}

func (supervisor *Supervisor) InsertCredential(
	ctx context.Context,
	workflowID uuid.UUID,
	credentialID uuid.UUID,
	ref string,
) (Workflow, error) {
	workflow, err := supervisor.Get(ctx, workflowID)
	if err != nil {
		return Workflow{}, err
	}
	if workflow.Status != WorkflowWaitingForHuman &&
		workflow.Status != WorkflowPaused {
		return Workflow{}, errors.New(
			"browser workflow: credential insertion requires a paused human handoff",
		)
	}
	actorID, _, err := workflowScope(ctx)
	if err != nil {
		return Workflow{}, err
	}
	supervisor.mu.Lock()
	credential, exists := supervisor.state.Credentials[credentialID.String()]
	supervisor.mu.Unlock()
	if !exists || credential.ActorID != actorID {
		return Workflow{}, errors.New("browser workflow: credential reference not found")
	}
	if credential.Origin != workflow.Origin {
		return Workflow{}, errors.New("browser workflow: credential origin mismatch")
	}
	preview, err := supervisor.browser.FillCredential(
		ctx, ref, credential.Secret, credential.Origin,
	)
	if err != nil {
		return Workflow{}, err
	}
	redactSnapshotSecret(&preview, credential.Secret)
	return supervisor.updatePreview(ctx, workflowID, preview)
}

func redactSnapshotSecret(snapshot *Snapshot, secret string) {
	if snapshot == nil || secret == "" {
		return
	}
	redact := func(value string) string {
		return strings.ReplaceAll(value, secret, "[REDACTED]")
	}
	snapshot.URL = redact(snapshot.URL)
	snapshot.Title = redact(snapshot.Title)
	snapshot.Text = redact(snapshot.Text)
	for index := range snapshot.Elements {
		snapshot.Elements[index].Text = redact(snapshot.Elements[index].Text)
		snapshot.Elements[index].Name = redact(snapshot.Elements[index].Name)
		snapshot.Elements[index].Placeholder = redact(snapshot.Elements[index].Placeholder)
	}
}

func (supervisor *Supervisor) updatePreview(
	ctx context.Context,
	id uuid.UUID,
	preview Snapshot,
) (Workflow, error) {
	supervisor.mu.Lock()
	defer supervisor.mu.Unlock()
	workflow, err := supervisor.scopedWorkflowLocked(ctx, id)
	if err != nil {
		return Workflow{}, err
	}
	workflow.Preview = preview
	workflow.Revision++
	workflow.UpdatedAt = supervisor.clock.Now().UTC()
	supervisor.state.Workflows[id.String()] = workflow
	if err := supervisor.saveLocked(); err != nil {
		return Workflow{}, err
	}
	return cloneWorkflow(workflow), nil
}

func (supervisor *Supervisor) transition(
	ctx context.Context,
	id uuid.UUID,
	from []WorkflowStatus,
	to WorkflowStatus,
	reason string,
	handoff *Handoff,
) (Workflow, error) {
	supervisor.mu.Lock()
	defer supervisor.mu.Unlock()
	workflow, err := supervisor.scopedWorkflowLocked(ctx, id)
	if err != nil {
		return Workflow{}, err
	}
	allowed := false
	for _, status := range from {
		allowed = allowed || workflow.Status == status
	}
	if !allowed {
		return Workflow{}, fmt.Errorf("browser workflow: cannot transition %s to %s",
			workflow.Status, to)
	}
	workflow.Status = to
	workflow.Reason = reason
	workflow.Handoff = cloneHandoff(handoff)
	workflow.Revision++
	workflow.UpdatedAt = supervisor.clock.Now().UTC()
	supervisor.state.Workflows[id.String()] = workflow
	if err := supervisor.saveLocked(); err != nil {
		return Workflow{}, err
	}
	return cloneWorkflow(workflow), nil
}

func (supervisor *Supervisor) scopedWorkflowLocked(
	ctx context.Context,
	id uuid.UUID,
) (Workflow, error) {
	actorID, sessionID, err := workflowScope(ctx)
	if err != nil {
		return Workflow{}, err
	}
	workflow, exists := supervisor.state.Workflows[id.String()]
	if !exists || workflow.ActorID != actorID ||
		!sameWorkflowUUID(workflow.SessionID, sessionID) {
		return Workflow{}, errors.New("browser workflow: workflow not found")
	}
	return workflow, nil
}

func (supervisor *Supervisor) reconcileRestartLocked() bool {
	changed := false
	now := supervisor.clock.Now().UTC()
	for key, workflow := range supervisor.state.Workflows {
		switch workflow.Status {
		case WorkflowActive, WorkflowPaused, WorkflowWaitingForHuman:
			workflow.Status = WorkflowRestartRequired
			workflow.Handoff = nil
			workflow.Reason = "volatile browser state was cleared during restart"
			workflow.Revision++
			workflow.UpdatedAt = now
			supervisor.state.Workflows[key] = workflow
			changed = true
		}
	}
	return changed
}

func (supervisor *Supervisor) load() error {
	encrypted, err := os.ReadFile(supervisor.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	plaintext, err := supervisor.vault.Decrypt(encrypted)
	if err != nil {
		return fmt.Errorf("browser workflow: decrypt state: %w", err)
	}
	defer clear(plaintext)
	var state workflowState
	if err := json.Unmarshal(plaintext, &state); err != nil {
		return fmt.Errorf("browser workflow: decode state: %w", err)
	}
	if state.Version != workflowStateVersion ||
		state.Workflows == nil || state.Credentials == nil {
		return errors.New("browser workflow: unsupported state")
	}
	supervisor.state = state
	return nil
}

func (supervisor *Supervisor) saveLocked() error {
	plaintext, err := json.Marshal(supervisor.state)
	if err != nil {
		return err
	}
	defer clear(plaintext)
	encrypted, err := supervisor.vault.Encrypt(plaintext)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(supervisor.path), ".browser-workflow-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(encrypted); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempName, supervisor.path)
}

func workflowScope(ctx context.Context) (uuid.UUID, *uuid.UUID, error) {
	scope, ok := controlplane.ApprovalScopeFromContext(ctx)
	if !ok || scope.ActorID == uuid.Nil {
		return uuid.Nil, nil, errors.New(
			"browser workflow: authenticated actor scope is required",
		)
	}
	return scope.ActorID, cloneWorkflowUUID(scope.SessionID), nil
}

func normalizedOrigin(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() == "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.User != nil {
		return "", errors.New("browser workflow: valid HTTP(S) origin is required")
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}

func validateHandoff(kind HandoffKind) error {
	switch kind {
	case HandoffCAPTCHA, HandoffPasskey, HandoffLegalIdentity, HandoffTerms,
		HandoffPayment, HandoffRecovery, HandoffAmbiguousControl:
		return nil
	default:
		return fmt.Errorf("browser workflow: unsupported handoff %q", kind)
	}
}

func cloneWorkflow(workflow Workflow) Workflow {
	workflow.SessionID = cloneWorkflowUUID(workflow.SessionID)
	workflow.Handoff = cloneHandoff(workflow.Handoff)
	workflow.Preview.Elements = append([]Element(nil), workflow.Preview.Elements...)
	return workflow
}

func cloneHandoff(handoff *Handoff) *Handoff {
	if handoff == nil {
		return nil
	}
	cloned := *handoff
	return &cloned
}

func cloneWorkflowUUID(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func sameWorkflowUUID(left, right *uuid.UUID) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
