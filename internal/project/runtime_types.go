package project

import (
	"time"

	"github.com/google/uuid"
)

const RuntimeContractVersion = "ion.project-runtime.v1"

type RuntimeCommand struct {
	Kind        string   `json:"kind"`
	Argv        []string `json:"argv"`
	Description string   `json:"description"`
	Network     bool     `json:"network,omitempty"`
}

type RuntimePlan struct {
	Version           string           `json:"version"`
	ProjectID         uuid.UUID        `json:"project_id"`
	WorkspaceRevision uint64           `json:"workspace_revision"`
	Stack             string           `json:"stack"`
	WorkingDirectory  string           `json:"working_directory"`
	Commands          []RuntimeCommand `json:"commands"`
	DefaultService    string           `json:"default_service"`
	ReadinessPath     string           `json:"readiness_path"`
	Inferred          bool             `json:"inferred"`
	Warnings          []string         `json:"warnings,omitempty"`
}

type RuntimeStartRequest struct {
	ProjectID         uuid.UUID `json:"project_id"`
	WorkspaceRevision uint64    `json:"workspace_revision"`
	Name              string    `json:"name"`
	CommandKind       string    `json:"command_kind"`
	Argv              []string  `json:"argv,omitempty"`
	WorkingDirectory  string    `json:"working_directory,omitempty"`
	ReadinessPath     string    `json:"readiness_path,omitempty"`
	ReadinessSeconds  int       `json:"readiness_seconds,omitempty"`
}

type RuntimePhaseRequest struct {
	ProjectID         uuid.UUID `json:"project_id"`
	WorkspaceRevision uint64    `json:"workspace_revision"`
	Kind              string    `json:"kind"`
	Argv              []string  `json:"argv,omitempty"`
	WorkingDirectory  string    `json:"working_directory,omitempty"`
	TimeoutSeconds    int       `json:"timeout_seconds,omitempty"`
}

type RuntimeControlRequest struct {
	ProjectID uuid.UUID `json:"project_id"`
	Name      string    `json:"name"`
}

type RuntimeDiagnostic struct {
	ID             string    `json:"id"`
	Source         string    `json:"source"`
	Severity       string    `json:"severity"`
	Code           string    `json:"code,omitempty"`
	Message        string    `json:"message"`
	Path           string    `json:"path,omitempty"`
	Line           int       `json:"line,omitempty"`
	Column         int       `json:"column,omitempty"`
	Signature      string    `json:"signature"`
	Recurrence     int       `json:"recurrence"`
	CausalEvidence []string  `json:"causal_evidence,omitempty"`
	FirstSeen      time.Time `json:"first_seen"`
	LastSeen       time.Time `json:"last_seen"`
}

type RuntimeState struct {
	Version           string                 `json:"version"`
	ID                uuid.UUID              `json:"id"`
	ActorID           uuid.UUID              `json:"actor_id"`
	ProjectID         uuid.UUID              `json:"project_id"`
	WorkspaceRevision uint64                 `json:"workspace_revision"`
	Name              string                 `json:"name"`
	CommandKind       string                 `json:"command_kind"`
	Argv              []string               `json:"argv"`
	CommandTemplate   []string               `json:"command_template"`
	WorkingDirectory  string                 `json:"working_directory"`
	Host              string                 `json:"host"`
	Port              uint16                 `json:"port"`
	PreviewURL        string                 `json:"preview_url"`
	Origin            string                 `json:"origin"`
	ReadinessPath     string                 `json:"readiness_path"`
	Status            string                 `json:"status"`
	NextAction        string                 `json:"next_action"`
	PID               int                    `json:"pid,omitempty"`
	ProcessStartToken string                 `json:"process_start_token,omitempty"`
	Reloads           int                    `json:"reloads"`
	Restarts          int                    `json:"restarts"`
	Logs              string                 `json:"logs,omitempty"`
	LogsTruncated     bool                   `json:"logs_truncated"`
	Diagnostics       []RuntimeDiagnostic    `json:"diagnostics,omitempty"`
	Screenshots       []RuntimeScreenshot    `json:"screenshots,omitempty"`
	Annotations       []RuntimeAnnotation    `json:"annotations,omitempty"`
	StyleProposals    []RuntimeStyleProposal `json:"style_proposals,omitempty"`
	StartedAt         time.Time              `json:"started_at"`
	ReadyAt           *time.Time             `json:"ready_at,omitempty"`
	StoppedAt         *time.Time             `json:"stopped_at,omitempty"`
	ExitCode          *int                   `json:"exit_code,omitempty"`
	LastError         string                 `json:"last_error,omitempty"`
}

