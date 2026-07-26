package project

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/controllease"
)

const TerminalVersion = "ion.project-terminal.v1"

var ErrTerminalNotFound = errors.New("project: terminal session not found")

type ProcessMode string

const (
	ProcessOneShot ProcessMode = "one_shot"
	ProcessPTY     ProcessMode = "pty"
)

type ProcessRequest struct {
	ProjectID         uuid.UUID   `json:"project_id"`
	WorkspaceRevision uint64      `json:"workspace_revision"`
	Mode              ProcessMode `json:"mode"`
	Argv              []string    `json:"argv"`
	WorkingDirectory  string      `json:"working_directory,omitempty"`
	Environment       []string    `json:"environment,omitempty"`
	TimeoutSeconds    int         `json:"timeout_seconds,omitempty"`
	OutputBytes       int         `json:"output_bytes,omitempty"`
	Columns           uint16      `json:"columns,omitempty"`
	Rows              uint16      `json:"rows,omitempty"`
}

type TerminalState struct {
	Version           string             `json:"version"`
	ID                uuid.UUID          `json:"id"`
	ActorID           uuid.UUID          `json:"actor_id"`
	ProjectID         uuid.UUID          `json:"project_id"`
	WorkspaceRevision uint64             `json:"workspace_revision"`
	Mode              ProcessMode        `json:"mode"`
	Argv              []string           `json:"argv"`
	WorkingDirectory  string             `json:"working_directory"`
	PID               int                `json:"pid,omitempty"`
	ProcessStartToken string             `json:"process_start_token,omitempty"`
	Status            string             `json:"status"`
	ExitCode          *int               `json:"exit_code,omitempty"`
	StartedAt         time.Time          `json:"started_at"`
	FinishedAt        *time.Time         `json:"finished_at,omitempty"`
	OutputCursor      uint64             `json:"output_cursor"`
	DroppedBytes      uint64             `json:"dropped_bytes"`
	Truncated         bool               `json:"truncated"`
	TimedOut          bool               `json:"timed_out"`
	ControlSessionID  *uuid.UUID         `json:"control_session_id,omitempty"`
	ControlOwner      controllease.Owner `json:"control_owner"`
}

type TerminalReplay struct {
	State      TerminalState `json:"state"`
	FromCursor uint64        `json:"from_cursor"`
	NextCursor uint64        `json:"next_cursor"`
	Output     string        `json:"output"`
	Gap        bool          `json:"gap"`
}
