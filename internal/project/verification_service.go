package project

import (
	"context"

	"github.com/google/uuid"
)

func (service *Service) DeriveVerificationManifest(ctx context.Context, actor uuid.UUID,
	input VerificationManifestInput) (VerificationManifest, error) {
	return service.verification.Derive(ctx, actor, input)
}

func (service *Service) CurrentVerificationManifest(ctx context.Context, actor,
	projectID uuid.UUID) (VerificationManifest, error) {
	return service.verification.Current(ctx, actor, projectID)
}

func (service *Service) RunVerification(ctx context.Context, actor uuid.UUID,
	input VerificationRunRequest) (VerificationRun, error) {
	return service.verification.Run(ctx, actor, input)
}

func (service *Service) ListVerificationRuns(ctx context.Context, actor,
	projectID uuid.UUID) ([]VerificationRun, error) {
	return service.verification.ListRuns(ctx, actor, projectID)
}

func (service *Service) PutVerificationWaiver(ctx context.Context, actor uuid.UUID,
	input VerificationWaiverInput) (VerificationWaiver, error) {
	return service.verification.PutWaiver(ctx, actor, input)
}

func (service *Service) ListVerificationWaivers(ctx context.Context, actor,
	projectID uuid.UUID) ([]VerificationWaiver, error) {
	return service.verification.ListWaivers(ctx, actor, projectID)
}
