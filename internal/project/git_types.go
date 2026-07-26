package project

import (
	"time"

	"github.com/google/uuid"
)

const GitContractVersion = "ion.project-git.v1"

type GitStatusEntry struct {
	Path         string `json:"path"`
	OriginalPath string `json:"original_path,omitempty"`
	IndexStatus  string `json:"index_status"`
	WorkStatus   string `json:"work_status"`
	Untracked    bool   `json:"untracked,omitempty"`
	Ignored      bool   `json:"ignored,omitempty"`
}

type GitCommit struct {
	Hash       string    `json:"hash"`
	Parents    []string  `json:"parents"`
	Author     string    `json:"author"`
	AuthoredAt time.Time `json:"authored_at"`
	Subject    string    `json:"subject"`
}

type GitRemote struct {
	Name     string `json:"name"`
	FetchURL string `json:"fetch_url,omitempty"`
	PushURL  string `json:"push_url,omitempty"`
}

type GitBranch struct {
	Name     string `json:"name"`
	Commit   string `json:"commit"`
	Upstream string `json:"upstream,omitempty"`
	Current  bool   `json:"current,omitempty"`
}

type GitBaseline struct {
	Version           string    `json:"version"`
	ProjectID         uuid.UUID `json:"project_id"`
	WorkspaceRevision uint64    `json:"workspace_revision"`
	RepositoryRoot    string    `json:"repository_root"`
	Head              string    `json:"head,omitempty"`
	Branch            string    `json:"branch,omitempty"`
	StatusSHA256      string    `json:"status_sha256"`
	CapturedAt        time.Time `json:"captured_at"`
}

type GitProjection struct {
	Version           string           `json:"version"`
	ProjectID         uuid.UUID        `json:"project_id"`
	WorkspaceRevision uint64           `json:"workspace_revision"`
	RepositoryRoot    string           `json:"repository_root"`
	Head              string           `json:"head,omitempty"`
	Branch            string           `json:"branch,omitempty"`
	Detached          bool             `json:"detached"`
	Status            []GitStatusEntry `json:"status"`
	Branches          []GitBranch      `json:"branches"`
	Remotes           []GitRemote      `json:"remotes"`
	History           []GitCommit      `json:"history"`
	StagedDiff        string           `json:"staged_diff,omitempty"`
	UnstagedDiff      string           `json:"unstaged_diff,omitempty"`
	Baseline          GitBaseline      `json:"baseline"`
	Truncated         bool             `json:"truncated"`
}

type GitBlameRequest struct {
	ProjectID uuid.UUID `json:"project_id"`
	Path      string    `json:"path"`
	StartLine int       `json:"start_line,omitempty"`
	EndLine   int       `json:"end_line,omitempty"`
}

type GitBlameLine struct {
	Line       int       `json:"line"`
	Commit     string    `json:"commit"`
	Author     string    `json:"author"`
	AuthoredAt time.Time `json:"authored_at,omitempty"`
	Text       string    `json:"text"`
}

type GitPreviewRequest struct {
	ProjectID uuid.UUID `json:"project_id"`
	Revision  string    `json:"revision"`
}

type GitPreviewCloseRequest struct {
	ProjectID uuid.UUID `json:"project_id"`
	PreviewID uuid.UUID `json:"preview_id"`
}

type GitPreview struct {
	Version        string     `json:"version"`
	ID             uuid.UUID  `json:"id"`
	ActorID        uuid.UUID  `json:"actor_id"`
	ProjectID      uuid.UUID  `json:"project_id"`
	RepositoryRoot string     `json:"repository_root"`
	Path           string     `json:"path"`
	Revision       string     `json:"revision"`
	OriginalHead   string     `json:"original_head"`
	OriginalBranch string     `json:"original_branch,omitempty"`
	State          string     `json:"state"`
	CreatedAt      time.Time  `json:"created_at"`
	ClosedAt       *time.Time `json:"closed_at,omitempty"`
}

type GitPathExpectation struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type GitBranchCreateRequest struct {
	ProjectID         uuid.UUID `json:"project_id"`
	WorkspaceRevision uint64    `json:"workspace_revision"`
	Name              string    `json:"name"`
	ExpectedHead      string    `json:"expected_head"`
}

type GitStageRequest struct {
	ProjectID         uuid.UUID            `json:"project_id"`
	WorkspaceRevision uint64               `json:"workspace_revision"`
	ExpectedHead      string               `json:"expected_head"`
	Paths             []GitPathExpectation `json:"paths"`
}

// GitStageHunksRequest stages only a reviewed unified patch. DiffSHA256 binds
// the request to the complete current unstaged diff for Paths, while Patch is
// the exact selected subset applied to the index.
type GitStageHunksRequest struct {
	ProjectID         uuid.UUID            `json:"project_id"`
	WorkspaceRevision uint64               `json:"workspace_revision"`
	ExpectedHead      string               `json:"expected_head"`
	DiffSHA256        string               `json:"diff_sha256"`
	Patch             string               `json:"patch"`
	Paths             []GitPathExpectation `json:"paths"`
}

