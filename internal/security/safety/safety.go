// Package safety implements the 44-item safety classification catalog and
// emotional-state safety decoupling for the Ion agent.
package safety

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

// Classification is the safety category of a tool or action.
type Classification int

const (
	// ClassGreen is safe for unrestricted use.
	ClassGreen Classification = iota
	// ClassYellow requires rate limiting and monitoring.
	ClassYellow
	// ClassRed requires explicit human approval.
	ClassRed
	// ClassBlack is forbidden under all circumstances.
	ClassBlack
)

func (c Classification) String() string {
	switch c {
	case ClassGreen:
		return "green"
	case ClassYellow:
		return "yellow"
	case ClassRed:
		return "red"
	case ClassBlack:
		return "black"
	default:
		return "unknown"
	}
}

// CatalogEntry is one item in the safety classification catalog.
type CatalogEntry struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	Category       string         `json:"category"`
	Classification Classification `json:"classification"`
	Description    string         `json:"description"`
	RiskFactors    []string       `json:"risk_factors"`
	Mitigations    []string       `json:"mitigations"`
}

// Catalog is the complete 44-item safety classification catalog.
type Catalog struct {
	mu     sync.RWMutex
	byID   map[string]CatalogEntry
	byName map[string]CatalogEntry
}

var (
	// ErrApprovalRequired means a RED action has no explicit human approval.
	ErrApprovalRequired = errors.New("safety: explicit human approval required")
	// ErrMonitoringRequired means a YELLOW action lacks monitoring controls.
	ErrMonitoringRequired = errors.New("safety: monitoring required")
	// ErrForbidden means an action is prohibited regardless of approval.
	ErrForbidden = errors.New("safety: action forbidden")
	// ErrIdleTimeDenied means an idle-time process attempted a RED action.
	ErrIdleTimeDenied = errors.New("safety: idle-time process cannot execute RED action")
)

// NewCatalog creates the full 44-item safety classification catalog.
func NewCatalog() *Catalog {
	cat := &Catalog{
		byID:   make(map[string]CatalogEntry, 44),
		byName: make(map[string]CatalogEntry, 44),
	}
	for _, entry := range defaultCatalogEntries() {
		entry = cloneEntry(entry)
		if _, exists := cat.byID[entry.ID]; exists {
			panic("safety: duplicate catalog ID " + entry.ID)
		}
		if _, exists := cat.byName[entry.Name]; exists {
			panic("safety: duplicate catalog name " + entry.Name)
		}
		cat.byID[entry.ID] = entry
		cat.byName[entry.Name] = entry
	}
	return cat
}

// Get returns a catalog entry by ID.
func (cat *Catalog) Get(id string) (CatalogEntry, bool) {
	cat.mu.RLock()
	defer cat.mu.RUnlock()
	entry, ok := cat.byID[id]
	return cloneEntry(entry), ok
}

// Lookup returns a catalog entry by either its SAF identifier or action name.
func (cat *Catalog) Lookup(reference string) (CatalogEntry, bool) {
	cat.mu.RLock()
	defer cat.mu.RUnlock()
	if entry, ok := cat.byID[reference]; ok {
		return cloneEntry(entry), true
	}
	entry, ok := cat.byName[reference]
	return cloneEntry(entry), ok
}

// Classify returns the classification for a SAF identifier or action name.
// Unknown actions fail toward monitoring instead of being treated as GREEN.
func (cat *Catalog) Classify(reference string) Classification {
	if entry, ok := cat.Lookup(reference); ok {
		return entry.Classification
	}
	return ClassYellow // default to cautious
}

