package project

import (
	"context"

	"github.com/google/uuid"
)

func (service *Service) DeliverySnapshot(ctx context.Context, actor,
	projectID uuid.UUID) (DeliverySnapshot, error) {
	return service.delivery.Snapshot(ctx, actor, projectID)
}

func (service *Service) PlanResource(ctx context.Context, actor uuid.UUID,
	input ResourcePlanInput) (ResourcePlan, error) {
	return service.delivery.PlanResource(ctx, actor, input)
}

func (service *Service) ApplyResource(ctx context.Context, actor uuid.UUID,
	input ResourceApplyInput, approved bool) (ResourceReceipt, error) {
	return service.delivery.ApplyResource(ctx, actor, input, approved)
}

func (service *Service) PutEnvironmentSchema(ctx context.Context, actor uuid.UUID,
	input EnvironmentSchemaInput) (EnvironmentSchema, error) {
	return service.delivery.PutEnvironment(ctx, actor, input)
}

func (service *Service) PlanMigration(ctx context.Context, actor uuid.UUID,
	input MigrationPlanInput) (MigrationPlan, error) {
	return service.delivery.PlanMigration(ctx, actor, input)
}

func (service *Service) ApplyMigration(ctx context.Context, actor uuid.UUID,
	input MigrationApplyInput, approved bool) (MigrationReceipt, error) {
	return service.delivery.ApplyMigration(ctx, actor, input, approved)
}

func (service *Service) RollbackMigration(ctx context.Context, actor uuid.UUID,
	input MigrationRollbackInput, approved bool) (MigrationReceipt, error) {
	return service.delivery.RollbackMigration(ctx, actor, input, approved)
}

func (service *Service) PlanDeployment(ctx context.Context, actor uuid.UUID,
	input DeploymentPlanInput) (DeploymentPlan, error) {
	return service.delivery.PlanDeployment(ctx, actor, input)
}

func (service *Service) ApplyDeployment(ctx context.Context, actor uuid.UUID,
	input DeploymentApplyInput, approved bool) (DeploymentReceipt, error) {
	return service.delivery.ApplyDeployment(ctx, actor, input, approved)
}

func (service *Service) ReconcileDeployment(ctx context.Context, actor uuid.UUID,
	input DeploymentReconcileInput) (DeploymentReceipt, error) {
	return service.delivery.ReconcileDeployment(ctx, actor, input)
}

func (service *Service) RollbackDeployment(ctx context.Context, actor uuid.UUID,
	input DeploymentRollbackInput, approved bool) (DeploymentReceipt, error) {
	return service.delivery.RollbackDeployment(ctx, actor, input, approved)
}

func (service *Service) PrepareCIPatch(ctx context.Context, actor,
	projectID uuid.UUID) (CIPatchPlan, error) {
	return service.delivery.PrepareCIPatch(ctx, actor, projectID)
}

func (service *Service) PrepareRelease(ctx context.Context, actor uuid.UUID,
	input ReleaseInput) (ReleaseReadiness, error) {
	return service.delivery.PrepareRelease(ctx, actor, input)
}

func (service *Service) PortableExport(ctx context.Context, actor,
	projectID uuid.UUID) (string, error) {
	return service.delivery.PortableExport(ctx, actor, projectID)
}
