package project

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/session"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
	_ "modernc.org/sqlite"
)

const (
	deliveryStateKind = "project_delivery_v1"
	maxDeliveryItems  = 128
)

var deliveryNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)
var environmentNamePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,126}$`)

type DeliveryService struct {
	mu                 sync.Mutex
	store              *session.Store
	clock              types.Clock
	projects           *Service
	root               string
	resourceAdapters   map[string]ResourceAdapter
	deploymentAdapters map[string]DeploymentAdapter
}

func newDeliveryService(store *session.Store, clock types.Clock, projects *Service, root string,
	resourceAdapters map[string]ResourceAdapter,
	deploymentAdapters map[string]DeploymentAdapter) (*DeliveryService, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("project: resolve delivery root: %w", err)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("project: create delivery root: %w", err)
	}
	resources := map[string]ResourceAdapter{}
	for name, adapter := range resourceAdapters {
		normalized := strings.ToLower(strings.TrimSpace(name))
		if normalized == "" || adapter == nil || adapter.Name() != normalized {
			return nil, fmt.Errorf("project: resource adapter registration is invalid")
		}
		resources[normalized] = adapter
	}
	if _, ok := resources["local"]; !ok {
		resources["local"] = &localResourceAdapter{root: filepath.Join(root, "resources")}
	}
	deployments := map[string]DeploymentAdapter{}
	for name, adapter := range deploymentAdapters {
		normalized := strings.ToLower(strings.TrimSpace(name))
		if normalized == "" || adapter == nil || adapter.Name() != normalized {
			return nil, fmt.Errorf("project: deployment adapter registration is invalid")
		}
		deployments[normalized] = adapter
	}
	if _, ok := deployments["local_staging"]; !ok {
		local, err := newLocalStagingAdapter(filepath.Join(root, "deployments"))
		if err != nil {
			return nil, err
		}
		deployments["local_staging"] = local
	}
	return &DeliveryService{store: store, clock: clock, projects: projects, root: root,
		resourceAdapters: resources, deploymentAdapters: deployments}, nil
}

func (service *DeliveryService) Close() error {
	service.mu.Lock()
	defer service.mu.Unlock()
	var result error
	for _, adapter := range service.deploymentAdapters {
		result = errors.Join(result, adapter.Close())
	}
	return result
}

func ResourceCatalog() []ResourceCatalogEntry {
	return []ResourceCatalogEntry{
		{Kind: ResourceDatabase, Capabilities: []string{"schema", "migration", "backup", "rollback"}, DataRisks: []string{"customer_data", "regulated_data"}},
		{Kind: ResourceStorage, Capabilities: []string{"objects", "retention", "export"}, DataRisks: []string{"customer_files", "public_access"}},
		{Kind: ResourceAuth, Capabilities: []string{"identity", "session", "export"}, DataRisks: []string{"credentials", "identity"}},
		{Kind: ResourceEmail, Capabilities: []string{"outbox", "delivery_status"}, DataRisks: []string{"personal_data", "external_effect"}},
		{Kind: ResourceQueue, Capabilities: []string{"enqueue", "lease", "retry"}, DataRisks: []string{"payload_data", "duplicate_effect"}},
		{Kind: ResourceSchedule, Capabilities: []string{"timezone", "pause", "run_history"}, DataRisks: []string{"external_effect", "replay"}},
		{Kind: ResourceAnalytics, Capabilities: []string{"events", "retention", "export"}, DataRisks: []string{"behavioral_data", "reidentification"}},
		{Kind: ResourcePayment, Capabilities: []string{"checkout", "refund", "webhook"}, DataRisks: []string{"financial", "external_effect"}},
		{Kind: ResourceExternalAPI, Capabilities: []string{"endpoint", "rate_limit", "health"}, DataRisks: []string{"external_effect", "data_egress"}},
	}
}

func (service *DeliveryService) Snapshot(ctx context.Context, actor, projectID uuid.UUID) (DeliverySnapshot, error) {
	if _, err := service.projects.Get(ctx, actor, projectID); err != nil {
		return DeliverySnapshot{}, err
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	state, err := service.load(ctx, actor, projectID)
	if err != nil {
		return DeliverySnapshot{}, err
	}
	return DeliverySnapshot{Version: DeliveryContractVersion, Catalog: ResourceCatalog(),
		Resources:    append([]ResourceReceipt{}, state.Resources...),
		Environments: append([]EnvironmentSchema{}, state.Environments...),
		Migrations:   append([]MigrationReceipt{}, state.Migrations...),
		Deployments:  append([]DeploymentReceipt{}, state.Deployments...),
		Release:      state.Release}, nil
}

func (service *DeliveryService) PlanResource(ctx context.Context, actor uuid.UUID,
	input ResourcePlanInput) (ResourcePlan, error) {
	if actor == uuid.Nil || input.ProjectID == uuid.Nil || input.WorkspaceRevision == 0 {
		return ResourcePlan{}, fmt.Errorf("project: complete resource plan input is required")
	}
	project, err := service.projects.Get(ctx, actor, input.ProjectID)
	if err != nil {
		return ResourcePlan{}, err
	}
	if project.WorkspaceRevision != input.WorkspaceRevision {
		return ResourcePlan{}, ErrStaleRevision
	}
	desired := input.Desired
	desired.ProjectID = project.ID
	if desired.ID == uuid.Nil {
		desired.ID = uuid.New()
	}
	if err := validateResourceDesired(desired); err != nil {
		return ResourcePlan{}, err
	}
	adapter := service.resourceAdapters[desired.Provider]
	if adapter == nil || !containsResourceKind(adapter.Capabilities(ctx), desired.Kind) {
		return ResourcePlan{}, fmt.Errorf("%w: provider %q does not support %s", ErrUnsupported, desired.Provider, desired.Kind)
	}
	actions, estimate, err := adapter.Plan(ctx, desired)
	if err != nil {
		return ResourcePlan{}, err
	}
	if estimate < 0 || estimate > desired.MonthlyCostLimitCents {
		return ResourcePlan{}, fmt.Errorf("project: resource cost exceeds the declared bound")
	}
	classification := PolicyYellow
	if desired.Environment == EnvironmentProduction || desired.Kind == ResourcePayment {
		classification = PolicyRed
	}
	plan := ResourcePlan{Version: DeliveryContractVersion, ID: uuid.New(), ActorID: actor,
		ProjectID: project.ID, WorkspaceRevision: project.WorkspaceRevision, Desired: desired,
		Actions: trimDeliveryStrings(actions), Classification: classification,
		EstimatedCostCents: estimate, CreatedAt: service.clock.Now().UTC()}
	plan.PlanSHA256 = requestHash("resource.plan", planHashInput(plan))
	service.mu.Lock()
	defer service.mu.Unlock()
	state, err := service.load(ctx, actor, project.ID)
	if err != nil {
		return ResourcePlan{}, err
	}
	if len(state.ResourcePlans) >= maxDeliveryItems {
		return ResourcePlan{}, fmt.Errorf("project: resource plan limit reached")
	}
	state.ResourcePlans = append(state.ResourcePlans, plan)
	if err := service.save(ctx, actor, project.ID, &state); err != nil {
		return ResourcePlan{}, err
	}
	return plan, nil
}

func (service *DeliveryService) ApplyResource(ctx context.Context, actor uuid.UUID,
	input ResourceApplyInput, approved bool) (ResourceReceipt, error) {
	if actor == uuid.Nil || input.ProjectID == uuid.Nil || input.PlanID == uuid.Nil ||
		strings.TrimSpace(input.IdempotencyKey) == "" || len(input.IdempotencyKey) > 256 {
		return ResourceReceipt{}, fmt.Errorf("project: complete resource apply input is required")
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	state, err := service.load(ctx, actor, input.ProjectID)
	if err != nil {
		return ResourceReceipt{}, err
	}
	plan, ok := findResourcePlan(state.ResourcePlans, input.PlanID)
	if !ok {
		return ResourceReceipt{}, ErrNotFound
	}
	if plan.Classification == PolicyRed && !approved {
		return ResourceReceipt{}, fmt.Errorf("%w: explicit RED approval is required", ErrUnsupported)
	}
	requestSHA := requestHash("resource.apply", struct {
		PlanID uuid.UUID
		Refs   []string
	}{PlanID: plan.ID, Refs: plan.Desired.SecretReferences})
	for _, receipt := range state.Resources {
		if receipt.IdempotencyKey != input.IdempotencyKey {
			continue
		}
		if receipt.RequestSHA256 != requestSHA {
			return ResourceReceipt{}, ErrConflict
		}
		return receipt, nil
	}
	if err := service.verifyReferences(ctx, actor, plan.Desired.SecretReferences, input.SecretGrants); err != nil {
		return ResourceReceipt{}, err
	}
	adapter := service.resourceAdapters[plan.Desired.Provider]
	if adapter == nil {
		return ResourceReceipt{}, ErrUnsupported
	}
	receipt, err := adapter.Apply(ctx, plan)
	if err != nil {
		return ResourceReceipt{}, err
	}
	now := service.clock.Now().UTC()
	receipt.Version, receipt.ID, receipt.PlanID = DeliveryContractVersion, uuid.New(), plan.ID
	receipt.ActorID, receipt.ProjectID, receipt.ResourceID = actor, plan.ProjectID, plan.Desired.ID
	receipt.Provider, receipt.Environment = plan.Desired.Provider, plan.Desired.Environment
	receipt.Classification, receipt.IdempotencyKey = plan.Classification, input.IdempotencyKey
	receipt.RequestSHA256, receipt.CreatedAt, receipt.ReconciledAt = requestSHA, now, now
	if receipt.State != "ready" || receipt.ActualCostCents > plan.Desired.MonthlyCostLimitCents {
		return ResourceReceipt{}, fmt.Errorf("project: resource adapter did not return bounded ready evidence")
	}
	state.Resources = appendBoundedResourceReceipts(state.Resources, receipt)
	if err := service.save(ctx, actor, input.ProjectID, &state); err != nil {
		return ResourceReceipt{}, err
	}
	return receipt, nil
}

func (service *DeliveryService) PutEnvironment(ctx context.Context, actor uuid.UUID,
	input EnvironmentSchemaInput) (EnvironmentSchema, error) {
	if actor == uuid.Nil || input.ProjectID == uuid.Nil || !validEnvironment(input.Environment) ||
		len(input.Variables) > 256 {
		return EnvironmentSchema{}, fmt.Errorf("project: valid bounded environment schema is required")
	}
	if _, err := service.projects.Get(ctx, actor, input.ProjectID); err != nil {
		return EnvironmentSchema{}, err
	}
	variables, err := normalizeEnvironmentVariables(input.Variables)
	if err != nil {
		return EnvironmentSchema{}, err
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	state, err := service.load(ctx, actor, input.ProjectID)
	if err != nil {
		return EnvironmentSchema{}, err
	}
	var revision uint64 = 1
	for _, schema := range state.Environments {
		if schema.Environment == input.Environment && schema.Revision >= revision {
			revision = schema.Revision + 1
		}
	}
	schema := EnvironmentSchema{Version: DeliveryContractVersion, Revision: revision,
		ActorID: actor, ProjectID: input.ProjectID, Environment: input.Environment,
		Variables: variables, CreatedAt: service.clock.Now().UTC()}
	schema.SchemaSHA256 = requestHash("environment.schema", struct {
		Environment EnvironmentScope
		Variables   []EnvironmentVariable
	}{schema.Environment, schema.Variables})
	state.Environments = append(state.Environments, schema)
	if len(state.Environments) > maxDeliveryItems {
		state.Environments = append([]EnvironmentSchema(nil), state.Environments[len(state.Environments)-maxDeliveryItems:]...)
	}
	if err := service.save(ctx, actor, input.ProjectID, &state); err != nil {
		return EnvironmentSchema{}, err
	}
	return schema, nil
}

func (service *DeliveryService) verifyReferences(ctx context.Context, actor uuid.UUID,
	references []string, grants []SecretGrant) error {
	if len(references) == 0 {
		return nil
	}
	byReference := map[string]SecretGrant{}
	for _, grant := range grants {
		if err := service.projects.verifyGrant(ctx, actor, grant); err != nil {
			return err
		}
		byReference[grant.Reference] = grant
	}
	for _, reference := range references {
		if _, ok := byReference[reference]; !ok {
			return fmt.Errorf("project: write-only grant for %q is required", reference)
		}
	}
	return nil
}

func (service *DeliveryService) load(ctx context.Context, actor, projectID uuid.UUID) (deliveryState, error) {
	if actor == uuid.Nil || projectID == uuid.Nil {
		return deliveryState{}, fmt.Errorf("project: actor and project are required")
	}
	raw, err := service.store.LoadLivingState(ctx, deliveryStateKind, actor.String()+":"+projectID.String())
	if errors.Is(err, sql.ErrNoRows) {
		return deliveryState{ResourceRequests: map[string]json.RawMessage{},
			DeploymentRequests: map[string]json.RawMessage{}}, nil
	}
	if err != nil {
		return deliveryState{}, err
	}
	var state deliveryState
	if err := json.Unmarshal(raw, &state); err != nil {
		return deliveryState{}, fmt.Errorf("project: decode delivery state: %w", err)
	}
	if state.ResourceRequests == nil {
		state.ResourceRequests = map[string]json.RawMessage{}
	}
	if state.DeploymentRequests == nil {
		state.DeploymentRequests = map[string]json.RawMessage{}
	}
	return state, nil
}

func (service *DeliveryService) save(ctx context.Context, actor, projectID uuid.UUID,
	state *deliveryState) error {
	state.Revision++
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return service.store.SaveLivingState(ctx, deliveryStateKind, actor.String()+":"+projectID.String(), raw)
}

type localResourceAdapter struct {
	root string
}

func (adapter *localResourceAdapter) Name() string {
	return "local"
}

func (adapter *localResourceAdapter) Capabilities(context.Context) []ResourceKind {
	return []ResourceKind{ResourceDatabase, ResourceStorage, ResourceAuth, ResourceEmail,
		ResourceQueue, ResourceSchedule, ResourceAnalytics, ResourceExternalAPI}
}

func (adapter *localResourceAdapter) Plan(_ context.Context, desired ResourceDesiredState) ([]string, int64, error) {
	actions := []string{"create actor-scoped resource root", "write redaction-safe desired state"}
	switch desired.Kind {
	case ResourceDatabase, ResourceAuth, ResourceEmail, ResourceQueue, ResourceSchedule, ResourceAnalytics:
		actions = append(actions, "initialize durable SQLite state", "verify database integrity")
	case ResourceStorage:
		actions = append(actions, "initialize confined object directory")
	case ResourceExternalAPI:
		actions = append(actions, "record endpoint contract without credentials")
	default:
		return nil, 0, ErrUnsupported
	}
	return actions, 0, nil
}

func (adapter *localResourceAdapter) Apply(ctx context.Context, plan ResourcePlan) (ResourceReceipt, error) {
	root := filepath.Join(adapter.root, plan.ActorID.String(), plan.ProjectID.String(), plan.Desired.ID.String())
	if err := os.MkdirAll(root, 0o700); err != nil {
		return ResourceReceipt{}, err
	}
	desiredRaw, err := json.Marshal(plan.Desired)
	if err != nil {
		return ResourceReceipt{}, err
	}
	if err := os.WriteFile(filepath.Join(root, "desired.json"), desiredRaw, 0o600); err != nil {
		return ResourceReceipt{}, err
	}
	endpoint := root
	switch plan.Desired.Kind {
	case ResourceDatabase, ResourceAuth, ResourceEmail, ResourceQueue, ResourceSchedule, ResourceAnalytics:
		databasePath := filepath.Join(root, "resource.sqlite")
		database, err := sql.Open("sqlite", databasePath)
		if err != nil {
			return ResourceReceipt{}, err
		}
		_, err = database.ExecContext(ctx, `PRAGMA journal_mode=WAL;
			CREATE TABLE IF NOT EXISTS ion_resource_metadata (
				key TEXT PRIMARY KEY,
				value TEXT NOT NULL
			);
			INSERT OR REPLACE INTO ion_resource_metadata(key, value) VALUES
				('kind', ?), ('environment', ?), ('plan_sha256', ?);`,
			plan.Desired.Kind, plan.Desired.Environment, plan.PlanSHA256)
		if err == nil {
			err = database.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(new(string))
		}
		closeErr := database.Close()
		if err != nil {
			return ResourceReceipt{}, err
		}
		if closeErr != nil {
			return ResourceReceipt{}, closeErr
		}
		endpoint = databasePath
	case ResourceStorage:
		endpoint = filepath.Join(root, "objects")
		if err := os.MkdirAll(endpoint, 0o700); err != nil {
			return ResourceReceipt{}, err
		}
	case ResourceExternalAPI:
		endpoint = "contract://" + plan.Desired.Name
	default:
		return ResourceReceipt{}, ErrUnsupported
	}
	digest, err := directorySHA256(root)
	if err != nil {
		return ResourceReceipt{}, err
	}
	return ResourceReceipt{State: "ready", ExternalID: "local:" + plan.Desired.ID.String(),
		Endpoint: endpoint, Evidence: []EvidenceReference{{Kind: "resource_manifest",
			Reference: filepath.Join(root, "desired.json"), SHA256: digest}}}, nil
}

func (adapter *localResourceAdapter) Reconcile(_ context.Context,
	receipt ResourceReceipt) (ResourceReceipt, error) {
	if receipt.Endpoint == "" {
		return ResourceReceipt{}, fmt.Errorf("project: resource endpoint is missing")
	}
	if strings.HasPrefix(receipt.Endpoint, "contract://") {
		receipt.State = "ready"
		return receipt, nil
	}
	if _, err := os.Stat(receipt.Endpoint); err != nil {
		receipt.State = "missing"
		return receipt, err
	}
	receipt.State = "ready"
	return receipt, nil
}

func (adapter *localResourceAdapter) Export(_ context.Context, receipt ResourceReceipt,
	root string) ([]EvidenceReference, error) {
	if strings.HasPrefix(receipt.Endpoint, "contract://") {
		return []EvidenceReference{{Kind: "resource_contract", Reference: receipt.Endpoint}}, nil
	}
	sourceRoot := filepath.Dir(receipt.Endpoint)
	target := filepath.Join(root, "resources", receipt.ResourceID.String())
	if err := copyDeliveryTree(sourceRoot, target); err != nil {
		return nil, err
	}
	digest, err := directorySHA256(target)
	if err != nil {
		return nil, err
	}
	return []EvidenceReference{{Kind: "resource_export", Reference: target, SHA256: digest}}, nil
}

func validateResourceDesired(desired ResourceDesiredState) error {
	if desired.ID == uuid.Nil || desired.ProjectID == uuid.Nil ||
		!deliveryNamePattern.MatchString(desired.Name) ||
		strings.ToLower(strings.TrimSpace(desired.Provider)) != desired.Provider ||
		!validEnvironment(desired.Environment) || desired.MonthlyCostLimitCents < 0 ||
		desired.MonthlyCostLimitCents > 100_000_00 || desired.RetentionDays < 0 ||
		desired.RetentionDays > 3650 || len(desired.Capabilities) > 32 ||
		len(desired.SecretReferences) > 32 {
		return fmt.Errorf("project: invalid bounded resource desired state")
	}
	validKind := false
	for _, entry := range ResourceCatalog() {
		if entry.Kind == desired.Kind {
			validKind = true
			break
		}
	}
	if !validKind || (desired.Ownership != "ion_managed" && desired.Ownership != "external") ||
		strings.TrimSpace(desired.DataRisk) == "" {
		return fmt.Errorf("project: resource ownership and data risk are required")
	}
	for _, reference := range desired.SecretReferences {
		if !validVaultReference(reference) {
			return fmt.Errorf("project: resource secrets require vault references")
		}
	}
	if desired.Kind == ResourceExternalAPI && desired.Engine != "" {
		parsed, err := url.Parse(desired.Engine)
		if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Hostname() == "" {
			return fmt.Errorf("project: external API endpoint must be credential-free HTTPS")
		}
	}
	return nil
}

func normalizeEnvironmentVariables(input []EnvironmentVariable) ([]EnvironmentVariable, error) {
	result := append([]EnvironmentVariable(nil), input...)
	seen := map[string]bool{}
	for index := range result {
		result[index].Name = strings.TrimSpace(result[index].Name)
		result[index].Kind = strings.TrimSpace(result[index].Kind)
		result[index].Reference = strings.TrimSpace(result[index].Reference)
		result[index].Description = strings.TrimSpace(result[index].Description)
		if !environmentNamePattern.MatchString(result[index].Name) || seen[result[index].Name] ||
			(result[index].Kind != "config_reference" && result[index].Kind != "secret_reference") ||
			len(result[index].Description) > 512 {
			return nil, fmt.Errorf("project: environment variable schema is invalid")
		}
		if result[index].Kind == "secret_reference" && !validVaultReference(result[index].Reference) {
			return nil, fmt.Errorf("project: secret environment values require vault references")
		}
		if result[index].Kind == "config_reference" &&
			!strings.HasPrefix(result[index].Reference, "config://") {
			return nil, fmt.Errorf("project: non-secret environment values require config references")
		}
		seen[result[index].Name] = true
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func validVaultReference(reference string) bool {
	return strings.HasPrefix(reference, "vault://") && len(reference) <= 512 &&
		!strings.ContainsAny(reference, "\x00\r\n\t ")
}

func validEnvironment(environment EnvironmentScope) bool {
	switch environment {
	case EnvironmentDevelopment, EnvironmentTest, EnvironmentPreview, EnvironmentStaging, EnvironmentProduction:
		return true
	default:
		return false
	}
}

func containsResourceKind(values []ResourceKind, wanted ResourceKind) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func findResourcePlan(plans []ResourcePlan, id uuid.UUID) (ResourcePlan, bool) {
	for _, plan := range plans {
		if plan.ID == id {
			return plan, true
		}
	}
	return ResourcePlan{}, false
}

func appendBoundedResourceReceipts(values []ResourceReceipt, receipt ResourceReceipt) []ResourceReceipt {
	values = append(values, receipt)
	if len(values) > maxDeliveryItems {
		values = append([]ResourceReceipt(nil), values[len(values)-maxDeliveryItems:]...)
	}
	return values
}

func planHashInput(plan ResourcePlan) any {
	return struct {
		ProjectID         uuid.UUID
		WorkspaceRevision uint64
		Desired           ResourceDesiredState
		Actions           []string
		Classification    PolicyClassification
		EstimatedCost     int64
	}{plan.ProjectID, plan.WorkspaceRevision, plan.Desired, plan.Actions, plan.Classification, plan.EstimatedCostCents}
}

func trimDeliveryStrings(values []string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > 4096 || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func fileSHA256(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	digest := sha256.New()
	size, err := io.Copy(digest, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(digest.Sum(nil)), size, nil
}

func directorySHA256(root string) (string, error) {
	paths := []string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type().IsRegular() {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(paths)
	digest := sha256.New()
	for _, path := range paths {
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return "", err
		}
		fileDigest, _, err := fileSHA256(path)
		if err != nil {
			return "", err
		}
		_, _ = digest.Write([]byte(filepath.ToSlash(relative)))
		_, _ = digest.Write([]byte{0})
		_, _ = digest.Write([]byte(fileDigest))
		_, _ = digest.Write([]byte{0})
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func copyDeliveryTree(source, target string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o700)
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		defer input.Close()
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return err
		}
		output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(output, input)
		closeErr := output.Close()
		return errors.Join(copyErr, closeErr)
	})
}
