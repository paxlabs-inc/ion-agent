package operatorapp

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	officecontrol "github.com/paxlabs-inc/ion-agent/internal/office"
	"github.com/paxlabs-inc/ion-agent/internal/tools"
	workcontrol "github.com/paxlabs-inc/ion-agent/internal/work"
)

func registerOfficeTools(
	ctx context.Context,
	manager *tools.Manager,
	service *officecontrol.Service,
	work *workcontrol.Service,
	workspaceRoot string,
) error {
	if manager == nil || service == nil || work == nil || workspaceRoot == "" {
		return fmt.Errorf("operator Office tools: manager, Office, and work services are required")
	}
	available := func(runCtx context.Context) error {
		status := service.Status(runCtx)
		if !status.Available {
			return fmt.Errorf("%s", status.Message)
		}
		return nil
	}
	registrations := []tools.Registration{
		officeRegistration(
			"office_list",
			"List the authenticated user's encrypted Office documents and their current metadata.",
			`{"type":"object","additionalProperties":false,"properties":{"archived":{"type":"boolean"}}}`,
			tools.ClassificationGreen,
			available,
			func(runCtx context.Context, raw json.RawMessage) (json.RawMessage, error) {
				actor, err := officeActor(runCtx)
				if err != nil {
					return nil, err
				}
				var input struct {
					Archived bool `json:"archived"`
				}
				if err := decodeStrictJSON(raw, &input); err != nil {
					return nil, err
				}
				documents, err := service.ListDocuments(runCtx, actor, input.Archived)
				return marshalOfficeToolResult(documents, err)
			},
		),
		officeRegistration(
			"office_inspect",
			"Inspect one Office document and its bounded immutable version history without returning document content.",
			`{"type":"object","additionalProperties":false,"required":["document_id"],"properties":{"document_id":{"type":"string","format":"uuid"}}}`,
			tools.ClassificationGreen,
			available,
			func(runCtx context.Context, raw json.RawMessage) (json.RawMessage, error) {
				actor, documentID, err := decodeOfficeDocumentScope(runCtx, raw)
				if err != nil {
					return nil, err
				}
				document, err := service.GetDocument(runCtx, actor, documentID)
				if err != nil {
					return nil, err
				}
				versions, err := service.ListVersions(runCtx, actor, documentID)
				return marshalOfficeToolResult(map[string]any{
					"document": document,
					"versions": versions,
				}, err)
			},
		),
		officeRegistration(
			"office_create",
			"Create a blank encrypted Office document for the authenticated user. This mutates the durable Office library.",
			`{"type":"object","additionalProperties":false,"required":["title","kind"],"properties":{"title":{"type":"string","minLength":1,"maxLength":256},"kind":{"enum":["document","spreadsheet","presentation"]}}}`,
			tools.ClassificationYellow,
			available,
			func(runCtx context.Context, raw json.RawMessage) (json.RawMessage, error) {
				actor, err := officeActor(runCtx)
				if err != nil {
					return nil, err
				}
				var input officecontrol.CreateDocumentRequest
				if err := decodeStrictJSON(raw, &input); err != nil {
					return nil, err
				}
				document, err := service.CreateDocument(runCtx, actor, input)
				return marshalOfficeToolResult(document, err)
			},
		),
		officeRegistration(
			"office_rename",
			"Rename an Office document in the authenticated user's library.",
			`{"type":"object","additionalProperties":false,"required":["document_id","title"],"properties":{"document_id":{"type":"string","format":"uuid"},"title":{"type":"string","minLength":1,"maxLength":256}}}`,
			tools.ClassificationYellow,
			available,
			func(runCtx context.Context, raw json.RawMessage) (json.RawMessage, error) {
				actor, err := officeActor(runCtx)
				if err != nil {
					return nil, err
				}
				var input struct {
					DocumentID uuid.UUID `json:"document_id"`
					Title      string    `json:"title"`
				}
				if err := decodeStrictJSON(raw, &input); err != nil ||
					input.DocumentID == uuid.Nil {
					return nil, fmt.Errorf("operator Office tools: valid document_id is required")
				}
				err = service.RenameDocument(runCtx, actor, input.DocumentID, input.Title)
				return marshalOfficeToolResult(map[string]bool{"renamed": err == nil}, err)
			},
		),
		officeRegistration(
			"office_archive",
			"Archive or restore an Office document in the authenticated user's library.",
			`{"type":"object","additionalProperties":false,"required":["document_id","archived"],"properties":{"document_id":{"type":"string","format":"uuid"},"archived":{"type":"boolean"}}}`,
			tools.ClassificationYellow,
			available,
			func(runCtx context.Context, raw json.RawMessage) (json.RawMessage, error) {
				actor, err := officeActor(runCtx)
				if err != nil {
					return nil, err
				}
				var input struct {
					DocumentID uuid.UUID `json:"document_id"`
					Archived   bool      `json:"archived"`
				}
				if err := decodeStrictJSON(raw, &input); err != nil ||
					input.DocumentID == uuid.Nil {
					return nil, fmt.Errorf("operator Office tools: valid document_id is required")
				}
				if input.Archived {
					err = service.ArchiveDocument(runCtx, actor, input.DocumentID)
				} else {
					err = service.RestoreDocument(runCtx, actor, input.DocumentID)
				}
				return marshalOfficeToolResult(map[string]bool{"archived": input.Archived}, err)
			},
		),
		officeRegistration(
			"office_restore_version",
			"Restore a historical Office version by committing its content as a new immutable version.",
			`{"type":"object","additionalProperties":false,"required":["document_id","version_id"],"properties":{"document_id":{"type":"string","format":"uuid"},"version_id":{"type":"string","format":"uuid"}}}`,
			tools.ClassificationYellow,
			available,
			func(runCtx context.Context, raw json.RawMessage) (json.RawMessage, error) {
				actor, err := officeActor(runCtx)
				if err != nil {
					return nil, err
				}
				var input struct {
					DocumentID uuid.UUID `json:"document_id"`
					VersionID  uuid.UUID `json:"version_id"`
				}
				if err := decodeStrictJSON(raw, &input); err != nil ||
					input.DocumentID == uuid.Nil || input.VersionID == uuid.Nil {
					return nil, fmt.Errorf("operator Office tools: valid document_id and version_id are required")
				}
				version, err := service.RestoreVersion(
					runCtx, actor, input.DocumentID, input.VersionID,
				)
				return marshalOfficeToolResult(version, err)
			},
		),
		officeRegistration(
			"office_register_artifact",
			"Materialize the current encrypted Office version into the workspace and register it as independently verified evidence for an existing outcome contract.",
			`{"type":"object","additionalProperties":false,"required":["document_id","contract_id","criteria_covered"],"properties":{"document_id":{"type":"string","format":"uuid"},"contract_id":{"type":"string","format":"uuid"},"criteria_covered":{"type":"array","minItems":1,"maxItems":32,"items":{"type":"string","minLength":1,"maxLength":128}}}}`,
			tools.ClassificationYellow,
			available,
			func(runCtx context.Context, raw json.RawMessage) (json.RawMessage, error) {
				actor, err := officeActor(runCtx)
				if err != nil {
					return nil, err
				}
				var input struct {
					DocumentID      uuid.UUID `json:"document_id"`
					ContractID      uuid.UUID `json:"contract_id"`
					CriteriaCovered []string  `json:"criteria_covered"`
				}
				if err := decodeStrictJSON(raw, &input); err != nil ||
					input.DocumentID == uuid.Nil || input.ContractID == uuid.Nil {
					return nil, fmt.Errorf("operator Office tools: valid document_id and contract_id are required")
				}
				document, err := service.GetDocument(runCtx, actor, input.DocumentID)
				if err != nil {
					return nil, err
				}
				content, _, _, err := service.DownloadVersion(
					runCtx, actor, input.DocumentID, document.CurrentVersionID,
				)
				if err != nil {
					return nil, err
				}
				if len(content) > 32<<20 {
					return nil, fmt.Errorf("operator Office tools: artifact exceeds 32 MiB")
				}
				reference := fmt.Sprintf(
					"office-artifacts/%s/%s%s",
					document.ID,
					document.CurrentVersionID,
					document.Extension,
				)
				artifact, err := work.RecordArtifactInWorkspace(
					runCtx,
					actor,
					workcontrol.ArtifactInput{
						ContractID:      input.ContractID,
						Kind:            "office_document",
						Title:           document.Title,
						Reference:       reference,
						CriteriaCovered: input.CriteriaCovered,
					},
					workspaceRoot,
				)
				if err != nil {
					return nil, err
				}
				if err := writeOfficeArtifact(
					filepath.Join(workspaceRoot, reference), content,
				); err != nil {
					return nil, err
				}
				artifact, err = work.VerifyArtifactInWorkspace(
					runCtx, actor, artifact.ID, workspaceRoot,
				)
				return marshalOfficeToolResult(artifact, err)
			},
		),
	}
	for _, registration := range registrations {
		if err := manager.Register(ctx, registration); err != nil {
			return fmt.Errorf(
				"operator Office tools: register %s: %w",
				registration.Name,
				err,
			)
		}
	}
	return nil
}

