package project

import (
	"archive/zip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
	_ "modernc.org/sqlite"
)

func TestDeliveryProviderConformanceResourcesEnvironmentsAndSecretIsolation(t *testing.T) {
	ctx := context.Background()
	dataRoot, workspaceRoot, attachRoot, deliveryRoot := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	manager, store := openProjectStore(t, ctx, dataRoot)
	defer manager.Close()
	defer store.Close(ctx)
	service, err := NewService(store, types.SystemClock{}, ServiceConfig{
		WorkspaceRoot: workspaceRoot, AttachRoots: []string{attachRoot}, DeliveryRoot: deliveryRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	root := filepath.Join(attachRoot, "resources")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	actor := uuid.New()
	project, err := service.AttachDirectory(ctx, testMeta(actor, "delivery-resources"),
		AttachInput{Name: "Delivery resources", Directory: root})
	if err != nil {
		t.Fatal(err)
	}
	empty, err := service.DeliverySnapshot(ctx, actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	emptyRaw, err := json.Marshal(empty)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(emptyRaw), `"resources":[]`) ||
		!strings.Contains(string(emptyRaw), `"environments":[]`) ||
		!strings.Contains(string(emptyRaw), `"migrations":[]`) ||
		!strings.Contains(string(emptyRaw), `"deployments":[]`) {
		t.Fatalf("empty delivery collections must be arrays: %s", emptyRaw)
	}
	catalog := ResourceCatalog()
	if len(catalog) != 9 {
		t.Fatalf("catalog kinds = %d", len(catalog))
	}
	seen := map[ResourceKind]bool{}
	for _, entry := range catalog {
		seen[entry.Kind] = true
		if len(entry.Capabilities) == 0 || len(entry.DataRisks) == 0 {
			t.Fatalf("catalog entry lacks capability or risk metadata: %+v", entry)
		}
	}
	for _, kind := range []ResourceKind{ResourceDatabase, ResourceStorage, ResourceAuth, ResourceEmail,
		ResourceQueue, ResourceSchedule, ResourceAnalytics, ResourcePayment, ResourceExternalAPI} {
		if !seen[kind] {
			t.Fatalf("catalog omitted %s", kind)
		}
	}
	grant, err := service.IssueSecretGrant(ctx, actor, "vault://projects/database-password", 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := service.PutEnvironmentSchema(ctx, actor, EnvironmentSchemaInput{
		ProjectID: project.ID, Environment: EnvironmentProduction,
		Variables: []EnvironmentVariable{
			{Name: "DATABASE_PASSWORD", Kind: "secret_reference", Reference: grant.Reference, Required: true},
			{Name: "PUBLIC_ORIGIN", Kind: "config_reference", Reference: "config://projects/public-origin", Required: true},
		},
	})
	if err != nil || schema.Revision != 1 {
		t.Fatalf("environment schema = %+v, %v", schema, err)
	}
	if _, err := service.PutEnvironmentSchema(ctx, actor, EnvironmentSchemaInput{
		ProjectID: project.ID, Environment: EnvironmentStaging,
		Variables: []EnvironmentVariable{{Name: "TOKEN", Kind: "secret_reference", Reference: "plaintext-secret", Required: true}},
	}); err == nil {
		t.Fatal("plaintext environment value was accepted")
	}
	plan, err := service.PlanResource(ctx, actor, ResourcePlanInput{
		ProjectID: project.ID, WorkspaceRevision: project.WorkspaceRevision,
		Desired: ResourceDesiredState{Name: "primary-db", Kind: ResourceDatabase, Provider: "local",
			Environment: EnvironmentProduction, Capabilities: []string{"schema", "backup"},
			Ownership: "ion_managed", DataRisk: "customer_data", Engine: "sqlite",
			MonthlyCostLimitCents: 10, SecretReferences: []string{grant.Reference}},
	})
	if err != nil || plan.Classification != PolicyRed || plan.EstimatedCostCents != 0 {
		t.Fatalf("resource plan = %+v, %v", plan, err)
	}
	if _, err := service.ApplyResource(ctx, actor, ResourceApplyInput{
		ProjectID: project.ID, PlanID: plan.ID, SecretGrants: []SecretGrant{grant}, IdempotencyKey: "resource-production",
	}, false); err == nil {
		t.Fatal("production resource apply bypassed RED approval")
	}
	receipt, err := service.ApplyResource(ctx, actor, ResourceApplyInput{
		ProjectID: project.ID, PlanID: plan.ID, SecretGrants: []SecretGrant{grant}, IdempotencyKey: "resource-production",
	}, true)
	if err != nil || receipt.State != "ready" || receipt.Endpoint == "" {
		t.Fatalf("resource receipt = %+v, %v", receipt, err)
	}
	repeated, err := service.ApplyResource(ctx, actor, ResourceApplyInput{
		ProjectID: project.ID, PlanID: plan.ID, SecretGrants: []SecretGrant{grant}, IdempotencyKey: "resource-production",
	}, true)
	if err != nil || repeated.ID != receipt.ID {
		t.Fatalf("idempotent resource = %+v, %v", repeated, err)
	}
	if database, err := sql.Open("sqlite", receipt.Endpoint); err != nil {
		t.Fatal(err)
	} else {
		defer database.Close()
		var count int
		if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM ion_resource_metadata`).Scan(&count); err != nil || count != 3 {
			t.Fatalf("resource database conformance count = %d, %v", count, err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(dataRoot, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), grant.Reference) || strings.Contains(string(raw), grant.Token) {
		t.Fatal("environment reference or grant leaked from encrypted living state")
	}
	if other, err := service.DeliverySnapshot(ctx, uuid.New(), project.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-actor delivery snapshot = %+v, %v", other, err)
	}
}

func TestDeliverySQLiteMigrationDryRunPromotionBackupAndRollback(t *testing.T) {
	ctx := context.Background()
	dataRoot, workspaceRoot, attachRoot := t.TempDir(), t.TempDir(), t.TempDir()
	manager, store := openProjectStore(t, ctx, dataRoot)
	defer manager.Close()
	defer store.Close(ctx)
	service, err := NewService(store, types.SystemClock{}, ServiceConfig{
		WorkspaceRoot: workspaceRoot, AttachRoots: []string{attachRoot},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	root := filepath.Join(attachRoot, "migration")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"development.sqlite", "test.sqlite", "destructive.sqlite"} {
		createSQLiteDatabase(t, filepath.Join(root, name))
	}
	actor := uuid.New()
	project, err := service.AttachDirectory(ctx, testMeta(actor, "migration-project"),
		AttachInput{Name: "Migration project", Directory: root})
	if err != nil {
		t.Fatal(err)
	}
	steps := []MigrationStep{{ID: "create_accounts",
		SQL:      "CREATE TABLE accounts (id INTEGER PRIMARY KEY, name TEXT NOT NULL)",
		Rollback: "DROP TABLE accounts"}}
	development, err := service.PlanMigration(ctx, actor, MigrationPlanInput{
		ProjectID: project.ID, WorkspaceRevision: project.WorkspaceRevision,
		Environment: EnvironmentDevelopment, DatabasePath: "development.sqlite", Steps: steps,
	})
	if err != nil || !development.DryRunPassed || len(development.SchemaAfter) != 1 {
		t.Fatalf("development dry run = %+v, %v", development, err)
	}
	if _, err := service.ApplyMigration(ctx, actor, MigrationApplyInput{
		ProjectID: project.ID, PlanID: development.ID,
	}, false); err != nil {
		t.Fatal(err)
	}
	testPlan, err := service.PlanMigration(ctx, actor, MigrationPlanInput{
		ProjectID: project.ID, WorkspaceRevision: project.WorkspaceRevision,
		Environment: EnvironmentTest, DatabasePath: "test.sqlite", Steps: steps,
	})
	if err != nil {
		t.Fatal(err)
	}
	testReceipt, err := service.ApplyMigration(ctx, actor, MigrationApplyInput{
		ProjectID: project.ID, PlanID: testPlan.ID,
	}, false)
	if err != nil || testReceipt.BackupSHA256 == "" || testReceipt.State != "applied" {
		t.Fatalf("test migration = %+v, %v", testReceipt, err)
	}
	rolledBack, err := service.RollbackMigration(ctx, actor, MigrationRollbackInput{
		ProjectID: project.ID, ReceiptID: testReceipt.ID,
	}, false)
	if err != nil || rolledBack.State != "rolled_back" || rolledBack.RolledBackAt == nil {
		t.Fatalf("migration rollback = %+v, %v", rolledBack, err)
	}
	assertSQLiteTableAbsent(t, filepath.Join(root, "test.sqlite"), "accounts")
	database, err := sql.Open("sqlite", filepath.Join(root, "destructive.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TABLE old_data (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	destructive, err := service.PlanMigration(ctx, actor, MigrationPlanInput{
		ProjectID: project.ID, WorkspaceRevision: project.WorkspaceRevision,
		Environment: EnvironmentDevelopment, DatabasePath: "destructive.sqlite",
		Steps: []MigrationStep{{ID: "drop_old_data", SQL: "DROP TABLE old_data",
			Rollback: "CREATE TABLE old_data (id INTEGER PRIMARY KEY)"}},
	})
	if err != nil || destructive.Classification != PolicyRed || len(destructive.DestructiveFindings) != 1 {
		t.Fatalf("destructive plan = %+v, %v", destructive, err)
	}
	if _, err := service.ApplyMigration(ctx, actor, MigrationApplyInput{
		ProjectID: project.ID, PlanID: destructive.ID,
	}, false); err == nil {
		t.Fatal("destructive migration bypassed RED approval")
	}
}

func TestDeliveryLiveStagingReconcileRollbackReleaseAndPortableExport(t *testing.T) {
	ctx := context.Background()
	dataRoot, workspaceRoot, attachRoot, deliveryRoot := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	manager, store := openProjectStore(t, ctx, dataRoot)
	service, err := NewService(store, types.SystemClock{}, ServiceConfig{
		WorkspaceRoot: workspaceRoot, AttachRoots: []string{attachRoot}, DeliveryRoot: deliveryRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(attachRoot, "staging")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<h1>release one</h1>\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("API_TOKEN=must-not-export\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	actor := uuid.New()
	project, err := service.AttachDirectory(ctx, testMeta(actor, "staging-project"),
		AttachInput{Name: "Staging project", Directory: root})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := service.DeriveVerificationManifest(ctx, actor, VerificationManifestInput{
		ProjectID: project.ID, WorkspaceRevision: project.WorkspaceRevision,
		Criteria: []VerificationCriterion{{ID: "release.static", Description: "Static release is readable.", Kinds: []string{"test"}}},
		Gates: []VerificationGate{{ID: "release-test", Kind: "test", Argv: []string{"/bin/sh", "-c", "test -s index.html"},
			TimeoutSeconds: 10, Required: true, Criteria: []string{"release.static"}, EvidenceKinds: []string{"logs"}, Available: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if run, err := service.RunVerification(ctx, actor, VerificationRunRequest{
		ProjectID: project.ID, ManifestID: manifest.ID, Full: true,
	}); err != nil || run.Status != "passed" {
		t.Fatalf("release verification = %+v, %v", run, err)
	}
	firstPlan, err := service.PlanDeployment(ctx, actor, DeploymentPlanInput{
		ProjectID: project.ID, WorkspaceRevision: project.WorkspaceRevision,
		Environment: EnvironmentStaging, Provider: "local_staging", HealthPath: "/",
		Version: "1.0.0", CostLimitCents: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.ApplyDeployment(ctx, actor, DeploymentApplyInput{
		ProjectID: project.ID, PlanID: firstPlan.ID, IdempotencyKey: "deploy-one",
	}, false)
	if err != nil || first.State != "healthy" || fetchBody(t, first.URL) != "<h1>release one</h1>\n" {
		t.Fatalf("first live staging = %+v, %v", first, err)
	}
	repeated, err := service.ApplyDeployment(ctx, actor, DeploymentApplyInput{
		ProjectID: project.ID, PlanID: firstPlan.ID, IdempotencyKey: "deploy-one",
	}, false)
	if err != nil || repeated.ID != first.ID {
		t.Fatalf("idempotent deployment = %+v, %v", repeated, err)
	}
	release, err := service.PrepareRelease(ctx, actor, ReleaseInput{
		ProjectID: project.ID, ReleaseVersion: "1.0.0",
		Notes: []string{"Initial staging release."}, Changelog: []string{"Add the static application."},
	})
	if err != nil || !release.Ready || release.DNSState != "not_applicable" {
		t.Fatalf("release readiness = %+v, %v", release, err)
	}
	exportPath, err := service.PortableExport(ctx, actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertZipContainsAndExcludes(t, exportPath, "source/index.html", "source/.env")
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	manager, store = reopenProjectStore(t, ctx, dataRoot)
	defer manager.Close()
	defer store.Close(ctx)
	service, err = NewService(store, types.SystemClock{}, ServiceConfig{
		WorkspaceRoot: workspaceRoot, AttachRoots: []string{attachRoot}, DeliveryRoot: deliveryRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	reconciled, err := service.ReconcileDeployment(ctx, actor, DeploymentReconcileInput{
		ProjectID: project.ID, ReceiptID: first.ID,
	})
	if err != nil || reconciled.Health != "passing" || fetchBody(t, reconciled.URL) != "<h1>release one</h1>\n" {
		t.Fatalf("restart reconciliation = %+v, %v", reconciled, err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<h1>release two</h1>\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	project, err = service.ObserveWorkspaceChange(ctx, actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	secondPlan, err := service.PlanDeployment(ctx, actor, DeploymentPlanInput{
		ProjectID: project.ID, WorkspaceRevision: project.WorkspaceRevision,
		Environment: EnvironmentStaging, Provider: "local_staging", HealthPath: "/",
		Version: "1.1.0", CostLimitCents: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.ApplyDeployment(ctx, actor, DeploymentApplyInput{
		ProjectID: project.ID, PlanID: secondPlan.ID, IdempotencyKey: "deploy-two",
	}, false)
	if err != nil || fetchBody(t, second.URL) != "<h1>release two</h1>\n" || second.PreviousReceipt == nil {
		t.Fatalf("second staging = %+v, %v", second, err)
	}
	rolledBack, err := service.RollbackDeployment(ctx, actor, DeploymentRollbackInput{
		ProjectID: project.ID, ReceiptID: second.ID,
	}, false)
	if err != nil || rolledBack.State != "healthy" || fetchBody(t, rolledBack.URL) != "<h1>release one</h1>\n" {
		t.Fatalf("deployment rollback = %+v, %v", rolledBack, err)
	}
}

func createSQLiteDatabase(t *testing.T, path string) {
	t.Helper()
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
}

func assertSQLiteTableAbsent(t *testing.T, path, table string) {
	t.Helper()
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name=?`, table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("table %s still exists", table)
	}
}

func fetchBody(t *testing.T, rawURL string) string {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d", rawURL, response.StatusCode)
	}
	return string(body)
}

func assertZipContainsAndExcludes(t *testing.T, path, present, absent string) {
	t.Helper()
	archive, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	foundPresent, foundAbsent := false, false
	for _, file := range archive.File {
		foundPresent = foundPresent || file.Name == present
		foundAbsent = foundAbsent || file.Name == absent
	}
	if !foundPresent || foundAbsent {
		t.Fatalf("portable export present=%v absent=%v", foundPresent, foundAbsent)
	}
}