type GitDiffRequest struct {
	ProjectID uuid.UUID `json:"project_id"`
	Paths     []string  `json:"paths"`
}

type GitDiffSelection struct {
	ProjectID uuid.UUID `json:"project_id"`
	Head      string    `json:"head"`
	Patch     string    `json:"patch"`
	SHA256    string    `json:"sha256"`
	Truncated bool      `json:"truncated"`
}

type GitCommitRequest struct {
	ProjectID         uuid.UUID            `json:"project_id"`
	WorkspaceRevision uint64               `json:"workspace_revision"`
	ExpectedHead      string               `json:"expected_head"`
	Message           string               `json:"message"`
	AuthorName        string               `json:"author_name"`
	AuthorEmail       string               `json:"author_email"`
	Paths             []GitPathExpectation `json:"paths"`
}

type GitTagRequest struct {
	ProjectID         uuid.UUID `json:"project_id"`
	WorkspaceRevision uint64    `json:"workspace_revision"`
	ExpectedHead      string    `json:"expected_head"`
	Name              string    `json:"name"`
	Message           string    `json:"message"`
	AuthorName        string    `json:"author_name"`
	AuthorEmail       string    `json:"author_email"`
}

type GitCheckpointRequest = GitCommitRequest

type GitSyncRequest struct {
	ProjectID           uuid.UUID   `json:"project_id"`
	WorkspaceRevision   uint64      `json:"workspace_revision"`
	ExpectedHead        string      `json:"expected_head"`
	Remote              string      `json:"remote"`
	Provider            string      `json:"provider"`
	CredentialReference string      `json:"credential_reference,omitempty"`
	SecretGrant         SecretGrant `json:"secret_grant,omitempty"`
	PermissionGrant     string      `json:"permission_grant,omitempty"`
}

type GitMergeRequest struct {
	ProjectID         uuid.UUID `json:"project_id"`
	WorkspaceRevision uint64    `json:"workspace_revision"`
	ExpectedHead      string    `json:"expected_head"`
	Revision          string    `json:"revision"`
	Message           string    `json:"message,omitempty"`
}

type GitPullRequest struct {
	ProjectID           uuid.UUID   `json:"project_id"`
	WorkspaceRevision   uint64      `json:"workspace_revision"`
	ExpectedHead        string      `json:"expected_head"`
	Remote              string      `json:"remote"`
	Provider            string      `json:"provider"`
	Branch              string      `json:"branch"`
	CredentialReference string      `json:"credential_reference,omitempty"`
	SecretGrant         SecretGrant `json:"secret_grant,omitempty"`
	PermissionGrant     string      `json:"permission_grant,omitempty"`
}

type GitPushRequest struct {
	ProjectID           uuid.UUID   `json:"project_id"`
	WorkspaceRevision   uint64      `json:"workspace_revision"`
	ExpectedHead        string      `json:"expected_head"`
	Remote              string      `json:"remote"`
	Provider            string      `json:"provider"`
	SourceRevision      string      `json:"source_revision"`
	TargetBranch        string      `json:"target_branch"`
	ExpectedRemoteHead  string      `json:"expected_remote_head,omitempty"`
	CredentialReference string      `json:"credential_reference,omitempty"`
	SecretGrant         SecretGrant `json:"secret_grant,omitempty"`
	PermissionGrant     string      `json:"permission_grant"`
	IdempotencyKey      string      `json:"idempotency_key"`
}

type GitRemoteReceipt struct {
	Version           string               `json:"version"`
	ProjectID         uuid.UUID            `json:"project_id"`
	Operation         string               `json:"operation"`
	Remote            string               `json:"remote"`
	Target            string               `json:"target,omitempty"`
	BeforeHead        string               `json:"before_head,omitempty"`
	AfterHead         string               `json:"after_head,omitempty"`
	WorkspaceRevision uint64               `json:"workspace_revision"`
	Classification    PolicyClassification `json:"classification"`
	Reconciled        bool                 `json:"reconciled,omitempty"`
	AppliedAt         time.Time            `json:"applied_at"`
}

type GitMutationReceipt struct {
	Version           string               `json:"version"`
	ProjectID         uuid.UUID            `json:"project_id"`
	Operation         string               `json:"operation"`
	BeforeHead        string               `json:"before_head"`
	AfterHead         string               `json:"after_head"`
	WorkspaceRevision uint64               `json:"workspace_revision"`
	Paths             []GitPathExpectation `json:"paths,omitempty"`
	Classification    PolicyClassification `json:"classification"`
	AppliedAt         time.Time            `json:"applied_at"`
}

type GitRestorePlanRequest struct {
	ProjectID         uuid.UUID `json:"project_id"`
	WorkspaceRevision uint64    `json:"workspace_revision"`
	Revision          string    `json:"revision"`
	Paths             []string  `json:"paths,omitempty"`
}