func officeRegistration(
	name string,
	description string,
	schema string,
	classification tools.Classification,
	check tools.CheckFunc,
	handler tools.Handler,
) tools.Registration {
	return tools.Registration{
		Name: name, Description: description, Parameters: json.RawMessage(schema),
		Timeout: 30 * time.Second, Check: check, Handler: handler,
		Classification: classification,
	}
}

func officeActor(ctx context.Context) (uuid.UUID, error) {
	scope, ok := controlplane.ApprovalScopeFromContext(ctx)
	if !ok || scope.ActorID == uuid.Nil {
		return uuid.Nil, fmt.Errorf("operator Office tools: authenticated actor is required")
	}
	return scope.ActorID, nil
}

func decodeOfficeDocumentScope(
	ctx context.Context,
	raw json.RawMessage,
) (uuid.UUID, uuid.UUID, error) {
	actor, err := officeActor(ctx)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	var input struct {
		DocumentID uuid.UUID `json:"document_id"`
	}
	if err := decodeStrictJSON(raw, &input); err != nil || input.DocumentID == uuid.Nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf(
			"operator Office tools: valid document_id is required",
		)
	}
	return actor, input.DocumentID, nil
}

func marshalOfficeToolResult(value any, err error) (json.RawMessage, error) {
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("operator Office tools: encode result: %w", err)
	}
	return raw, nil
}