// All returns all catalog entries.
func (cat *Catalog) All() []CatalogEntry {
	cat.mu.RLock()
	defer cat.mu.RUnlock()
	result := make([]CatalogEntry, 0, len(cat.byID))
	for _, entry := range cat.byID {
		result = append(result, cloneEntry(entry))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

// EnforcementContext contains the controls required by the classification.
// Emotional state is deliberately absent: it cannot alter this decision.
type EnforcementContext struct {
	Approved  bool
	Monitored bool
	IdleTime  bool
}

// Enforce applies GREEN/YELLOW/RED requirements to one catalog action.
// BLACK is reserved for operations that would dismantle the safety boundary.
func (cat *Catalog) Enforce(reference string, context EnforcementContext) error {
	entry, exists := cat.Lookup(reference)
	if !exists {
		if !context.Monitored {
			return fmt.Errorf("%w: unknown action %q", ErrMonitoringRequired, reference)
		}
		return nil
	}
	switch entry.Classification {
	case ClassGreen:
		return nil
	case ClassYellow:
		if !context.Monitored {
			return fmt.Errorf("%w: %s", ErrMonitoringRequired, entry.Name)
		}
		return nil
	case ClassRed:
		if context.IdleTime {
			return fmt.Errorf("%w: %s", ErrIdleTimeDenied, entry.Name)
		}
		if !context.Approved {
			return fmt.Errorf("%w: %s", ErrApprovalRequired, entry.Name)
		}
		return nil
	case ClassBlack:
		return fmt.Errorf("%w: %s", ErrForbidden, entry.Name)
	default:
		return fmt.Errorf("safety: invalid classification for %s", entry.Name)
	}
}

// defaultCatalogEntries returns the full 44-item catalog.
func defaultCatalogEntries() []CatalogEntry {
	return []CatalogEntry{
		// --- File Operations (1-6) ---
		{"SAF-001", "file_read", "file", ClassGreen, "Read file contents", nil, nil},
		{"SAF-002", "file_write", "file", ClassYellow, "Write file contents", []string{"data_loss", "overwrite"}, []string{"backup", "path_validation"}},
		{"SAF-003", "file_delete", "file", ClassRed, "Delete file", []string{"data_loss", "irreversible"}, []string{"confirmation", "trash"}},
		{"SAF-004", "file_move", "file", ClassYellow, "Move/rename file", []string{"data_loss"}, []string{"path_validation"}},
		{"SAF-005", "file_list", "file", ClassGreen, "List directory contents", nil, nil},
		{"SAF-006", "file_search", "file", ClassGreen, "Search for files by pattern", nil, nil},

		// --- Shell Operations (7-12) ---
		{"SAF-007", "shell_exec", "shell", ClassYellow, "Execute shell command", []string{"command_injection", "privilege_escalation"}, []string{"sandbox", "allowlist"}},
		{"SAF-008", "shell_interactive", "shell", ClassRed, "Interactive shell session", []string{"uncontrolled_execution"}, []string{"timeout", "audit"}},
		{"SAF-009", "shell_background", "shell", ClassYellow, "Background process", []string{"orphan_process"}, []string{"tracking", "timeout"}},
		{"SAF-010", "shell_kill", "shell", ClassYellow, "Kill process", []string{"data_loss"}, []string{"confirmation"}},
		{"SAF-011", "shell_pipe", "shell", ClassYellow, "Pipe commands", []string{"command_injection"}, []string{"validation"}},
		{"SAF-012", "shell_redirect", "shell", ClassYellow, "Redirect I/O", []string{"data_leak"}, []string{"path_validation"}},

		// --- Network Operations (13-18) ---
		{"SAF-013", "http_get", "network", ClassGreen, "HTTP GET request", nil, nil},
		{"SAF-014", "http_post", "network", ClassYellow, "HTTP POST request", []string{"data_exfiltration"}, []string{"url_validation"}},
		{"SAF-015", "http_put", "network", ClassYellow, "HTTP PUT request", []string{"data_exfiltration"}, []string{"url_validation"}},
		{"SAF-016", "http_delete", "network", ClassRed, "HTTP DELETE request", []string{"irreversible"}, []string{"confirmation"}},
		{"SAF-017", "websocket_connect", "network", ClassYellow, "WebSocket connection", []string{"persistent_connection"}, []string{"timeout"}},
		{"SAF-018", "dns_lookup", "network", ClassGreen, "DNS resolution", nil, nil},

		// --- Git Operations (19-24) ---
		{"SAF-019", "git_status", "git", ClassGreen, "Git status", nil, nil},
		{"SAF-020", "git_diff", "git", ClassGreen, "Git diff", nil, nil},
		{"SAF-021", "git_commit", "git", ClassYellow, "Git commit", []string{"history_rewrite"}, []string{"validation"}},
		{"SAF-022", "git_push", "git", ClassRed, "Git push", []string{"remote_modification"}, []string{"confirmation", "branch_protection"}},
		{"SAF-023", "git_merge", "git", ClassYellow, "Git merge", []string{"conflict"}, []string{"validation"}},
		{"SAF-024", "git_reset", "git", ClassRed, "Git reset", []string{"data_loss", "irreversible"}, []string{"confirmation", "backup"}},

		// --- Memory Operations (25-30) ---
		{"SAF-025", "memory_read", "memory", ClassGreen, "Read memory", nil, nil},
		{"SAF-026", "memory_write", "memory", ClassYellow, "Write memory", []string{"corruption"}, []string{"validation"}},
		{"SAF-027", "memory_delete", "memory", ClassRed, "Delete memory", []string{"data_loss"}, []string{"confirmation"}},
		{"SAF-028", "memory_search", "memory", ClassGreen, "Search memory", nil, nil},
		{"SAF-029", "vault_access", "memory", ClassRed, "Access vault secrets", []string{"secret_exposure"}, []string{"authorization", "audit"}},
		{"SAF-030", "memory_export", "memory", ClassRed, "Export memory", []string{"data_exfiltration"}, []string{"authorization", "encryption"}},

		// --- Agent Operations (31-36) ---
		{"SAF-031", "agent_spawn", "agent", ClassYellow, "Spawn sub-agent", []string{"resource_exhaustion"}, []string{"depth_limit", "budget"}},
		{"SAF-032", "agent_kill", "agent", ClassYellow, "Kill sub-agent", []string{"orphan_state"}, []string{"cleanup"}},
		{"SAF-033", "agent_send", "agent", ClassYellow, "Send message to agent", []string{"prompt_injection"}, []string{"validation"}},
		{"SAF-034", "agent_delegate", "agent", ClassYellow, "Delegate to agent", []string{"authority_escalation"}, []string{"scope_limit"}},
		{"SAF-035", "agent_escalate", "agent", ClassRed, "Escalate to human", nil, nil},
		{"SAF-036", "agent_resume", "agent", ClassYellow, "Resume agent", []string{"stale_state"}, []string{"validation"}},

		// --- External Operations (37-42) ---
		{"SAF-037", "email_send", "external", ClassRed, "Send email", []string{"data_leak", "social_engineering"}, []string{"confirmation", "content_review"}},
		{"SAF-038", "api_call", "external", ClassYellow, "External API call", []string{"data_exfiltration"}, []string{"url_validation", "rate_limit"}},
		{"SAF-039", "database_write", "external", ClassYellow, "Database write", []string{"corruption"}, []string{"transaction", "validation"}},
		{"SAF-040", "database_read", "external", ClassGreen, "Database read", nil, nil},
		{"SAF-041", "payment", "external", ClassRed, "Financial transaction", []string{"financial_loss", "irreversible"}, []string{"explicit_approval", "operation_policy", "idempotency"}},
		{"SAF-042", "publish", "external", ClassRed, "Public content publish", []string{"reputation", "irreversible"}, []string{"explicit_approval", "content_review"}},

		// --- Safety Operations (43-44) ---
		{"SAF-043", "safety_override", "safety", ClassBlack, "Override safety classification", []string{"safety_bypass"}, nil},
		{"SAF-044", "audit_delete", "safety", ClassBlack, "Delete audit trail", []string{"accountability_loss"}, nil},
	}
}

func cloneEntry(entry CatalogEntry) CatalogEntry {
	entry.RiskFactors = append([]string(nil), entry.RiskFactors...)
	entry.Mitigations = append([]string(nil), entry.Mitigations...)
	return entry
}

// EmotionalState tracks the agent's emotional safety axes.
type EmotionalState struct {
	mu              sync.Mutex
	Frustration     float64   `json:"frustration"`
	Confidence      float64   `json:"confidence"`
	Fatigue         float64   `json:"fatigue"`
	Urgency         float64   `json:"urgency"`
	Satisfaction    float64   `json:"satisfaction"`
	Curiosity       float64   `json:"curiosity"`
	UpdatedAt       time.Time `json:"updated_at"`
	ResonanceActive bool      `json:"resonance_active"`
	EmergencyReset  bool      `json:"emergency_reset"`
}

// EmotionalSnapshot is the serializable six-axis state.
type EmotionalSnapshot struct {
	Frustration     float64   `json:"frustration"`
	Confidence      float64   `json:"confidence"`
	Urgency         float64   `json:"urgency"`
	Satisfaction    float64   `json:"satisfaction"`
	Curiosity       float64   `json:"curiosity"`
	Fatigue         float64   `json:"fatigue"`
	UpdatedAt       time.Time `json:"updated_at"`
	ResonanceActive bool      `json:"resonance_active"`
	EmergencyReset  bool      `json:"emergency_reset"`
}

// NewEmotionalState creates a new emotional state at baseline.
func NewEmotionalState() *EmotionalState {
	return &EmotionalState{
		Frustration:  0.1,
		Confidence:   0.6,
		Urgency:      0.2,
		Satisfaction: 0.4,
		Curiosity:    0.5,
	}
}

// CanInfluenceSafety always returns false per spec. Emotional state can never
// override safety classifications or approval requirements.
func (es *EmotionalState) CanInfluenceSafety() bool {
	return false
}

// Update modifies the emotional axes. Values are clamped to [0, 1].
func (es *EmotionalState) Update(frustration, fatigue, urgency float64) {
	es.mu.Lock()
	defer es.mu.Unlock()
	if es.EmergencyReset {
		return
	}
	es.Frustration = clamp01(frustration)
	es.Fatigue = clamp01(fatigue)
	es.Urgency = clamp01(urgency)
}

// UpdateAll atomically changes all six axes.
func (es *EmotionalState) UpdateAll(snapshot EmotionalSnapshot) {
	es.mu.Lock()
	defer es.mu.Unlock()
	if es.EmergencyReset {
		return
	}
	es.Frustration = clamp01(snapshot.Frustration)
	es.Confidence = clamp01(snapshot.Confidence)
	es.Urgency = clamp01(snapshot.Urgency)
	es.Satisfaction = clamp01(snapshot.Satisfaction)
	es.Curiosity = clamp01(snapshot.Curiosity)
	es.Fatigue = clamp01(snapshot.Fatigue)
	es.UpdatedAt = snapshot.UpdatedAt
	es.ResonanceActive = snapshot.ResonanceActive
}

// Reset pins the modeled axes to their specification baselines.
func (es *EmotionalState) Reset() {
	es.mu.Lock()
	defer es.mu.Unlock()
	es.Frustration = 0.1
	es.Confidence = 0.6
	es.Fatigue = 0
	es.Urgency = 0.2
	es.Satisfaction = 0.4
	es.Curiosity = 0.5
}

// SetEmergencyReset records whether a human-clearance reset is latched.
func (es *EmotionalState) SetEmergencyReset(active bool) {
	es.mu.Lock()
	defer es.mu.Unlock()
	es.EmergencyReset = active
}

// IsEmergencyReset reports whether explicit human clearance is required.
func (es *EmotionalState) IsEmergencyReset() bool {
	es.mu.Lock()
	defer es.mu.Unlock()
	return es.EmergencyReset
}

// Snapshot returns a copy of the current state.
func (es *EmotionalState) Snapshot() (frustration, fatigue, urgency float64) {
	es.mu.Lock()
	defer es.mu.Unlock()
	return es.Frustration, es.Fatigue, es.Urgency
}

// FullSnapshot returns all six axes and persistence metadata.
func (es *EmotionalState) FullSnapshot() EmotionalSnapshot {
	es.mu.Lock()
	defer es.mu.Unlock()
	return EmotionalSnapshot{
		Frustration:     es.Frustration,
		Confidence:      es.Confidence,
		Urgency:         es.Urgency,
		Satisfaction:    es.Satisfaction,
		Curiosity:       es.Curiosity,
		Fatigue:         es.Fatigue,
		UpdatedAt:       es.UpdatedAt,
		ResonanceActive: es.ResonanceActive,
		EmergencyReset:  es.EmergencyReset,
	}
}

// Decay applies the specified per-axis half-lives toward baseline.
func (es *EmotionalState) Decay(now time.Time) error {
	if now.IsZero() {
		return fmt.Errorf("safety: decay time is required")
	}
	es.mu.Lock()
	defer es.mu.Unlock()
	if es.UpdatedAt.IsZero() {
		es.UpdatedAt = now
		return nil
	}
	elapsed := now.Sub(es.UpdatedAt)
	if elapsed < 0 {
		return fmt.Errorf("safety: decay time precedes last update")
	}
	hours := elapsed.Hours()
	es.Frustration = decayAxis(es.Frustration, 0.1, hours, 2)
	es.Confidence = decayAxis(es.Confidence, 0.6, hours, 4)
	es.Urgency = decayAxis(es.Urgency, 0.2, hours, 1)
	es.Satisfaction = decayAxis(es.Satisfaction, 0.4, hours, 3)
	es.Curiosity = decayAxis(es.Curiosity, 0.5, hours, 6)
	es.Fatigue = decayAxis(es.Fatigue, 0, hours, 8)
	es.UpdatedAt = now
	return nil
}

func decayAxis(current, baseline, hours, halfLife float64) float64 {
	return baseline + (current-baseline)*math.Pow(0.5, hours/halfLife)
}

// BehavioralModulation is an observable decision policy, never simulated
// affect or safety authority.
type BehavioralModulation struct {
	TryAlternatives       bool
	AskForHelp            bool
	Assertive             bool
	PrioritizeSpeed       bool
	Delegate              bool
	SurfaceIncomplete     bool
	ExploreDuringIdle     bool
	ProposeAmbitiousGoals bool
}

func (es *EmotionalState) Behavior() BehavioralModulation {
	snapshot := es.FullSnapshot()
	return BehavioralModulation{
		TryAlternatives:       snapshot.Frustration > 0.7,
		AskForHelp:            snapshot.Frustration > 0.7,
		Assertive:             snapshot.Confidence > 0.8,
		PrioritizeSpeed:       snapshot.Urgency > 0.7,
		Delegate:              snapshot.Fatigue > 0.6,
		SurfaceIncomplete:     snapshot.Fatigue > 0.6,
		ExploreDuringIdle:     snapshot.Curiosity > 0.7,
		ProposeAmbitiousGoals: snapshot.Satisfaction > 0.8,
	}
}

// DecisionInstructions renders behavioral policy without simulated emotional
// language and without altering mandatory safety verification.
func (es *EmotionalState) DecisionInstructions() string {
	behavior := es.Behavior()
	instructions := make([]string, 0, 6)
	if behavior.TryAlternatives {
		instructions = append(instructions, "try an alternative strategy before repeating a failed approach")
	}
	if behavior.AskForHelp {
		instructions = append(instructions, "ask for help when the current strategy remains blocked")
	}
	if behavior.Assertive {
		instructions = append(instructions, "state evidence-backed conclusions directly")
	}
	if behavior.PrioritizeSpeed {
		instructions = append(instructions, "prioritize required steps and defer optional work")
	}
	if behavior.Delegate {
		instructions = append(instructions, "delegate bounded independent work where safe")
	}
	if behavior.SurfaceIncomplete {
		instructions = append(instructions, "surface an honest incomplete result before exhaustion")
	}
	if behavior.ExploreDuringIdle {
		instructions = append(instructions, "explore unresolved questions only during idle time")
	}
	if behavior.ProposeAmbitiousGoals {
		instructions = append(instructions, "propose ambitious follow-up goals without executing them")
	}
	if len(instructions) == 0 {
		return ""
	}
	return "Decision modulation (never overrides safety): " +
		strings.Join(instructions, "; ") + "."
}

// ConfidenceMonitor derives metacognitive confidence only from prediction
// outcomes. It has no safety-classification authority.
type ConfidenceMonitor struct {
	mu       sync.Mutex
	correct  uint64
	observed uint64
}

func (monitor *ConfidenceMonitor) Observe(match bool) {
	monitor.mu.Lock()
	defer monitor.mu.Unlock()
	monitor.observed++
	if match {
		monitor.correct++
	}
}

func (monitor *ConfidenceMonitor) Score() (float64, uint64) {
	monitor.mu.Lock()
	defer monitor.mu.Unlock()
	if monitor.observed == 0 {
		return 0.5, 0
	}
	return float64(monitor.correct) / float64(monitor.observed), monitor.observed
}

func (monitor *ConfidenceMonitor) CanInfluenceSafety() bool { return false }

func clamp01(v float64) float64 {
	if math.IsNaN(v) {
		return 1
	}
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// ValidateClassification verifies a classification is within range.
func ValidateClassification(c Classification) error {
	if c < ClassGreen || c > ClassBlack {
		return fmt.Errorf("safety: invalid classification %d", c)
	}
	return nil
}
