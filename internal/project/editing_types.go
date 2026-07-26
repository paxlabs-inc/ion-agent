package project

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

const PatchSetVersion = "ion.patch-set.v1"

var (
	ErrStalePreimage = errors.New("project: patch preimage is stale")
	ErrPatchPending  = errors.New("project: a patch transaction requires reconciliation")
)

type PatchOperation string

const (
	PatchWrite   PatchOperation = "write"
	PatchExact   PatchOperation = "exact_replace"
	PatchDelete  PatchOperation = "delete"
	PatchRename  PatchOperation = "rename"
	PatchCopy    PatchOperation = "copy"
	PatchMkdir   PatchOperation = "mkdir"
	PatchArchive PatchOperation = "archive"
	PatchJSONSet PatchOperation = "json_set"
)

type PatchMember struct {
	Operation             PatchOperation  `json:"operation"`
	Path                  string          `json:"path"`
	Destination           string          `json:"destination,omitempty"`
	ExpectedSHA256        string          `json:"expected_sha256"`
	Content               string          `json:"content,omitempty"`
	ContentBase64         string          `json:"content_base64,omitempty"`
	MediaType             string          `json:"media_type,omitempty"`
	ArchivePaths          []string        `json:"archive_paths,omitempty"`
	ArchiveBaselineSHA256 string          `json:"archive_baseline_sha256,omitempty"`
	JSONPointer           string          `json:"json_pointer,omitempty"`
	JSONValue             json.RawMessage `json:"json_value,omitempty"`
	OldText               string          `json:"old_text,omitempty"`
	NewText               string          `json:"new_text,omitempty"`
	ReplaceAll            bool            `json:"replace_all,omitempty"`
	Generated             bool            `json:"generated,omitempty"`
}

type PatchRollbackRequest struct {
	ProjectID         uuid.UUID `json:"project_id"`
	PatchSetID        uuid.UUID `json:"patch_set_id"`
	WorkspaceRevision uint64    `json:"workspace_revision"`
}

type PatchSet struct {
	Version          string        `json:"version"`
	ID               uuid.UUID     `json:"id"`
	ProjectID        uuid.UUID     `json:"project_id"`
	BaselineRevision uint64        `json:"baseline_revision"`
	Criteria         []string      `json:"criteria"`
	ValidationPlan   []string      `json:"validation_plan"`
	Members          []PatchMember `json:"members"`
}

type PatchFileResult struct {
	Path         string `json:"path"`
	BeforeSHA256 string `json:"before_sha256"`
	AfterSHA256  string `json:"after_sha256"`
	Generated    bool   `json:"generated,omitempty"`
}

type PatchReceipt struct {
	Version           string               `json:"version"`
	PatchSetID        uuid.UUID            `json:"patch_set_id"`
	ProjectID         uuid.UUID            `json:"project_id"`
	BaselineRevision  uint64               `json:"baseline_revision"`
	WorkspaceRevision uint64               `json:"workspace_revision"`
	Status            string               `json:"status"`
	Criteria          []string             `json:"criteria"`
	ValidationPlan    []string             `json:"validation_plan"`
	Files             []PatchFileResult    `json:"files"`
	AppliedAt         time.Time            `json:"applied_at"`
	RollbackAvailable bool                 `json:"rollback_available"`
	Classification    PolicyClassification `json:"classification"`
	RequiresApproval  bool                 `json:"requires_approval"`
}

type PatchConflict struct {
	Path     string `json:"path"`
	Expected string `json:"expected_sha256"`
	Actual   string `json:"actual_sha256"`
}

func (conflict PatchConflict) Error() string {
	return fmt.Sprintf("%v: %s expected %s, found %s", ErrStalePreimage, conflict.Path, conflict.Expected, conflict.Actual)
}

func (PatchConflict) Unwrap() error { return ErrStalePreimage }
