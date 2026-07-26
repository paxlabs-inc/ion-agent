package project

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

const IntelligenceVersion = "ion.project-intelligence.v1"

var (
	ErrIndexNotFound = errors.New("project: intelligence index not found")
	ErrStaleIndex    = errors.New("project: intelligence index is stale")
	ErrStaleCitation = errors.New("project: citation is stale")
	ErrProtectedPath = errors.New("project: path is protected from model context")
)

type ContentClass string

const (
	ContentSource     ContentClass = "source"
	ContentGenerated  ContentClass = "generated"
	ContentVendor     ContentClass = "vendor"
	ContentBinary     ContentClass = "binary"
	ContentOversized  ContentClass = "oversized"
	ContentProtected  ContentClass = "protected"
	ContentIgnored    ContentClass = "ignored"
	ContentUnreadable ContentClass = "unreadable"
)

type SearchKind string

const (
	SearchLexical    SearchKind = "lexical"
	SearchRegex      SearchKind = "regex"
	SearchFilename   SearchKind = "filename"
	SearchSymbol     SearchKind = "symbol"
	SearchReference  SearchKind = "reference"
	SearchDependency SearchKind = "dependency"
	SearchDiagnostic SearchKind = "diagnostic"
	SearchSemantic   SearchKind = "semantic"
)

type Symbol struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Path      string `json:"path"`
	LineStart int    `json:"line_start"`
	LineEnd   int    `json:"line_end"`
	Exported  bool   `json:"exported"`
}

type Reference struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Line int    `json:"line"`
}

type Dependency struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Kind     string `json:"kind"`
	Line     int    `json:"line"`
	External bool   `json:"external"`
}

type EntryPoint struct {
	Path    string `json:"path"`
	Line    int    `json:"line,omitempty"`
	Kind    string `json:"kind"`
	Command string `json:"command,omitempty"`
}

type OwnershipRule struct {
	Pattern string   `json:"pattern"`
	Owners  []string `json:"owners"`
	Source  string   `json:"source"`
	Line    int      `json:"line"`
}

type ConfigurationRecord struct {
	Path   string   `json:"path"`
	Kind   string   `json:"kind"`
	Scope  string   `json:"scope"`
	SHA256 string   `json:"sha256"`
	Owners []string `json:"owners,omitempty"`
}

type GeneratedOwnership struct {
	Path      string   `json:"path"`
	Generator string   `json:"generator"`
	Owners    []string `json:"owners,omitempty"`
}

type Instruction struct {
	Path                 string `json:"path"`
	Scope                string `json:"scope"`
	SHA256               string `json:"sha256"`
	Precedence           int    `json:"precedence"`
	Content              string `json:"content"`
	Truncated            bool   `json:"truncated"`
	RepositoryData       bool   `json:"repository_data"`
	CannotOverrideSafety bool   `json:"cannot_override_safety"`
}

type SecretFinding struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Kind string `json:"kind"`
}

type FileRecord struct {
	Path           string          `json:"path"`
	SHA256         string          `json:"sha256,omitempty"`
	Size           int64           `json:"size"`
	ModifiedUnixNS int64           `json:"modified_unix_ns,omitempty"`
	Class          ContentClass    `json:"class"`
	Reason         string          `json:"reason,omitempty"`
	Language       string          `json:"language,omitempty"`
	Frameworks     []string        `json:"frameworks,omitempty"`
	Symbols        []Symbol        `json:"symbols,omitempty"`
	References     []Reference     `json:"references,omitempty"`
	Dependencies   []Dependency    `json:"dependencies,omitempty"`
	EntryPoints    []EntryPoint    `json:"entry_points,omitempty"`
	Secrets        []SecretFinding `json:"secret_findings,omitempty"`
}

type Diagnostic struct {
	Path       string    `json:"path"`
	Line       int       `json:"line"`
	Column     int       `json:"column,omitempty"`
	Severity   string    `json:"severity"`
	Code       string    `json:"code,omitempty"`
	Message    string    `json:"message"`
	Source     string    `json:"source"`
	Revision   uint64    `json:"workspace_revision"`
	RecordedAt time.Time `json:"recorded_at"`
}

type HistoryEntry struct {
	Commit    string    `json:"commit"`
	Author    string    `json:"author"`
	Timestamp time.Time `json:"timestamp"`
	Subject   string    `json:"subject"`
	Paths     []string  `json:"paths"`
}

type Rename struct {
	From   string `json:"from"`
	To     string `json:"to"`
	SHA256 string `json:"sha256"`
}

type RefreshStats struct {
	Added       int      `json:"added"`
	Updated     int      `json:"updated"`
	Reused      int      `json:"reused"`
	Deleted     int      `json:"deleted"`
	Renamed     []Rename `json:"renamed"`
	FilesSeen   int      `json:"files_seen"`
	BytesHashed int64    `json:"bytes_hashed"`
	DurationMS  int64    `json:"duration_ms"`
}

