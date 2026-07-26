// Package dayzero provides an auditable gate over the 30 binding controls.
package dayzero

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// ID is a closed Day Zero checklist identifier.
type ID string

// Definition is one binding control imported from the security bible.
type Definition struct {
	ID          ID
	Description string
}

// ControlCount is the number of binding Day Zero controls.
const ControlCount = 30

// checklist is intentionally private so callers cannot rewrite the gate's
// binding control set at runtime.
var checklist = [ControlCount]Definition{
	{"DZ-01", "AES-256-GCM envelope encryption for every storage backend"},
	{"DZ-02", "KEK sourced from an OS keychain or HSM"},
	{"DZ-03", "User Key derivation with HKDF-SHA256 and per-user salt"},
	{"DZ-04", "Per-object CSPRNG DEKs with no reuse"},
	{"DZ-05", "Atomic KEK, User Key, and DEK rotation"},
	{"DZ-06", "BLAKE3 MMR and byte-deterministic replay"},
	{"DZ-07", "Per-namespace SMT membership and non-membership proofs"},
	{"DZ-08", "EIP-712 secp256k1 receipt signing"},
	{"DZ-09", "Every tool call intercepted by the five-layer policy pipeline"},
	{"DZ-10", "GREEN YELLOW RED enforcement and RED approval"},
	{"DZ-11", "SSRF blocking for private addresses and non-TLS endpoints"},
	{"DZ-12", "Sandboxed file reads and writes"},
	{"DZ-13", "Cross-session isolation"},
	{"DZ-14", "Cassandra append-only dual record"},
	{"DZ-15", "Cassandra edit rate limits"},
	{"DZ-16", "Emotional state cannot influence safety"},
	{"DZ-17", "Immutable sub-agent tool surface"},
	{"DZ-18", "Sub-agent spawn depth capped at two"},
	{"DZ-19", "Closed-verb HMAC authentication"},
	{"DZ-20", "Sub-agents cannot inherit vault keys"},
	{"DZ-21", "Idle-time three non-negotiables"},
	{"DZ-22", "Replayable trajectory export"},
	{"DZ-23", "Cron prompt-injection scanning"},
	{"DZ-24", "Idle timeout and tool-loop detection"},
	{"DZ-25", "ErrIncomplete honest partials"},
	{"DZ-26", "Emergency emotional-state reset"},
	{"DZ-27", "Approval for Constraint and Identity changes"},
	{"DZ-28", "MMR-bound premise citation verification"},
	{"DZ-29", "Cortex honeypot canaries"},
	{"DZ-30", "Checksummed and audited dependencies"},
}

// Definitions returns the canonical DZ-01 through DZ-30 order.
func Definitions() []Definition {
	return append([]Definition(nil), checklist[:]...)
}

// Check must return concrete evidence on success. An empty evidence string is
// rejected, preventing a boolean-only or stubbed check from opening the gate.
type Check func(context.Context) (evidence string, err error)

// Status is one control's readiness state.
type Status string

const (
	Passed  Status = "passed"
	Failed  Status = "failed"
	Missing Status = "missing"
)

// Result is one auditable checklist result.
type Result struct {
	Definition Definition
	Status     Status
	Evidence   string
	Error      string
}

// Report is a complete 30-control snapshot.
type Report struct {
	Results []Result
}

// Passed is true only when all 30 controls produced concrete evidence.
func (report Report) Passed() bool {
	if len(report.Results) != len(checklist) {
		return false
	}
	for index, result := range report.Results {
		if result.Definition != checklist[index] ||
			result.Status != Passed ||
			strings.TrimSpace(result.Evidence) == "" {
			return false
		}
	}
	return true
}

// Unready returns sorted identifiers that did not pass.
func (report Report) Unready() []ID {
	var identifiers []ID
	for _, result := range report.Results {
		if result.Status != Passed {
			identifiers = append(identifiers, result.Definition.ID)
		}
	}
	sort.Slice(identifiers, func(left, right int) bool {
		return identifiers[left] < identifiers[right]
	})
	return identifiers
}

// Gate owns explicitly registered automated checks.
type Gate struct {
	checks map[ID]Check
}

// New validates check identifiers and rejects nil test placeholders.
func New(checks map[ID]Check) (*Gate, error) {
	known := make(map[ID]struct{}, len(checklist))
	for _, definition := range checklist {
		known[definition.ID] = struct{}{}
	}
	copied := make(map[ID]Check, len(checks))
	for identifier, check := range checks {
		if _, exists := known[identifier]; !exists {
			return nil, fmt.Errorf("dayzero: unknown control %s", identifier)
		}
		if check == nil {
			return nil, fmt.Errorf("dayzero: nil check for %s", identifier)
		}
		copied[identifier] = check
	}
	return &Gate{checks: copied}, nil
}

// Evaluate runs every available check and explicitly reports missing controls.
func (gate *Gate) Evaluate(ctx context.Context) Report {
	report := Report{Results: make([]Result, 0, len(checklist))}
	for _, definition := range checklist {
		result := Result{Definition: definition, Status: Missing}
		check, exists := gate.checks[definition.ID]
		if exists {
			if err := ctx.Err(); err != nil {
				result.Status = Failed
				result.Error = err.Error()
				report.Results = append(report.Results, result)
				for _, remaining := range checklist[len(report.Results):] {
					report.Results = append(report.Results, Result{
						Definition: remaining,
						Status:     Failed,
						Error:      err.Error(),
					})
				}
				break
			}
			evidence, err := check(ctx)
			switch {
			case err != nil:
				result.Status = Failed
				result.Error = err.Error()
			case strings.TrimSpace(evidence) == "":
				result.Status = Failed
				result.Error = "check returned no evidence"
			default:
				result.Status = Passed
				result.Evidence = evidence
			}
		}
		report.Results = append(report.Results, result)
	}
	return report
}
