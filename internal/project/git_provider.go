package project

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const providerDeliveryKind = "project_provider_delivery_v1"

const repositoryGrantVersion = "ion.repository-grant.v1"

type RepositoryGrantRequest struct {
	ProjectID  uuid.UUID `json:"project_id"`
	Provider   string    `json:"provider"`
	Repository string    `json:"repository"`
	Actions    []string  `json:"actions"`
	TTLSeconds int       `json:"ttl_seconds"`
}

type repositoryGrantClaims struct {
	Version    string    `json:"version"`
	ActorID    uuid.UUID `json:"actor_id"`
	ProjectID  uuid.UUID `json:"project_id"`
	Provider   string    `json:"provider"`
	Repository string    `json:"repository"`
	Actions    []string  `json:"actions"`
	ExpiresAt  time.Time `json:"expires_at"`
}

type ProviderContext struct {
	CredentialReference string `json:"credential_reference,omitempty"`
	PermissionGrant     string `json:"permission_grant,omitempty"`
}

type ProviderRepository struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	WebURL        string `json:"web_url"`
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
}

type ProviderIssue struct {
	ID        string    `json:"id"`
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	State     string    `json:"state"`
	WebURL    string    `json:"web_url"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ProviderChange struct {
	ID           string    `json:"id"`
	Number       int       `json:"number"`
	Title        string    `json:"title"`
	State        string    `json:"state"`
	Draft        bool      `json:"draft"`
	SourceBranch string    `json:"source_branch"`
	TargetBranch string    `json:"target_branch"`
	WebURL       string    `json:"web_url"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type ProviderReviewThread struct {
	ID       string `json:"id"`
	Path     string `json:"path,omitempty"`
	Line     int    `json:"line,omitempty"`
	Body     string `json:"body"`
	Resolved bool   `json:"resolved"`
	Outdated bool   `json:"outdated"`
}

type ProviderCheck struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion,omitempty"`
	WebURL     string `json:"web_url,omitempty"`
}

type ProviderMergeability struct {
	Mergeable bool     `json:"mergeable"`
	State     string   `json:"state"`
	Reasons   []string `json:"reasons,omitempty"`
}

