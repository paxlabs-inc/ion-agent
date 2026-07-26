package operatorapp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	projectcontrol "github.com/paxlabs-inc/ion-agent/internal/project"
)

func TestProjectDeliveryProductionControlPlaneLiveStagingAndIsolation(t *testing.T) {
	ctx := context.Background()
	dataRoot, workspace, projectRoot := t.TempDir(), t.TempDir(), t.TempDir()
	initializeRuntimeVault(t, ctx, dataRoot)
	runtime, err := OpenRuntime(ctx, RuntimeConfig{DataDirectory: dataRoot, DevelopmentFileKEK: true,
		WorkspaceDirectory: workspace, ProjectWorkspaceRoot: projectRoot})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	actor, other := uuid.New(), uuid.New()
	project := dispatchStudioProject(t, ctx, runtime, actor, controlplane.OperationProjectCreate,
		"delivery-project", map[string]any{"name": "Delivery app", "template": "static-web", "host": "direct_local"})
	planResponse := dispatchStudio(t, ctx, runtime, actor, controlplane.OperationProjectDeploymentPlan,
		"delivery-plan", projectcontrol.DeploymentPlanInput{ProjectID: project.ID,
			WorkspaceRevision: project.WorkspaceRevision, Environment: projectcontrol.EnvironmentStaging,
			Provider: "local_staging", HealthPath: "/", Version: "1.0.0"})
	var plan projectcontrol.DeploymentPlan
	decodeStudioResult(t, planResponse, &plan)
	if plan.Artifact.SHA256 == "" || plan.Classification != projectcontrol.PolicyYellow {
		t.Fatalf("deployment plan = %+v", plan)
	}
	applyResponse := dispatchStudio(t, ctx, runtime, actor, controlplane.OperationProjectDeploymentApply,
		"delivery-apply", projectcontrol.DeploymentApplyInput{ProjectID: project.ID, PlanID: plan.ID})
	var receipt projectcontrol.DeploymentReceipt
	decodeStudioResult(t, applyResponse, &receipt)
	if receipt.State != "healthy" || receipt.Health != "passing" || receipt.URL == "" {
		t.Fatalf("deployment receipt = %+v", receipt)
	}
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get(receipt.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("live staging status = %d", response.StatusCode)
	}
	payload, _ := json.Marshal(map[string]any{"project_id": project.ID})
	snapshot := projectQuery(t, ctx, runtime, actor, controlplane.OperationProjectDeliveryGet, string(payload))
	if !bytes.Contains(snapshot.Result, []byte(`"state":"healthy"`)) ||
		!bytes.Contains(snapshot.Result, []byte(plan.Artifact.SHA256)) {
		t.Fatalf("delivery snapshot = %s", snapshot.Result)
	}
	exportResponse := dispatchStudio(t, ctx, runtime, actor, controlplane.OperationProjectPortableExport,
		"delivery-export", map[string]any{"project_id": project.ID})
	if !bytes.Contains(exportResponse.Result, []byte(`"path"`)) {
		t.Fatalf("portable export = %s", exportResponse.Result)
	}
	crossActor := projectQuery(t, ctx, runtime, other, controlplane.OperationProjectDeliveryGet, string(payload))
	if !bytes.Contains(crossActor.Result, []byte(`"status":"unavailable"`)) ||
		!bytes.Contains(crossActor.Result, []byte(`"reason":"project was not found"`)) {
		t.Fatalf("cross-actor delivery = %s", crossActor.Result)
	}
}
