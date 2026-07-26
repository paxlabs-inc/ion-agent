package work

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type localSupervisorReconciler struct {
	workspace string
}

func (reconciler *localSupervisorReconciler) Reconcile(
	ctx context.Context,
	_ SupervisorRun,
	task SpecialistTask,
	attempt Attempt,
) (Reconciliation, bool, error) {
	if reconciler == nil || strings.TrimSpace(reconciler.workspace) == "" {
		return Reconciliation{}, false, fmt.Errorf("workspace is unavailable")
	}
	root, err := resolveWorkspaceRoot(reconciler.workspace)
	if err != nil {
		return Reconciliation{}, false, err
	}
	checked := 0
	scopes := append(
		append([]string(nil), task.Packet.Scope.ReadFiles...),
		task.Packet.Scope.WriteFiles...,
	)
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if scope == "" || scope == "workspace" {
			scope = "."
		}
		path := filepath.Join(root, filepath.FromSlash(scope))
		resolved, resolveErr := filepath.EvalSymlinks(path)
		if resolveErr != nil {
			if os.IsNotExist(resolveErr) {
				continue
			}
			return Reconciliation{}, false, resolveErr
		}
		relative, relativeErr := filepath.Rel(root, resolved)
		if relativeErr != nil || relative == ".." ||
			strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return Reconciliation{}, false, fmt.Errorf("scope escapes workspace")
		}
		if _, statErr := os.Stat(resolved); statErr != nil {
			return Reconciliation{}, false, statErr
		}
		checked++
	}
	gitState := "not_a_git_repository"
	command := exec.CommandContext(
		ctx, "git", "-C", root, "status", "--porcelain=v1", "--untracked-files=all",
	)
	output, commandErr := command.CombinedOutput()
	switch {
	case commandErr == nil && len(output) == 0:
		gitState = "inspected_clean"
	case commandErr == nil:
		gitState = "inspected_with_changes"
	case strings.Contains(string(output), "not a git repository"):
	default:
		return Reconciliation{}, false, commandErr
	}
	processState := "no_recorded_processes"
	if len(attempt.ProcessIDs) > 0 {
		running := 0
		for _, pid := range attempt.ProcessIDs {
			if pid <= 0 {
				continue
			}
			if _, statErr := os.Stat(
				filepath.Join("/proc", strconv.Itoa(pid)),
			); statErr == nil {
				running++
			} else if !os.IsNotExist(statErr) {
				return Reconciliation{}, false, statErr
			}
		}
		processState = fmt.Sprintf(
			"inspected_%d_running_of_%d", running, len(attempt.ProcessIDs),
		)
	}
	uncertain := hasUncertainEffect(attempt.ExternalEffects)
	providerState := "no_recorded_provider_effects"
	effectState := "none_recorded"
	if len(attempt.ExternalEffects) > 0 {
		providerState = "provider_receipt_required"
		effectState = "stopped_pending_idempotency_reconciliation"
	}
	return Reconciliation{
		FileSystem:      fmt.Sprintf("inspected_%d_scopes", checked),
		Git:             gitState,
		Processes:       processState,
		Provider:        providerState,
		EventHistory:    "durable_attempt_loaded",
		ExternalEffects: effectState,
	}, uncertain, nil
}

func mergeReconciliation(
	aggregate Reconciliation, inspected Reconciliation,
) Reconciliation {
	join := func(left, right string) string {
		if right == "" {
			return left
		}
		if left == "" || left == right {
			return right
		}
		return left + "; " + right
	}
	aggregate.FileSystem = join(aggregate.FileSystem, inspected.FileSystem)
	aggregate.Git = join(aggregate.Git, inspected.Git)
	aggregate.Processes = join(aggregate.Processes, inspected.Processes)
	aggregate.Provider = join(aggregate.Provider, inspected.Provider)
	aggregate.EventHistory = join(aggregate.EventHistory, inspected.EventHistory)
	aggregate.ExternalEffects = join(
		aggregate.ExternalEffects, inspected.ExternalEffects,
	)
	return aggregate
}

var _ SupervisorReconciler = (*localSupervisorReconciler)(nil)