type ProviderPage[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

type ProviderDraftInput struct {
	Repository   string `json:"repository"`
	SourceBranch string `json:"source_branch"`
	TargetBranch string `json:"target_branch"`
	Title        string `json:"title"`
	Body         string `json:"body"`
	Marker       string `json:"marker"`
}

// RepositoryProvider is provider-neutral and receives only write-only secret
// references. Implementations resolve those references outside model context.
type RepositoryProvider interface {
	Name() string
	ListRepositories(context.Context, ProviderContext, string) (ProviderPage[ProviderRepository], error)
	ListIssues(context.Context, ProviderContext, string, string) (ProviderPage[ProviderIssue], error)
	ListChanges(context.Context, ProviderContext, string, string) (ProviderPage[ProviderChange], error)
	ListReviewThreads(context.Context, ProviderContext, string, string, string) (ProviderPage[ProviderReviewThread], error)
	ListChecks(context.Context, ProviderContext, string, string, string) (ProviderPage[ProviderCheck], error)
	Mergeability(context.Context, ProviderContext, string, int) (ProviderMergeability, error)
	FindDraftByMarker(context.Context, ProviderContext, string, string) (*ProviderChange, error)
	CreateDraftChange(context.Context, ProviderContext, ProviderDraftInput) (ProviderChange, error)
}

type ProviderDraftRequest struct {
	ProjectID           uuid.UUID   `json:"project_id"`
	Provider            string      `json:"provider"`
	Repository          string      `json:"repository"`
	SourceBranch        string      `json:"source_branch"`
	TargetBranch        string      `json:"target_branch"`
	Title               string      `json:"title"`
	Body                string      `json:"body"`
	ExpectedHead        string      `json:"expected_head"`
	IdempotencyKey      string      `json:"idempotency_key"`
	CredentialReference string      `json:"credential_reference,omitempty"`
	SecretGrant         SecretGrant `json:"secret_grant,omitempty"`
	PermissionGrant     string      `json:"permission_grant"`
}

type ProviderQueryRequest struct {
	ProjectID           uuid.UUID   `json:"project_id"`
	Provider            string      `json:"provider"`
	Repository          string      `json:"repository,omitempty"`
	Change              int         `json:"change,omitempty"`
	Ref                 string      `json:"ref,omitempty"`
	CredentialReference string      `json:"credential_reference,omitempty"`
	SecretGrant         SecretGrant `json:"secret_grant,omitempty"`
	PermissionGrant     string      `json:"permission_grant"`
}

type ProviderProjection struct {
	Repositories []ProviderRepository   `json:"repositories,omitempty"`
	Issues       []ProviderIssue        `json:"issues,omitempty"`
	Changes      []ProviderChange       `json:"changes,omitempty"`
	Review       []ProviderReviewThread `json:"review,omitempty"`
	Checks       []ProviderCheck        `json:"checks,omitempty"`
	Mergeability *ProviderMergeability  `json:"mergeability,omitempty"`
}

func (service *Service) IssueRepositoryGrant(ctx context.Context, actor uuid.UUID,
	request RepositoryGrantRequest, authorized bool) (string, error) {
	provider, repository := strings.ToLower(strings.TrimSpace(request.Provider)), strings.TrimSpace(request.Repository)
	if actor == uuid.Nil || request.ProjectID == uuid.Nil || provider == "" || repository == "" ||
		request.TTLSeconds <= 0 || request.TTLSeconds > 900 || len(request.Actions) == 0 || len(request.Actions) > 8 {
		return "", fmt.Errorf("project: bounded repository grant scope is required")
	}
	if _, err := service.Get(ctx, actor, request.ProjectID); err != nil {
		return "", err
	}
	actions := make([]string, 0, len(request.Actions))
	seen := map[string]struct{}{}
	for _, action := range request.Actions {
		action = strings.ToLower(strings.TrimSpace(action))
		if action != "read" && action != "draft.create" && action != "push" && action != "force-push" {
			return "", fmt.Errorf("project: unsupported repository grant action")
		}
		if action != "read" && !authorized {
			return "", fmt.Errorf("project: repository write grant requires explicit approval")
		}
		if _, duplicate := seen[action]; duplicate {
			continue
		}
		seen[action], actions = struct{}{}, append(actions, action)
	}
	claims := repositoryGrantClaims{Version: repositoryGrantVersion, ActorID: actor,
		ProjectID: request.ProjectID, Provider: provider, Repository: repository, Actions: actions,
		ExpiresAt: service.clock.Now().UTC().Add(time.Duration(request.TTLSeconds) * time.Second)}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	service.mu.Lock()
	key, err := service.grantKey(ctx, actor)
	service.mu.Unlock()
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(payload)
	for index := range key {
		key[index] = 0
	}
	return base64.RawURLEncoding.EncodeToString(payload) + "." + hex.EncodeToString(mac.Sum(nil)), nil
}

func (service *Service) verifyRepositoryGrant(ctx context.Context, actor, projectID uuid.UUID,
	provider, repository, action, token string) error {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return fmt.Errorf("project: repository permission grant is invalid")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return fmt.Errorf("project: repository permission grant is invalid")
	}
	var claims repositoryGrantClaims
	if json.Unmarshal(payload, &claims) != nil || claims.Version != repositoryGrantVersion ||
		claims.ActorID != actor || claims.ProjectID != projectID || !claims.ExpiresAt.After(service.clock.Now()) ||
		claims.Provider != strings.ToLower(strings.TrimSpace(provider)) ||
		(claims.Repository != "*" && claims.Repository != strings.TrimSpace(repository)) {
		return fmt.Errorf("project: repository permission grant scope is invalid or expired")
	}
	allowed := false
	for _, candidate := range claims.Actions {
		allowed = allowed || candidate == action
	}
	if !allowed {
		return fmt.Errorf("project: repository permission grant does not allow %s", action)
	}
	service.mu.Lock()
	key, err := service.grantKey(ctx, actor)
	service.mu.Unlock()
	if err != nil {
		return err
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(payload)
	expected := mac.Sum(nil)
	for index := range key {
		key[index] = 0
	}
	provided, err := hex.DecodeString(parts[1])
	if err != nil || !hmac.Equal(expected, provided) {
		return fmt.Errorf("project: repository permission grant signature is invalid")
	}
	return nil
}

func (service *Service) verifyProviderCredential(ctx context.Context, actor uuid.UUID,
	reference string, grant SecretGrant) error {
	if strings.TrimSpace(reference) == "" {
		return nil
	}
	if grant.Reference != strings.TrimSpace(reference) {
		return fmt.Errorf("project: matching write-only credential grant is required")
	}
	return service.verifyGrant(ctx, actor, grant)
}

type providerDelivery struct {
	RequestHash string         `json:"request_hash"`
	Result      ProviderChange `json:"result"`
	DeliveredAt time.Time      `json:"delivered_at"`
}

func (service *Service) ListAllProviderRepositories(ctx context.Context, actor uuid.UUID, projectID uuid.UUID,
	providerName string, providerContext ProviderContext) ([]ProviderRepository, error) {
	if _, err := service.Get(ctx, actor, projectID); err != nil {
		return nil, err
	}
	provider, err := service.repositoryProvider(providerName)
	if err != nil {
		return nil, err
	}
	result := []ProviderRepository{}
	seen := map[string]struct{}{}
	cursor := ""
	for page := 0; page < 20; page++ {
		found, err := provider.ListRepositories(ctx, providerContext, cursor)
		if err != nil {
			return nil, err
		}
		result = append(result, found.Items...)
		if len(result) > 2000 {
			return nil, fmt.Errorf("project: provider pagination bound exceeded")
		}
		if found.NextCursor == "" {
			return result, nil
		}
		if _, duplicate := seen[found.NextCursor]; duplicate {
			return nil, fmt.Errorf("project: provider returned a cursor loop")
		}
		seen[found.NextCursor] = struct{}{}
		cursor = found.NextCursor
	}
	return nil, fmt.Errorf("project: provider pagination bound exceeded")
}

func (service *Service) ProviderQuery(ctx context.Context, actor uuid.UUID, request ProviderQueryRequest,
	kind string) (ProviderProjection, error) {
	if _, err := service.Get(ctx, actor, request.ProjectID); err != nil {
		return ProviderProjection{}, err
	}
	if err := service.verifyRepositoryGrant(ctx, actor, request.ProjectID, request.Provider,
		request.Repository, "read", request.PermissionGrant); err != nil {
		return ProviderProjection{}, err
	}
	if err := service.verifyProviderCredential(ctx, actor, request.CredentialReference, request.SecretGrant); err != nil {
		return ProviderProjection{}, err
	}
	provider, err := service.repositoryProvider(request.Provider)
	if err != nil {
		return ProviderProjection{}, err
	}
	auth := ProviderContext{CredentialReference: request.CredentialReference, PermissionGrant: request.PermissionGrant}
	result := ProviderProjection{}
	switch kind {
	case "repositories":
		items, err := collectProviderPages(func(cursor string) (ProviderPage[ProviderRepository], error) {
			return provider.ListRepositories(ctx, auth, cursor)
		})
		result.Repositories = items
		return result, err
	case "issues":
		items, err := collectProviderPages(func(cursor string) (ProviderPage[ProviderIssue], error) {
			return provider.ListIssues(ctx, auth, request.Repository, cursor)
		})
		result.Issues = items
		return result, err
	case "changes":
		items, err := collectProviderPages(func(cursor string) (ProviderPage[ProviderChange], error) {
			return provider.ListChanges(ctx, auth, request.Repository, cursor)
		})
		result.Changes = items
		return result, err
	case "review":
		items, err := collectProviderPages(func(cursor string) (ProviderPage[ProviderReviewThread], error) {
			return provider.ListReviewThreads(ctx, auth, request.Repository, strconv.Itoa(request.Change), cursor)
		})
		result.Review = items
		return result, err
	case "checks":
		items, err := collectProviderPages(func(cursor string) (ProviderPage[ProviderCheck], error) {
			return provider.ListChecks(ctx, auth, request.Repository, request.Ref, cursor)
		})
		result.Checks = items
		return result, err
	case "mergeability":
		value, err := provider.Mergeability(ctx, auth, request.Repository, request.Change)
		result.Mergeability = &value
		return result, err
	default:
		return ProviderProjection{}, fmt.Errorf("project: unsupported provider projection")
	}
}

func collectProviderPages[T any](fetch func(string) (ProviderPage[T], error)) ([]T, error) {
	result, seen, cursor := []T{}, map[string]struct{}{}, ""
	for page := 0; page < 20; page++ {
		found, err := fetch(cursor)
		if err != nil {
			return nil, err
		}
		result = append(result, found.Items...)
		if len(result) > 2000 {
			return nil, fmt.Errorf("project: provider pagination bound exceeded")
		}
		if found.NextCursor == "" {
			return result, nil
		}
		if _, duplicate := seen[found.NextCursor]; duplicate {
			return nil, fmt.Errorf("project: provider returned a cursor loop")
		}
		seen[found.NextCursor], cursor = struct{}{}, found.NextCursor
	}
	return nil, fmt.Errorf("project: provider pagination bound exceeded")
}

func (service *Service) CreateProviderDraft(ctx context.Context, actor uuid.UUID,
	request ProviderDraftRequest, authorized bool) (ProviderChange, error) {
	if !authorized || actor == uuid.Nil || request.ProjectID == uuid.Nil || strings.TrimSpace(request.PermissionGrant) == "" ||
		strings.TrimSpace(request.IdempotencyKey) == "" || len(request.IdempotencyKey) > 256 ||
		strings.TrimSpace(request.Repository) == "" || strings.TrimSpace(request.SourceBranch) == "" ||
		strings.TrimSpace(request.TargetBranch) == "" || strings.TrimSpace(request.Title) == "" {
		return ProviderChange{}, fmt.Errorf("project: exact approved draft-change request is required")
	}
	_, root, err := service.gitProject(ctx, actor, request.ProjectID)
	if err != nil {
		return ProviderChange{}, err
	}
	head, _, err := runGitBounded(ctx, root, "rev-parse", "--verify", "HEAD")
	if err != nil || strings.TrimSpace(string(head)) != strings.TrimSpace(request.ExpectedHead) {
		return ProviderChange{}, errors.Join(ErrConflict, err)
	}
	provider, err := service.repositoryProvider(request.Provider)
	if err != nil {
		return ProviderChange{}, err
	}
	if err := service.verifyRepositoryGrant(ctx, actor, request.ProjectID, request.Provider,
		request.Repository, "draft.create", request.PermissionGrant); err != nil {
		return ProviderChange{}, err
	}
	if err := service.verifyProviderCredential(ctx, actor, request.CredentialReference, request.SecretGrant); err != nil {
		return ProviderChange{}, err
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return ProviderChange{}, err
	}
	digest := sha256.Sum256(encoded)
	requestHash := hex.EncodeToString(digest[:])
	scope := actor.String() + ":" + strings.ToLower(strings.TrimSpace(request.Provider)) + ":" + request.IdempotencyKey
	if raw, loadErr := service.store.LoadLivingState(ctx, providerDeliveryKind, scope); loadErr == nil {
		var delivery providerDelivery
		if json.Unmarshal(raw, &delivery) != nil || delivery.RequestHash != requestHash {
			return ProviderChange{}, ErrConflict
		}
		return delivery.Result, nil
	} else if !errors.Is(loadErr, sql.ErrNoRows) {
		return ProviderChange{}, loadErr
	}
	markerDigest := sha256.Sum256([]byte(actor.String() + ":" + request.Provider + ":" + request.IdempotencyKey))
	marker := "ion-delivery:" + hex.EncodeToString(markerDigest[:16])
	providerContext := ProviderContext{CredentialReference: request.CredentialReference, PermissionGrant: request.PermissionGrant}
	if existing, findErr := provider.FindDraftByMarker(ctx, providerContext, request.Repository, marker); findErr != nil {
		return ProviderChange{}, findErr
	} else if existing != nil {
		return *existing, service.saveProviderDelivery(ctx, scope, requestHash, *existing)
	}
	result, err := provider.CreateDraftChange(ctx, providerContext, ProviderDraftInput{Repository: request.Repository,
		SourceBranch: request.SourceBranch, TargetBranch: request.TargetBranch, Title: request.Title,
		Body: request.Body + "\n\n<!-- " + marker + " -->", Marker: marker})
	if err != nil {
		// The effect may have succeeded despite a lost response. Leave no false
		// success; the exact retry reconciles by marker before creating again.
		return ProviderChange{}, err
	}
	return result, service.saveProviderDelivery(ctx, scope, requestHash, result)
}

func (service *Service) saveProviderDelivery(ctx context.Context, scope, requestHash string,
	result ProviderChange) error {
	raw, err := json.Marshal(providerDelivery{RequestHash: requestHash, Result: result, DeliveredAt: service.clock.Now().UTC()})
	if err != nil {
		return err
	}
	return service.store.SaveLivingState(ctx, providerDeliveryKind, scope, raw)
}

func (service *Service) repositoryProvider(name string) (RepositoryProvider, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	provider := service.repositoryProviders[name]
	if provider == nil {
		return nil, fmt.Errorf("project: repository provider %q is unavailable", name)
	}
	return provider, nil
}