type ProjectIndex struct {
	Version            string                `json:"version"`
	ActorID            uuid.UUID             `json:"actor_id"`
	ProjectID          uuid.UUID             `json:"project_id"`
	WorkspaceRevision  uint64                `json:"workspace_revision"`
	IndexRevision      uint64                `json:"index_revision"`
	RootDigest         string                `json:"root_digest"`
	DeniedPaths        []string              `json:"denied_paths"`
	Files              []FileRecord          `json:"files"`
	Languages          []string              `json:"languages"`
	Frameworks         []string              `json:"frameworks"`
	EntryPoints        []EntryPoint          `json:"entry_points"`
	Ownership          []OwnershipRule       `json:"ownership"`
	Configuration      []ConfigurationRecord `json:"configuration"`
	GeneratedOwnership []GeneratedOwnership  `json:"generated_ownership"`
	Instructions       []Instruction         `json:"instructions"`
	History            []HistoryEntry        `json:"history"`
	Diagnostics        []Diagnostic          `json:"diagnostics"`
	Omissions          []Omission            `json:"omissions"`
	Stats              RefreshStats          `json:"refresh_stats"`
	Truncated          bool                  `json:"truncated"`
	UpdatedAt          time.Time             `json:"updated_at"`
}

type RefreshInput struct {
	ProjectID         uuid.UUID    `json:"project_id"`
	WorkspaceRevision uint64       `json:"workspace_revision"`
	Diagnostics       []Diagnostic `json:"diagnostics,omitempty"`
	DeniedPaths       []string     `json:"denied_paths,omitempty"`
}

type Citation struct {
	ProjectID         uuid.UUID `json:"project_id"`
	WorkspaceRevision uint64    `json:"workspace_revision"`
	IndexRevision     uint64    `json:"index_revision"`
	Path              string    `json:"path"`
	SHA256            string    `json:"sha256"`
	LineStart         int       `json:"line_start,omitempty"`
	LineEnd           int       `json:"line_end,omitempty"`
	Symbol            string    `json:"symbol,omitempty"`
	Source            string    `json:"source"`
}

type SearchRequest struct {
	ProjectID             uuid.UUID  `json:"project_id"`
	WorkspaceRevision     uint64     `json:"workspace_revision"`
	ExpectedIndexRevision uint64     `json:"expected_index_revision,omitempty"`
	Kind                  SearchKind `json:"kind"`
	Query                 string     `json:"query"`
	PathPrefix            string     `json:"path_prefix,omitempty"`
	Limit                 int        `json:"limit,omitempty"`
	MaxResultBytes        int        `json:"max_result_bytes,omitempty"`
}

type SearchMatch struct {
	Kind      SearchKind `json:"kind"`
	Score     float64    `json:"score"`
	Path      string     `json:"path"`
	LineStart int        `json:"line_start,omitempty"`
	LineEnd   int        `json:"line_end,omitempty"`
	Symbol    string     `json:"symbol,omitempty"`
	Snippet   string     `json:"snippet"`
	Citation  Citation   `json:"citation"`
}

type SearchResponse struct {
	ProjectID         uuid.UUID     `json:"project_id"`
	WorkspaceRevision uint64        `json:"workspace_revision"`
	IndexRevision     uint64        `json:"index_revision"`
	Matches           []SearchMatch `json:"matches"`
	Omissions         []Omission    `json:"omissions"`
	Truncated         bool          `json:"truncated"`
	ResultBytes       int           `json:"result_bytes"`
}

type Omission struct {
	Path   string `json:"path,omitempty"`
	Class  string `json:"class"`
	Reason string `json:"reason"`
	Count  int    `json:"count,omitempty"`
}

type ContextSource struct {
	Kind     string   `json:"kind"`
	Title    string   `json:"title"`
	Content  string   `json:"content"`
	Citation Citation `json:"citation"`
	Verified bool     `json:"verified"`
	Priority int      `json:"priority"`
}

type ContextPlanRequest struct {
	ProjectID             uuid.UUID       `json:"project_id"`
	WorkspaceRevision     uint64          `json:"workspace_revision"`
	ExpectedIndexRevision uint64          `json:"expected_index_revision,omitempty"`
	Task                  string          `json:"task"`
	PathScope             string          `json:"path_scope,omitempty"`
	MaxBytes              int             `json:"max_bytes,omitempty"`
	Mismatch              string          `json:"mismatch,omitempty"`
	Expand                []string        `json:"expand,omitempty"`
	Sources               []ContextSource `json:"-"`
}

type ContextItem struct {
	Kind     string   `json:"kind"`
	Title    string   `json:"title"`
	Content  string   `json:"content"`
	Citation Citation `json:"citation"`
	Verified bool     `json:"verified"`
	Bytes    int      `json:"bytes"`
	Priority int      `json:"priority"`
}

type InstructionResolution struct {
	TargetPath          string        `json:"target_path"`
	Instructions        []Instruction `json:"instructions"`
	Precedence          string        `json:"precedence"`
	ImmutableSafetyWins bool          `json:"immutable_safety_wins"`
	UserAuthorityWins   bool          `json:"user_authority_wins"`
}

type ContextPack struct {
	Version             string                `json:"version"`
	ProjectID           uuid.UUID             `json:"project_id"`
	WorkspaceRevision   uint64                `json:"workspace_revision"`
	IndexRevision       uint64                `json:"index_revision"`
	Task                string                `json:"task"`
	Items               []ContextItem         `json:"items"`
	Instructions        InstructionResolution `json:"instruction_resolution"`
	Omissions           []Omission            `json:"omissions"`
	Truncated           bool                  `json:"truncated"`
	Bytes               int                   `json:"bytes"`
	ExpandedForMismatch bool                  `json:"expanded_for_mismatch"`
	CreatedAt           time.Time             `json:"created_at"`
}