type RuntimeInspectRequest struct {
	ProjectID uuid.UUID `json:"project_id"`
	Name      string    `json:"name"`
	Width     int64     `json:"width"`
	Height    int64     `json:"height"`
	DarkMode  bool      `json:"dark_mode"`
}

type RuntimeInspectionElement struct {
	Ref         string `json:"ref"`
	Tag         string `json:"tag"`
	Type        string `json:"type,omitempty"`
	Text        string `json:"text,omitempty"`
	Name        string `json:"name,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
	Disabled    bool   `json:"disabled,omitempty"`
}

type RuntimeAccessibilityFinding struct {
	Ref     string `json:"ref"`
	Rule    string `json:"rule"`
	Message string `json:"message"`
}

type RuntimeInspection struct {
	URL              string                        `json:"url"`
	Title            string                        `json:"title"`
	Text             string                        `json:"text"`
	Elements         []RuntimeInspectionElement    `json:"elements"`
	Accessibility    []RuntimeAccessibilityFinding `json:"accessibility,omitempty"`
	ScreenshotPNG    string                        `json:"screenshot_png"`
	ScreenshotSHA256 string                        `json:"screenshot_sha256"`
	Width            int64                         `json:"width"`
	Height           int64                         `json:"height"`
	DarkMode         bool                          `json:"dark_mode"`
	CapturedAt       time.Time                     `json:"captured_at"`
}

type RuntimeBrowserSnapshot struct {
	URL           string
	Title         string
	Text          string
	Elements      []RuntimeInspectionElement
	Accessibility []RuntimeAccessibilityFinding
	ScreenshotPNG string
	Diagnostics   []RuntimeBrowserReport
	Width         int64
	Height        int64
	DarkMode      bool
}

type RuntimeScreenshot struct {
	ID        uuid.UUID `json:"id"`
	SHA256    string    `json:"sha256"`
	Path      string    `json:"path"`
	Width     int64     `json:"width"`
	Height    int64     `json:"height"`
	DarkMode  bool      `json:"dark_mode"`
	CreatedAt time.Time `json:"created_at"`
}

type RuntimeAnnotation struct {
	ID         uuid.UUID `json:"id"`
	ElementRef string    `json:"element_ref,omitempty"`
	X          float64   `json:"x,omitempty"`
	Y          float64   `json:"y,omitempty"`
	Body       string    `json:"body"`
	CreatedAt  time.Time `json:"created_at"`
}

type RuntimeAnnotationRequest struct {
	ProjectID  uuid.UUID `json:"project_id"`
	Name       string    `json:"name"`
	ElementRef string    `json:"element_ref,omitempty"`
	X          float64   `json:"x,omitempty"`
	Y          float64   `json:"y,omitempty"`
	Body       string    `json:"body"`
}

type RuntimeStyleProposal struct {
	ID                uuid.UUID         `json:"id"`
	WorkspaceRevision uint64            `json:"workspace_revision"`
	ElementRef        string            `json:"element_ref"`
	Path              string            `json:"path"`
	ExpectedSHA256    string            `json:"expected_sha256"`
	Declarations      map[string]string `json:"declarations"`
	Status            string            `json:"status"`
	CreatedAt         time.Time         `json:"created_at"`
}

type RuntimeStyleProposalRequest struct {
	ProjectID      uuid.UUID         `json:"project_id"`
	Name           string            `json:"name"`
	ElementRef     string            `json:"element_ref"`
	Path           string            `json:"path"`
	ExpectedSHA256 string            `json:"expected_sha256"`
	Declarations   map[string]string `json:"declarations"`
}

type RuntimeBrowserReport struct {
	ProjectID      uuid.UUID `json:"project_id"`
	Name           string    `json:"name"`
	Source         string    `json:"source"`
	Severity       string    `json:"severity"`
	Code           string    `json:"code,omitempty"`
	Message        string    `json:"message"`
	Path           string    `json:"path,omitempty"`
	Line           int       `json:"line,omitempty"`
	Column         int       `json:"column,omitempty"`
	CausalEvidence []string  `json:"causal_evidence,omitempty"`
}

type RuntimeEvent struct {
	Phase string       `json:"phase"`
	State RuntimeState `json:"state"`
}
