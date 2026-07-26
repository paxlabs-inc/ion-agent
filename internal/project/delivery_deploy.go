package project

import (
	"archive/zip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

var destructiveSQLPattern = regexp.MustCompile(`(?i)\b(DROP\s+(TABLE|COLUMN|INDEX)|TRUNCATE|DELETE\s+FROM\s+[^\s;]+\s*(;|$)|ALTER\s+TABLE\b.*\bDROP\b)`)

func (service *DeliveryService) PlanMigration(ctx context.Context, actor uuid.UUID,
	input MigrationPlanInput) (MigrationPlan, error) {
	if actor == uuid.Nil || input.ProjectID == uuid.Nil || input.WorkspaceRevision == 0 ||
		!validEnvironment(input.Environment) || len(input.Steps) == 0 || len(input.Steps) > 128 {
		return MigrationPlan{}, fmt.Errorf("project: complete bounded migration plan input is required")
	}
	project, err := service.projects.Get(ctx, actor, input.ProjectID)
	if err != nil {
		return MigrationPlan{}, err
	}
	if project.WorkspaceRevision != input.WorkspaceRevision {
		return MigrationPlan{}, ErrStaleRevision
	}
	databasePath, err := securePatchPath(project.Root, input.DatabasePath, false)
	if err != nil {
		return MigrationPlan{}, err
	}
	if info, err := os.Stat(databasePath); err != nil || !info.Mode().IsRegular() {
		return MigrationPlan{}, fmt.Errorf("project: migration database must be an existing regular file")
	}
	steps, destructive, err := normalizeMigrationSteps(input.Steps)
	if err != nil {
		return MigrationPlan{}, err
	}
	before, err := inspectSQLiteSchema(ctx, databasePath)
	if err != nil {
		return MigrationPlan{}, err
	}
	shadow, err := os.CreateTemp(service.root, "migration-shadow-*.sqlite")
	if err != nil {
		return MigrationPlan{}, err
	}
	shadowPath := shadow.Name()
	if err := shadow.Close(); err != nil {
		return MigrationPlan{}, err
	}
	defer os.Remove(shadowPath)
	if err := copyFile(databasePath, shadowPath); err != nil {
		return MigrationPlan{}, err
	}
	if err := executeSQLiteSteps(ctx, shadowPath, steps, false); err != nil {
		return MigrationPlan{}, fmt.Errorf("project: migration dry-run failed: %w", err)
	}
	after, err := inspectSQLiteSchema(ctx, shadowPath)
	if err != nil {
		return MigrationPlan{}, err
	}
	classification := PolicyYellow
	if input.Environment == EnvironmentProduction || len(destructive) > 0 {
		classification = PolicyRed
	}
	plan := MigrationPlan{Version: DeliveryContractVersion, ID: uuid.New(), ActorID: actor,
		ProjectID: project.ID, WorkspaceRevision: project.WorkspaceRevision,
		Environment: input.Environment, DatabasePath: input.DatabasePath, Steps: steps,
		SchemaBefore: before, SchemaAfter: after, DestructiveFindings: destructive,
		DryRunPassed: true, Classification: classification, CreatedAt: service.clock.Now().UTC()}
	plan.PlanSHA256 = requestHash("migration.plan", migrationPlanHashInput(plan))
	service.mu.Lock()
	defer service.mu.Unlock()
	state, err := service.load(ctx, actor, project.ID)
	if err != nil {
		return MigrationPlan{}, err
	}
	state.MigrationPlans = append(state.MigrationPlans, plan)
	if len(state.MigrationPlans) > maxDeliveryItems {
		state.MigrationPlans = append([]MigrationPlan(nil), state.MigrationPlans[len(state.MigrationPlans)-maxDeliveryItems:]...)
	}
	if err := service.save(ctx, actor, project.ID, &state); err != nil {
		return MigrationPlan{}, err
	}
	return plan, nil
}

func (service *DeliveryService) ApplyMigration(ctx context.Context, actor uuid.UUID,
	input MigrationApplyInput, approved bool) (MigrationReceipt, error) {
	if actor == uuid.Nil || input.ProjectID == uuid.Nil || input.PlanID == uuid.Nil {
		return MigrationReceipt{}, fmt.Errorf("project: complete migration apply input is required")
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	state, err := service.load(ctx, actor, input.ProjectID)
	if err != nil {
		return MigrationReceipt{}, err
	}
	plan, ok := findMigrationPlan(state.MigrationPlans, input.PlanID)
	if !ok {
		return MigrationReceipt{}, ErrNotFound
	}
	if !plan.DryRunPassed || requestHash("migration.plan", migrationPlanHashInput(plan)) != plan.PlanSHA256 {
		return MigrationReceipt{}, ErrConflict
	}
	project, err := service.projects.Get(ctx, actor, input.ProjectID)
	if err != nil {
		return MigrationReceipt{}, err
	}
	if project.WorkspaceRevision != plan.WorkspaceRevision {
		return MigrationReceipt{}, ErrStaleRevision
	}
	if plan.Classification == PolicyRed && !approved {
		return MigrationReceipt{}, fmt.Errorf("%w: explicit RED approval is required", ErrUnsupported)
	}
	if err := requireMigrationPromotion(state, plan); err != nil {
		return MigrationReceipt{}, err
	}
	databasePath, err := securePatchPath(project.Root, plan.DatabasePath, false)
	if err != nil {
		return MigrationReceipt{}, err
	}
	receiptID := uuid.New()
	backupRoot := filepath.Join(service.root, "migrations", actor.String(), project.ID.String())
	if err := os.MkdirAll(backupRoot, 0o700); err != nil {
		return MigrationReceipt{}, err
	}
	backupPath := filepath.Join(backupRoot, receiptID.String()+".sqlite")
	if err := copyFile(databasePath, backupPath); err != nil {
		return MigrationReceipt{}, err
	}
	backupSHA, _, err := fileSHA256(backupPath)
	if err != nil {
		return MigrationReceipt{}, err
	}
	if err := executeSQLiteSteps(ctx, databasePath, plan.Steps, false); err != nil {
		_ = copyFile(backupPath, databasePath)
		return MigrationReceipt{}, fmt.Errorf("project: migration apply failed and backup was restored: %w", err)
	}
	schemaAfter, err := inspectSQLiteSchema(ctx, databasePath)
	if err != nil {
		_ = copyFile(backupPath, databasePath)
		return MigrationReceipt{}, err
	}
	if requestHash("migration.schema", schemaAfter) != requestHash("migration.schema", plan.SchemaAfter) {
		_ = copyFile(backupPath, databasePath)
		return MigrationReceipt{}, fmt.Errorf("project: applied migration did not match the dry-run schema")
	}
	now := service.clock.Now().UTC()
	receipt := MigrationReceipt{Version: DeliveryContractVersion, ID: receiptID, PlanID: plan.ID,
		ActorID: actor, ProjectID: project.ID, Environment: plan.Environment, State: "applied",
		BackupPath: backupPath, BackupSHA256: backupSHA, SchemaAfter: schemaAfter,
		RollbackEvidence: []EvidenceReference{{Kind: "database_backup", Reference: backupPath, SHA256: backupSHA}},
		AppliedAt:        now}
	state.Migrations = append(state.Migrations, receipt)
	if len(state.Migrations) > maxDeliveryItems {
		state.Migrations = append([]MigrationReceipt(nil), state.Migrations[len(state.Migrations)-maxDeliveryItems:]...)
	}
	if err := service.save(ctx, actor, project.ID, &state); err != nil {
		_ = copyFile(backupPath, databasePath)
		return MigrationReceipt{}, err
	}
	return receipt, nil
}

func (service *DeliveryService) RollbackMigration(ctx context.Context, actor uuid.UUID,
	input MigrationRollbackInput, approved bool) (MigrationReceipt, error) {
	if actor == uuid.Nil || input.ProjectID == uuid.Nil || input.ReceiptID == uuid.Nil {
		return MigrationReceipt{}, fmt.Errorf("project: complete migration rollback input is required")
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	state, err := service.load(ctx, actor, input.ProjectID)
	if err != nil {
		return MigrationReceipt{}, err
	}
	receipt, index, ok := findMigrationReceipt(state.Migrations, input.ReceiptID)
	if !ok {
		return MigrationReceipt{}, ErrNotFound
	}
	if receipt.RolledBackAt != nil {
		return receipt, nil
	}
	plan, ok := findMigrationPlan(state.MigrationPlans, receipt.PlanID)
	if !ok {
		return MigrationReceipt{}, ErrNotFound
	}
	if receipt.Environment == EnvironmentProduction && !approved {
		return MigrationReceipt{}, fmt.Errorf("%w: explicit RED approval is required", ErrUnsupported)
	}
	project, err := service.projects.Get(ctx, actor, input.ProjectID)
	if err != nil {
		return MigrationReceipt{}, err
	}
	databasePath, err := securePatchPath(project.Root, plan.DatabasePath, false)
	if err != nil {
		return MigrationReceipt{}, err
	}
	backupSHA, _, err := fileSHA256(receipt.BackupPath)
	if err != nil || backupSHA != receipt.BackupSHA256 {
		return MigrationReceipt{}, fmt.Errorf("project: migration backup evidence is missing or changed")
	}
	if err := copyFile(receipt.BackupPath, databasePath); err != nil {
		return MigrationReceipt{}, err
	}
	now := service.clock.Now().UTC()
	receipt.State, receipt.RolledBackAt = "rolled_back", &now
	restoredSHA, _, err := fileSHA256(databasePath)
	if err != nil {
		return MigrationReceipt{}, err
	}
	receipt.RollbackEvidence = append(receipt.RollbackEvidence,
		EvidenceReference{Kind: "rollback_restore", Reference: databasePath, SHA256: restoredSHA})
	state.Migrations[index] = receipt
	if err := service.save(ctx, actor, project.ID, &state); err != nil {
		return MigrationReceipt{}, err
	}
	return receipt, nil
}

func (service *DeliveryService) PlanDeployment(ctx context.Context, actor uuid.UUID,
	input DeploymentPlanInput) (DeploymentPlan, error) {
	if actor == uuid.Nil || input.ProjectID == uuid.Nil || input.WorkspaceRevision == 0 ||
		!validEnvironment(input.Environment) || strings.TrimSpace(input.Provider) == "" ||
		strings.TrimSpace(input.Version) == "" || len(input.Version) > 128 ||
		input.CostLimitCents < 0 || input.CostLimitCents > 10_000_00 {
		return DeploymentPlan{}, fmt.Errorf("project: complete bounded deployment plan input is required")
	}
	project, err := service.projects.Get(ctx, actor, input.ProjectID)
	if err != nil {
		return DeploymentPlan{}, err
	}
	if project.WorkspaceRevision != input.WorkspaceRevision {
		return DeploymentPlan{}, ErrStaleRevision
	}
	provider := strings.ToLower(strings.TrimSpace(input.Provider))
	adapter := service.deploymentAdapters[provider]
	if adapter == nil {
		return DeploymentPlan{}, fmt.Errorf("%w: deployment provider %q is unavailable", ErrUnsupported, provider)
	}
	healthPath := strings.TrimSpace(input.HealthPath)
	if healthPath == "" {
		healthPath = "/"
	}
	if !strings.HasPrefix(healthPath, "/") || strings.ContainsAny(healthPath, "\x00\r\n") {
		return DeploymentPlan{}, fmt.Errorf("project: health path must be an origin-relative path")
	}
	if input.EnvironmentRevision > 0 {
		service.mu.Lock()
		state, loadErr := service.load(ctx, actor, project.ID)
		service.mu.Unlock()
		if loadErr != nil {
			return DeploymentPlan{}, loadErr
		}
		if _, ok := findEnvironment(state.Environments, input.Environment, input.EnvironmentRevision); !ok {
			return DeploymentPlan{}, fmt.Errorf("project: environment schema revision was not found")
		}
	}
	artifact, err := service.buildImmutableArtifact(project)
	if err != nil {
		return DeploymentPlan{}, err
	}
	classification := PolicyYellow
	if input.Environment == EnvironmentProduction {
		classification = PolicyRed
	}
	plan := DeploymentPlan{Version: DeliveryContractVersion, ID: uuid.New(), ActorID: actor,
		ProjectID: project.ID, WorkspaceRevision: project.WorkspaceRevision,
		Environment: input.Environment, Provider: provider, Artifact: artifact,
		HealthPath: healthPath, Domain: strings.TrimSpace(input.Domain),
		ReleaseVersion: strings.TrimSpace(input.Version), EnvironmentRevision: input.EnvironmentRevision,
		CostLimitCents: input.CostLimitCents, Classification: classification,
		CreatedAt: service.clock.Now().UTC()}
	actions, err := adapter.Plan(ctx, plan)
	if err != nil {
		return DeploymentPlan{}, err
	}
	plan.Actions = trimDeliveryStrings(actions)
	plan.PlanSHA256 = requestHash("deployment.plan", deploymentPlanHashInput(plan))
	service.mu.Lock()
	defer service.mu.Unlock()
	state, err := service.load(ctx, actor, project.ID)
	if err != nil {
		return DeploymentPlan{}, err
	}
	state.DeploymentPlans = append(state.DeploymentPlans, plan)
	if len(state.DeploymentPlans) > maxDeliveryItems {
		state.DeploymentPlans = append([]DeploymentPlan(nil), state.DeploymentPlans[len(state.DeploymentPlans)-maxDeliveryItems:]...)
	}
	if err := service.save(ctx, actor, project.ID, &state); err != nil {
		return DeploymentPlan{}, err
	}
	return plan, nil
}

func (service *DeliveryService) ApplyDeployment(ctx context.Context, actor uuid.UUID,
	input DeploymentApplyInput, approved bool) (DeploymentReceipt, error) {
	if actor == uuid.Nil || input.ProjectID == uuid.Nil || input.PlanID == uuid.Nil ||
		strings.TrimSpace(input.IdempotencyKey) == "" || len(input.IdempotencyKey) > 256 {
		return DeploymentReceipt{}, fmt.Errorf("project: complete deployment apply input is required")
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	state, err := service.load(ctx, actor, input.ProjectID)
	if err != nil {
		return DeploymentReceipt{}, err
	}
	plan, ok := findDeploymentPlan(state.DeploymentPlans, input.PlanID)
	if !ok {
		return DeploymentReceipt{}, ErrNotFound
	}
	if requestHash("deployment.plan", deploymentPlanHashInput(plan)) != plan.PlanSHA256 {
		return DeploymentReceipt{}, ErrConflict
	}
	if plan.Classification == PolicyRed && !approved {
		return DeploymentReceipt{}, fmt.Errorf("%w: explicit RED approval is required", ErrUnsupported)
	}
	if plan.Environment == EnvironmentProduction && !matchingHealthyStaging(state.Deployments, plan) {
		return DeploymentReceipt{}, fmt.Errorf("project: production promotion requires the same healthy staging artifact")
	}
	project, err := service.projects.Get(ctx, actor, input.ProjectID)
	if err != nil {
		return DeploymentReceipt{}, err
	}
	if project.WorkspaceRevision != plan.WorkspaceRevision {
		return DeploymentReceipt{}, ErrStaleRevision
	}
	references := []string{}
	if plan.EnvironmentRevision > 0 {
		schema, ok := findEnvironment(state.Environments, plan.Environment, plan.EnvironmentRevision)
		if !ok {
			return DeploymentReceipt{}, ErrNotFound
		}
		for _, variable := range schema.Variables {
			if variable.Kind == "secret_reference" {
				references = append(references, variable.Reference)
			}
		}
	}
	if err := service.verifyReferences(ctx, actor, references, input.SecretGrants); err != nil {
		return DeploymentReceipt{}, err
	}
	requestSHA := requestHash("deployment.apply", struct {
		PlanID uuid.UUID
		Refs   []string
	}{plan.ID, references})
	for _, receipt := range state.Deployments {
		if receipt.IdempotencyKey != input.IdempotencyKey {
			continue
		}
		if receipt.RequestSHA256 != requestSHA {
			return DeploymentReceipt{}, ErrConflict
		}
		return receipt, nil
	}
	adapter := service.deploymentAdapters[plan.Provider]
	if adapter == nil {
		return DeploymentReceipt{}, ErrUnsupported
	}
	receipt, applyErr := adapter.Apply(ctx, plan)
	now := service.clock.Now().UTC()
	if receipt.ID == uuid.Nil {
		receipt.ID = uuid.New()
	}
	receipt.Version, receipt.PlanID, receipt.ActorID = DeliveryContractVersion, plan.ID, actor
	receipt.ProjectID, receipt.Environment, receipt.Provider = plan.ProjectID, plan.Environment, plan.Provider
	receipt.ReleaseVersion, receipt.ArtifactSHA256 = plan.ReleaseVersion, plan.Artifact.SHA256
	receipt.Classification, receipt.IdempotencyKey, receipt.RequestSHA256 = plan.Classification, input.IdempotencyKey, requestSHA
	receipt.CreatedAt, receipt.ReconciledAt = now, now
	if previous := latestDeployment(state.Deployments, plan.Environment, "healthy"); previous != nil {
		receipt.PreviousReceipt = &previous.ID
		receipt.RollbackHandle = previous.ID.String()
	}
	if applyErr != nil {
		receipt.State, receipt.Health, receipt.Logs = "outcome_unknown", "unknown", safeError(applyErr)
		state.Deployments = appendBoundedDeployments(state.Deployments, receipt)
		if saveErr := service.save(ctx, actor, plan.ProjectID, &state); saveErr != nil {
			return DeploymentReceipt{}, errors.Join(applyErr, saveErr)
		}
		return receipt, applyErr
	}
	if receipt.State != "healthy" || receipt.Health != "passing" ||
		receipt.URL == "" || receipt.ActualCostCents > plan.CostLimitCents {
		return DeploymentReceipt{}, fmt.Errorf("project: deployment lacks direct bounded health evidence")
	}
	state.Deployments = appendBoundedDeployments(state.Deployments, receipt)
	if err := service.save(ctx, actor, plan.ProjectID, &state); err != nil {
		return DeploymentReceipt{}, err
	}
	return receipt, nil
}

func (service *DeliveryService) ReconcileDeployment(ctx context.Context, actor uuid.UUID,
	input DeploymentReconcileInput) (DeploymentReceipt, error) {
	if actor == uuid.Nil || input.ProjectID == uuid.Nil || input.ReceiptID == uuid.Nil {
		return DeploymentReceipt{}, fmt.Errorf("project: complete deployment reconcile input is required")
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	state, err := service.load(ctx, actor, input.ProjectID)
	if err != nil {
		return DeploymentReceipt{}, err
	}
	receipt, index, ok := findDeploymentReceipt(state.Deployments, input.ReceiptID)
	if !ok {
		return DeploymentReceipt{}, ErrNotFound
	}
	plan, ok := findDeploymentPlan(state.DeploymentPlans, receipt.PlanID)
	if !ok {
		return DeploymentReceipt{}, ErrNotFound
	}
	adapter := service.deploymentAdapters[receipt.Provider]
	if adapter == nil {
		return DeploymentReceipt{}, ErrUnsupported
	}
	reconciled, err := adapter.Reconcile(ctx, receipt, plan.Artifact, plan.HealthPath)
	if err != nil {
		reconciled.State, reconciled.Health, reconciled.Logs = "failed", "failing", safeError(err)
	}
	reconciled.ReconciledAt = service.clock.Now().UTC()
	state.Deployments[index] = reconciled
	if saveErr := service.save(ctx, actor, input.ProjectID, &state); saveErr != nil {
		return DeploymentReceipt{}, errors.Join(err, saveErr)
	}
	return reconciled, err
}

func (service *DeliveryService) RollbackDeployment(ctx context.Context, actor uuid.UUID,
	input DeploymentRollbackInput, approved bool) (DeploymentReceipt, error) {
	if actor == uuid.Nil || input.ProjectID == uuid.Nil || input.ReceiptID == uuid.Nil {
		return DeploymentReceipt{}, fmt.Errorf("project: complete deployment rollback input is required")
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	state, err := service.load(ctx, actor, input.ProjectID)
	if err != nil {
		return DeploymentReceipt{}, err
	}
	current, currentIndex, ok := findDeploymentReceipt(state.Deployments, input.ReceiptID)
	if !ok || current.PreviousReceipt == nil {
		return DeploymentReceipt{}, ErrNotFound
	}
	if current.Environment == EnvironmentProduction && !approved {
		return DeploymentReceipt{}, fmt.Errorf("%w: explicit RED approval is required", ErrUnsupported)
	}
	previous, _, ok := findDeploymentReceipt(state.Deployments, *current.PreviousReceipt)
	if !ok {
		return DeploymentReceipt{}, ErrNotFound
	}
	previousPlan, ok := findDeploymentPlan(state.DeploymentPlans, previous.PlanID)
	if !ok {
		return DeploymentReceipt{}, ErrNotFound
	}
	adapter := service.deploymentAdapters[current.Provider]
	if adapter == nil {
		return DeploymentReceipt{}, ErrUnsupported
	}
	rolledBack, err := adapter.Rollback(ctx, current, previous, previousPlan.Artifact, previousPlan.HealthPath)
	if err != nil {
		return DeploymentReceipt{}, err
	}
	now := service.clock.Now().UTC()
	current.State, current.RolledBackAt, current.ReconciledAt = "rolled_back", &now, now
	current.Evidence = append(current.Evidence, EvidenceReference{Kind: "rollback",
		Reference: rolledBack.ID.String(), SHA256: rolledBack.ArtifactSHA256})
	state.Deployments[currentIndex] = current
	rolledBack.ID, rolledBack.PreviousReceipt = uuid.New(), &current.ID
	rolledBack.RollbackHandle, rolledBack.CreatedAt, rolledBack.ReconciledAt = current.ID.String(), now, now
	rolledBack.IdempotencyKey = "rollback:" + current.ID.String()
	rolledBack.RequestSHA256 = requestHash("deployment.rollback", current.ID)
	state.Deployments = appendBoundedDeployments(state.Deployments, rolledBack)
	if err := service.save(ctx, actor, input.ProjectID, &state); err != nil {
		return DeploymentReceipt{}, err
	}
	return rolledBack, nil
}

func (service *DeliveryService) PrepareCIPatch(ctx context.Context, actor,
	projectID uuid.UUID) (CIPatchPlan, error) {
	project, err := service.projects.Get(ctx, actor, projectID)
	if err != nil {
		return CIPatchPlan{}, err
	}
	var path, content string
	switch {
	case regularFile(filepath.Join(project.Root, "go.mod")):
		path = ".github/workflows/ion-release.yml"
		content = "name: ion-release\non: [push, pull_request]\njobs:\n  verify:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/checkout@v4\n      - uses: actions/setup-go@v5\n        with:\n          go-version-file: go.mod\n      - run: go test ./...\n      - run: go build ./...\n"
	case regularFile(filepath.Join(project.Root, "package.json")):
		path = ".github/workflows/ion-release.yml"
		content = "name: ion-release\non: [push, pull_request]\njobs:\n  verify:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/checkout@v4\n      - uses: actions/setup-node@v4\n        with:\n          node-version: 22\n          cache: npm\n      - run: npm ci\n      - run: npm test\n      - run: npm run build\n"
	default:
		return CIPatchPlan{}, fmt.Errorf("%w: no reviewed CI template matches the inspected project", ErrUnsupported)
	}
	expected := ""
	if existing := filepath.Join(project.Root, path); regularFile(existing) {
		expected, _, err = fileSHA256(existing)
		if err != nil {
			return CIPatchPlan{}, err
		}
	}
	return CIPatchPlan{Version: DeliveryContractVersion, ProjectID: project.ID,
		WorkspaceRevision: project.WorkspaceRevision, Path: path, ExpectedSHA256: expected,
		Content: content, Classification: PolicyYellow, ReviewRequired: true}, nil
}

func (service *DeliveryService) PrepareRelease(ctx context.Context, actor uuid.UUID,
	input ReleaseInput) (ReleaseReadiness, error) {
	if actor == uuid.Nil || input.ProjectID == uuid.Nil || strings.TrimSpace(input.ReleaseVersion) == "" ||
		len(input.ReleaseVersion) > 128 {
		return ReleaseReadiness{}, fmt.Errorf("project: release version is required")
	}
	if _, err := service.projects.Get(ctx, actor, input.ProjectID); err != nil {
		return ReleaseReadiness{}, err
	}
	service.mu.Lock()
	state, err := service.load(ctx, actor, input.ProjectID)
	service.mu.Unlock()
	if err != nil {
		return ReleaseReadiness{}, err
	}
	readiness := ReleaseReadiness{Version: DeliveryContractVersion, ActorID: actor,
		ProjectID: input.ProjectID, ReleaseVersion: strings.TrimSpace(input.ReleaseVersion),
		Notes: trimDeliveryStrings(input.Notes), Changelog: trimDeliveryStrings(input.Changelog),
		Domain: strings.TrimSpace(input.Domain), DNSState: "not_applicable",
		CreatedAt: service.clock.Now().UTC()}
	deployment := latestDeploymentForVersion(state.Deployments, readiness.ReleaseVersion)
	if deployment == nil || deployment.State != "healthy" || deployment.Health != "passing" {
		readiness.Unmet = append(readiness.Unmet, "healthy direct-service deployment evidence")
	} else {
		readiness.ProviderChecks = append(readiness.ProviderChecks, deployment.Evidence...)
	}
	if readiness.Domain != "" {
		addresses, lookupErr := net.DefaultResolver.LookupHost(ctx, readiness.Domain)
		if lookupErr != nil || len(addresses) == 0 {
			readiness.DNSState = "unverified"
			readiness.Unmet = append(readiness.Unmet, "domain and DNS readiness")
		} else {
			readiness.DNSState = "resolves"
			readiness.ProviderChecks = append(readiness.ProviderChecks,
				EvidenceReference{Kind: "dns", Reference: readiness.Domain})
		}
	}
	if len(readiness.Notes) == 0 {
		readiness.Unmet = append(readiness.Unmet, "release notes")
	}
	if len(readiness.Changelog) == 0 {
		readiness.Unmet = append(readiness.Unmet, "changelog")
	}
	if manifest, manifestErr := service.projects.CurrentVerificationManifest(ctx, actor, input.ProjectID); manifestErr != nil {
		readiness.Unmet = append(readiness.Unmet, "revision-bound verification manifest")
	} else {
		runs, runErr := service.projects.ListVerificationRuns(ctx, actor, input.ProjectID)
		if runErr != nil || !fullPassingVerification(runs, manifest) {
			readiness.Unmet = append(readiness.Unmet, "complete passing release gates")
		}
	}
	readiness.Ready = len(readiness.Unmet) == 0
	service.mu.Lock()
	defer service.mu.Unlock()
	state, err = service.load(ctx, actor, input.ProjectID)
	if err != nil {
		return ReleaseReadiness{}, err
	}
	state.Release = &readiness
	if err := service.save(ctx, actor, input.ProjectID, &state); err != nil {
		return ReleaseReadiness{}, err
	}
	return readiness, nil
}

func (service *DeliveryService) PortableExport(ctx context.Context, actor,
	projectID uuid.UUID) (string, error) {
	project, err := service.projects.Get(ctx, actor, projectID)
	if err != nil {
		return "", err
	}
	service.mu.Lock()
	state, err := service.load(ctx, actor, projectID)
	service.mu.Unlock()
	if err != nil {
		return "", err
	}
	exportRoot, err := os.MkdirTemp(service.root, "portable-export-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(exportRoot)
	if err := copyProjectForDelivery(project.Root, filepath.Join(exportRoot, "source")); err != nil {
		return "", err
	}
	configRaw, err := json.MarshalIndent(struct {
		Version      string              `json:"version"`
		Environments []EnvironmentSchema `json:"environments"`
		Deployments  []DeploymentReceipt `json:"deployments"`
		Migrations   []MigrationReceipt  `json:"migrations"`
	}{DeliveryContractVersion, state.Environments, state.Deployments, state.Migrations}, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(exportRoot, "delivery.json"), configRaw, 0o600); err != nil {
		return "", err
	}
	for _, receipt := range state.Resources {
		adapter := service.resourceAdapters[receipt.Provider]
		if adapter == nil {
			return "", fmt.Errorf("%w: resource export adapter %q is unavailable", ErrUnsupported, receipt.Provider)
		}
		if _, err := adapter.Export(ctx, receipt, exportRoot); err != nil {
			return "", err
		}
	}
	exportDir := filepath.Join(service.root, "exports", actor.String(), project.ID.String())
	if err := os.MkdirAll(exportDir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(exportDir, service.clock.Now().UTC().Format("20060102T150405.000000000")+".zip")
	if _, err := zipDeliveryTree(exportRoot, path); err != nil {
		return "", err
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	state, err = service.load(ctx, actor, projectID)
	if err != nil {
		return "", err
	}
	if state.Release == nil {
		state.Release = &ReleaseReadiness{Version: DeliveryContractVersion, ActorID: actor,
			ProjectID: projectID, DNSState: "not_applicable", CreatedAt: service.clock.Now().UTC()}
	}
	state.Release.PortableExport = path
	if err := service.save(ctx, actor, projectID, &state); err != nil {
		return "", err
	}
	return path, nil
}

type localDeployment struct {
	server   *http.Server
	listener net.Listener
	root     string
}

type localStagingAdapter struct {
	mu     sync.Mutex
	root   string
	active map[string]*localDeployment
	client *http.Client
}

func newLocalStagingAdapter(root string) (*localStagingAdapter, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &localStagingAdapter{root: root, active: map[string]*localDeployment{},
		client: &http.Client{Transport: transport, Timeout: 2 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (adapter *localStagingAdapter) Name() string {
	return "local_staging"
}

func (adapter *localStagingAdapter) Plan(_ context.Context, plan DeploymentPlan) ([]string, error) {
	if plan.Environment != EnvironmentPreview && plan.Environment != EnvironmentStaging {
		return nil, fmt.Errorf("%w: local staging supports preview and staging only", ErrUnsupported)
	}
	archive, err := zip.OpenReader(plan.Artifact.Path)
	if err != nil {
		return nil, err
	}
	defer archive.Close()
	hasIndex := false
	for _, file := range archive.File {
		if file.Name == "index.html" || strings.HasSuffix(file.Name, "/index.html") {
			hasIndex = true
			break
		}
	}
	if !hasIndex {
		return nil, fmt.Errorf("%w: local staging requires a static index.html artifact", ErrUnsupported)
	}
	return []string{"verify immutable artifact", "extract into isolated release", "start loopback staging service",
		"perform direct HTTP health check", "retain prior release for rollback"}, nil
}

func (adapter *localStagingAdapter) Apply(ctx context.Context,
	plan DeploymentPlan) (DeploymentReceipt, error) {
	root, err := adapter.extract(plan.ActorID, plan.ProjectID, plan.Artifact)
	if err != nil {
		return DeploymentReceipt{}, err
	}
	deployment, rawURL, err := adapter.start(plan.ProjectID, plan.Environment, root)
	if err != nil {
		return DeploymentReceipt{}, err
	}
	healthURL := strings.TrimRight(rawURL, "/") + plan.HealthPath
	status, err := adapter.health(ctx, healthURL)
	if err != nil {
		_ = deployment.server.Shutdown(context.Background())
		return DeploymentReceipt{}, err
	}
	return DeploymentReceipt{ID: uuid.New(), State: "healthy", URL: rawURL, Health: "passing",
		Logs: fmt.Sprintf("direct health check returned HTTP %d", status), ActualCostCents: 0,
		Evidence: []EvidenceReference{{Kind: "artifact", Reference: plan.Artifact.Path, SHA256: plan.Artifact.SHA256},
			{Kind: "direct_health", Reference: healthURL}}}, nil
}

func (adapter *localStagingAdapter) Reconcile(ctx context.Context, receipt DeploymentReceipt,
	artifact Artifact, healthPath string) (DeploymentReceipt, error) {
	adapter.mu.Lock()
	active := adapter.active[localDeploymentKey(receipt.ProjectID, receipt.Environment)]
	adapter.mu.Unlock()
	if active == nil {
		root, err := adapter.extract(receipt.ActorID, receipt.ProjectID, artifact)
		if err != nil {
			return receipt, err
		}
		_, rawURL, err := adapter.start(receipt.ProjectID, receipt.Environment, root)
		if err != nil {
			return receipt, err
		}
		receipt.URL = rawURL
	}
	healthURL := strings.TrimRight(receipt.URL, "/") + healthPath
	status, err := adapter.health(ctx, healthURL)
	if err != nil {
		return receipt, err
	}
	receipt.State, receipt.Health = "healthy", "passing"
	receipt.Logs = fmt.Sprintf("reconciled direct health check returned HTTP %d", status)
	receipt.Evidence = append(receipt.Evidence, EvidenceReference{Kind: "reconciled_health", Reference: healthURL})
	return receipt, nil
}

func (adapter *localStagingAdapter) Rollback(ctx context.Context, _ DeploymentReceipt,
	previous DeploymentReceipt, artifact Artifact, healthPath string) (DeploymentReceipt, error) {
	root, err := adapter.extract(previous.ActorID, previous.ProjectID, artifact)
	if err != nil {
		return DeploymentReceipt{}, err
	}
	_, rawURL, err := adapter.start(previous.ProjectID, previous.Environment, root)
	if err != nil {
		return DeploymentReceipt{}, err
	}
	previous.URL = rawURL
	return adapter.Reconcile(ctx, previous, artifact, healthPath)
}

func (adapter *localStagingAdapter) Close() error {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	var result error
	for key, deployment := range adapter.active {
		result = errors.Join(result, deployment.server.Shutdown(context.Background()))
		delete(adapter.active, key)
	}
	return result
}

func (adapter *localStagingAdapter) extract(actor, projectID uuid.UUID,
	artifact Artifact) (string, error) {
	digest, _, err := fileSHA256(artifact.Path)
	if err != nil || digest != artifact.SHA256 {
		return "", fmt.Errorf("project: immutable artifact evidence changed")
	}
	root := filepath.Join(adapter.root, actor.String(), projectID.String(), artifact.SHA256)
	if info, statErr := os.Stat(root); statErr == nil && info.IsDir() {
		return root, nil
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	archive, err := zip.OpenReader(artifact.Path)
	if err != nil {
		return "", err
	}
	defer archive.Close()
	for _, file := range archive.File {
		clean := filepath.Clean(filepath.FromSlash(file.Name))
		if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("project: artifact contains an unsafe path")
		}
		target := filepath.Join(root, clean)
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o700); err != nil {
				return "", err
			}
			continue
		}
		if !file.Mode().IsRegular() {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return "", err
		}
		input, err := file.Open()
		if err != nil {
			return "", err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			input.Close()
			return "", err
		}
		_, copyErr := io.Copy(output, input)
		closeErr := errors.Join(input.Close(), output.Close())
		if copyErr != nil || closeErr != nil {
			return "", errors.Join(copyErr, closeErr)
		}
	}
	return root, nil
}

func (adapter *localStagingAdapter) start(projectID uuid.UUID, environment EnvironmentScope,
	root string) (*localDeployment, string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", err
	}
	server := &http.Server{Handler: http.FileServer(http.Dir(root)),
		ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second,
		IdleTimeout: 30 * time.Second}
	deployment := &localDeployment{server: server, listener: listener, root: root}
	key := localDeploymentKey(projectID, environment)
	adapter.mu.Lock()
	previous := adapter.active[key]
	adapter.active[key] = deployment
	adapter.mu.Unlock()
	if previous != nil {
		_ = previous.server.Shutdown(context.Background())
	}
	go func() {
		_ = server.Serve(listener)
	}()
	return deployment, "http://" + listener.Addr().String(), nil
}

func (adapter *localStagingAdapter) health(ctx context.Context, rawURL string) (int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, err
	}
	response, err := adapter.client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
	if response.StatusCode < 200 || response.StatusCode >= 400 {
		return response.StatusCode, fmt.Errorf("project: direct health check returned HTTP %d", response.StatusCode)
	}
	return response.StatusCode, nil
}

func (service *DeliveryService) buildImmutableArtifact(project Project) (Artifact, error) {
	artifactRoot := filepath.Join(service.root, "artifacts", project.ID.String())
	if err := os.MkdirAll(artifactRoot, 0o700); err != nil {
		return Artifact{}, err
	}
	temporary, err := os.CreateTemp(artifactRoot, "artifact-*.zip")
	if err != nil {
		return Artifact{}, err
	}
	temporaryPath := temporary.Name()
	if err := temporary.Close(); err != nil {
		return Artifact{}, err
	}
	defer os.Remove(temporaryPath)
	size, err := zipProjectForDelivery(project.Root, temporaryPath)
	if err != nil {
		return Artifact{}, err
	}
	digest, _, err := fileSHA256(temporaryPath)
	if err != nil {
		return Artifact{}, err
	}
	path := filepath.Join(artifactRoot, digest+".zip")
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := os.Rename(temporaryPath, path); err != nil {
			return Artifact{}, err
		}
	}
	return Artifact{ID: uuid.New(), ProjectID: project.ID, WorkspaceRevision: project.WorkspaceRevision,
		Path: path, SHA256: digest, SizeBytes: size, CreatedAt: service.clock.Now().UTC()}, nil
}

func zipProjectForDelivery(root, target string) (int64, error) {
	paths := []string{}
	var total int64
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() && deliveryExcludedPath(relative) && relative != "." {
			return filepath.SkipDir
		}
		if !entry.Type().IsRegular() || deliveryExcludedPath(relative) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		if len(paths) >= maxArchiveFiles || total > maxArchiveBytes {
			return fmt.Errorf("project: immutable artifact exceeds the bounded archive limit")
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		return 0, err
	}
	sort.Strings(paths)
	file, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	writer := zip.NewWriter(file)
	for _, path := range paths {
		relative, err := filepath.Rel(root, path)
		if err != nil {
			_ = writer.Close()
			_ = file.Close()
			return 0, err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			_ = writer.Close()
			_ = file.Close()
			return 0, err
		}
		_, findings := redactSecrets(relative, data)
		if len(findings) > 0 {
			continue
		}
		header := &zip.FileHeader{Name: filepath.ToSlash(relative), Method: zip.Deflate}
		header.SetMode(0o600)
		header.Modified = time.Unix(0, 0).UTC()
		entry, err := writer.CreateHeader(header)
		if err != nil {
			_ = writer.Close()
			_ = file.Close()
			return 0, err
		}
		if _, err := entry.Write(data); err != nil {
			_ = writer.Close()
			_ = file.Close()
			return 0, err
		}
	}
	closeErr := errors.Join(writer.Close(), file.Close())
	if closeErr != nil {
		return 0, closeErr
	}
	info, err := os.Stat(target)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func zipDeliveryTree(root, target string) (int64, error) {
	return zipProjectForDelivery(root, target)
}

func copyProjectForDelivery(source, target string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if entry.IsDir() && deliveryExcludedPath(relative) && relative != "." {
			return filepath.SkipDir
		}
		if deliveryExcludedPath(relative) {
			return nil
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o700)
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		_, findings := redactSecrets(relative, data)
		if len(findings) > 0 {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return err
		}
		return os.WriteFile(destination, data, 0o600)
	})
}

func deliveryExcludedPath(relative string) bool {
	relative = filepath.ToSlash(relative)
	if relative == "." {
		return false
	}
	first := strings.Split(relative, "/")[0]
	base := strings.ToLower(filepath.Base(relative))
	return first == ".git" || first == "node_modules" || first == ".ion" ||
		base == ".env" || strings.HasPrefix(base, ".env.") ||
		strings.HasSuffix(base, ".pem") || strings.HasSuffix(base, ".key")
}

func normalizeMigrationSteps(input []MigrationStep) ([]MigrationStep, []string, error) {
	steps := append([]MigrationStep(nil), input...)
	seen := map[string]bool{}
	findings := []string{}
	for index := range steps {
		steps[index].ID = strings.TrimSpace(steps[index].ID)
		steps[index].SQL = strings.TrimSpace(steps[index].SQL)
		steps[index].Rollback = strings.TrimSpace(steps[index].Rollback)
		if !deliveryNamePattern.MatchString(steps[index].ID) || seen[steps[index].ID] ||
			steps[index].SQL == "" || len(steps[index].SQL) > 1<<20 ||
			steps[index].Rollback == "" || len(steps[index].Rollback) > 1<<20 {
			return nil, nil, fmt.Errorf("project: migration steps require bounded SQL and rollback")
		}
		if destructiveSQLPattern.MatchString(steps[index].SQL) {
			findings = append(findings, steps[index].ID+": destructive SQL requires explicit review")
		}
		seen[steps[index].ID] = true
	}
	return steps, findings, nil
}

func executeSQLiteSteps(ctx context.Context, path string, steps []MigrationStep, rollback bool) error {
	database, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer database.Close()
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for index := range steps {
		statement := steps[index].SQL
		if rollback {
			statement = steps[len(steps)-1-index].Rollback
		}
		if _, err := transaction.ExecContext(ctx, statement); err != nil {
			_ = transaction.Rollback()
			return fmt.Errorf("%s: %w", steps[index].ID, err)
		}
	}
	if err := transaction.Commit(); err != nil {
		return err
	}
	var integrity string
	if err := database.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil {
		return err
	}
	if integrity != "ok" {
		return fmt.Errorf("sqlite integrity check: %s", integrity)
	}
	return nil
}

func inspectSQLiteSchema(ctx context.Context, path string) ([]string, error) {
	database, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	defer database.Close()
	rows, err := database.QueryContext(ctx, `SELECT type || ':' || name || ':' || COALESCE(sql, '')
		FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%' ORDER BY type, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []string{}
	for rows.Next() {
		var statement string
		if err := rows.Scan(&statement); err != nil {
			return nil, err
		}
		result = append(result, statement)
	}
	return result, rows.Err()
}

func copyFile(source, target string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	output, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	return errors.Join(copyErr, output.Close())
}

func requireMigrationPromotion(state deliveryState, plan MigrationPlan) error {
	previousEnvironment, required := priorEnvironment(plan.Environment)
	if !required {
		return nil
	}
	stepHash := requestHash("migration.steps", plan.Steps)
	for _, receipt := range state.Migrations {
		if receipt.Environment != previousEnvironment || receipt.State != "applied" {
			continue
		}
		previous, ok := findMigrationPlan(state.MigrationPlans, receipt.PlanID)
		if ok && requestHash("migration.steps", previous.Steps) == stepHash {
			return nil
		}
	}
	return fmt.Errorf("project: ordered migration promotion requires applied %s evidence", previousEnvironment)
}

func priorEnvironment(environment EnvironmentScope) (EnvironmentScope, bool) {
	switch environment {
	case EnvironmentTest:
		return EnvironmentDevelopment, true
	case EnvironmentPreview:
		return EnvironmentTest, true
	case EnvironmentStaging:
		return EnvironmentPreview, true
	case EnvironmentProduction:
		return EnvironmentStaging, true
	default:
		return "", false
	}
}

func findMigrationPlan(plans []MigrationPlan, id uuid.UUID) (MigrationPlan, bool) {
	for _, plan := range plans {
		if plan.ID == id {
			return plan, true
		}
	}
	return MigrationPlan{}, false
}

func findMigrationReceipt(receipts []MigrationReceipt, id uuid.UUID) (MigrationReceipt, int, bool) {
	for index, receipt := range receipts {
		if receipt.ID == id {
			return receipt, index, true
		}
	}
	return MigrationReceipt{}, -1, false
}

func findDeploymentPlan(plans []DeploymentPlan, id uuid.UUID) (DeploymentPlan, bool) {
	for _, plan := range plans {
		if plan.ID == id {
			return plan, true
		}
	}
	return DeploymentPlan{}, false
}

func findDeploymentReceipt(receipts []DeploymentReceipt, id uuid.UUID) (DeploymentReceipt, int, bool) {
	for index, receipt := range receipts {
		if receipt.ID == id {
			return receipt, index, true
		}
	}
	return DeploymentReceipt{}, -1, false
}

func findEnvironment(values []EnvironmentSchema, environment EnvironmentScope,
	revision uint64) (EnvironmentSchema, bool) {
	for _, value := range values {
		if value.Environment == environment && value.Revision == revision {
			return value, true
		}
	}
	return EnvironmentSchema{}, false
}

func migrationPlanHashInput(plan MigrationPlan) any {
	return struct {
		ProjectID         uuid.UUID
		WorkspaceRevision uint64
		Environment       EnvironmentScope
		DatabasePath      string
		Steps             []MigrationStep
		SchemaBefore      []string
		SchemaAfter       []string
		Destructive       []string
		DryRunPassed      bool
		Classification    PolicyClassification
	}{plan.ProjectID, plan.WorkspaceRevision, plan.Environment, plan.DatabasePath, plan.Steps,
		plan.SchemaBefore, plan.SchemaAfter, plan.DestructiveFindings, plan.DryRunPassed, plan.Classification}
}

func deploymentPlanHashInput(plan DeploymentPlan) any {
	return struct {
		ProjectID           uuid.UUID
		WorkspaceRevision   uint64
		Environment         EnvironmentScope
		Provider            string
		ArtifactSHA256      string
		HealthPath          string
		Domain              string
		ReleaseVersion      string
		EnvironmentRevision uint64
		CostLimitCents      int64
		Classification      PolicyClassification
		Actions             []string
	}{plan.ProjectID, plan.WorkspaceRevision, plan.Environment, plan.Provider, plan.Artifact.SHA256,
		plan.HealthPath, plan.Domain, plan.ReleaseVersion, plan.EnvironmentRevision,
		plan.CostLimitCents, plan.Classification, plan.Actions}
}

func appendBoundedDeployments(values []DeploymentReceipt, receipt DeploymentReceipt) []DeploymentReceipt {
	values = append(values, receipt)
	if len(values) > maxDeliveryItems {
		values = append([]DeploymentReceipt(nil), values[len(values)-maxDeliveryItems:]...)
	}
	return values
}

func latestDeployment(values []DeploymentReceipt, environment EnvironmentScope,
	state string) *DeploymentReceipt {
	for index := len(values) - 1; index >= 0; index-- {
		if values[index].Environment == environment && values[index].State == state {
			value := values[index]
			return &value
		}
	}
	return nil
}

func latestDeploymentForVersion(values []DeploymentReceipt, version string) *DeploymentReceipt {
	for index := len(values) - 1; index >= 0; index-- {
		if values[index].ReleaseVersion == version {
			value := values[index]
			return &value
		}
	}
	return nil
}

func matchingHealthyStaging(values []DeploymentReceipt, plan DeploymentPlan) bool {
	for _, receipt := range values {
		if receipt.Environment == EnvironmentStaging && receipt.State == "healthy" &&
			receipt.Health == "passing" && receipt.ArtifactSHA256 == plan.Artifact.SHA256 {
			return true
		}
	}
	return false
}

func fullPassingVerification(runs []VerificationRun, manifest VerificationManifest) bool {
	for index := len(runs) - 1; index >= 0; index-- {
		if runs[index].ManifestID == manifest.ID && runs[index].WorkspaceRevision == manifest.WorkspaceRevision &&
			runs[index].Mode == "full" && runs[index].Status == "passed" &&
			len(runs[index].UncoveredCriteria) == 0 {
			return true
		}
	}
	return false
}

func localDeploymentKey(projectID uuid.UUID, environment EnvironmentScope) string {
	return projectID.String() + ":" + string(environment)
}
