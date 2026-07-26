// Package relationship tracks bounded, domain-specific social context.
package relationship

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	MaxTrustPerInteraction = 0.05
	MaxTrustPerDay         = 0.20
	MaxTrust               = 0.90
)

type Expertise string

const (
	Beginner     Expertise = "beginner"
	Intermediate Expertise = "intermediate"
	Expert       Expertise = "expert"
)

func (expertise Expertise) Valid() bool {
	switch expertise {
	case Beginner, Intermediate, Expert:
		return true
	default:
		return false
	}
}

// Profile is the user-reviewable communication and workflow contract for one
// relationship domain. Populated values are explicit user declarations;
// PinnedFields identifies declarations that should remain especially visible
// during later review and must never be replaced by inference.
type Profile struct {
	ResponseLength       string   `json:"response_length,omitempty"`
	Directness           string   `json:"directness,omitempty"`
	ConclusionFirst      *bool    `json:"conclusion_first,omitempty"`
	PreferredTools       []string `json:"preferred_tools,omitempty"`
	RiskTolerance        string   `json:"risk_tolerance,omitempty"`
	ProactiveSuggestions *bool    `json:"proactive_suggestions,omitempty"`
	NotificationCadence  string   `json:"notification_cadence,omitempty"`
	Dislikes             []string `json:"dislikes,omitempty"`
	Constraints          []string `json:"constraints,omitempty"`
	ProjectPrinciples    []string `json:"project_principles,omitempty"`
	PinnedFields         []string `json:"pinned_fields,omitempty"`
}

// ProfilePatch contains explicit corrections. Nil fields remain unchanged.
type ProfilePatch struct {
	ResponseLength       *string    `json:"response_length,omitempty"`
	Directness           *string    `json:"directness,omitempty"`
	ConclusionFirst      *bool      `json:"conclusion_first,omitempty"`
	DomainExpertise      *Expertise `json:"domain_expertise,omitempty"`
	PreferredTools       *[]string  `json:"preferred_tools,omitempty"`
	RiskTolerance        *string    `json:"risk_tolerance,omitempty"`
	ProactiveSuggestions *bool      `json:"proactive_suggestions,omitempty"`
	NotificationCadence  *string    `json:"notification_cadence,omitempty"`
	Dislikes             *[]string  `json:"dislikes,omitempty"`
	Constraints          *[]string  `json:"constraints,omitempty"`
	ProjectPrinciples    *[]string  `json:"project_principles,omitempty"`
}

// Snapshot is the complete per-user, per-domain relationship state.
type Snapshot struct {
	UserID                  string    `json:"user_id"`
	Domain                  string    `json:"domain"`
	Trust                   float64   `json:"trust"`
	Expertise               Expertise `json:"expertise"`
	CommunicationPreference string    `json:"communication_preference"`
	RecentSentiment         float64   `json:"recent_sentiment"`
	InteractionFrequency    uint64    `json:"interaction_frequency"`
	UpdatedAt               time.Time `json:"updated_at"`
	Profile                 Profile   `json:"profile"`
}

// State is the complete restart-safe representation of one relationship.
// Day and DailyChange are deliberately included because omitting them would
// reset the daily trust-rate limit after every daemon restart.
type State struct {
	Version            int      `json:"version"`
	Snapshot           Snapshot `json:"snapshot"`
	Day                string   `json:"day"`
	DailyChange        float64  `json:"daily_change"`
	ExpertiseDeclared  bool     `json:"expertise_declared"`
	PreferenceDeclared bool     `json:"preference_declared"`
}

type state struct {
	Snapshot
	day                string
	dailyChange        float64
	expertiseDeclared  bool
	preferenceDeclared bool
}

// Clock supplies interaction dates.
type Clock interface{ Now() time.Time }

// Model is a concurrency-safe relationship registry.
type Model struct {
	mu    sync.RWMutex
	clock Clock
	items map[string]*state
}

func New(clock Clock) (*Model, error) {
	if clock == nil {
		return nil, fmt.Errorf("relationship: clock is required")
	}
	return &Model{clock: clock, items: make(map[string]*state)}, nil
}

// Restore reconstructs a model from validated durable state.
func Restore(clock Clock, states []State) (*Model, error) {
	model, err := New(clock)
	if err != nil {
		return nil, err
	}
	for _, durable := range states {
		if err := durable.Validate(); err != nil {
			return nil, err
		}
		key := durable.Snapshot.UserID + "\x00" + durable.Snapshot.Domain
		if _, exists := model.items[key]; exists {
			return nil, fmt.Errorf("relationship: duplicate durable scope")
		}
		model.items[key] = &state{
			Snapshot:           cloneSnapshot(durable.Snapshot),
			day:                durable.Day,
			dailyChange:        durable.DailyChange,
			expertiseDeclared:  durable.ExpertiseDeclared,
			preferenceDeclared: durable.PreferenceDeclared,
		}
	}
	return model, nil
}

// Validate rejects malformed, out-of-range, or ambiguous durable state.
func (durable State) Validate() error {
	snapshot := durable.Snapshot
	if durable.Version != 1 || strings.TrimSpace(snapshot.UserID) == "" ||
		strings.TrimSpace(snapshot.Domain) == "" || !snapshot.Expertise.Valid() ||
		math.IsNaN(snapshot.Trust) || math.IsInf(snapshot.Trust, 0) ||
		snapshot.Trust < 0 || snapshot.Trust > MaxTrust ||
		math.IsNaN(snapshot.RecentSentiment) ||
		math.IsInf(snapshot.RecentSentiment, 0) ||
		snapshot.RecentSentiment < -1 || snapshot.RecentSentiment > 1 ||
		snapshot.UpdatedAt.IsZero() || durable.Day == "" ||
		math.IsNaN(durable.DailyChange) ||
		math.IsInf(durable.DailyChange, 0) ||
		durable.DailyChange < 0 || durable.DailyChange > MaxTrustPerDay {
		return fmt.Errorf("relationship: invalid durable state")
	}
	if err := validateProfile(snapshot.Profile); err != nil {
		return err
	}
	return nil
}

// Observe records one interaction and applies both trust rate limits.
func (model *Model) Observe(
	userID string,
	domain string,
	requestedTrustDelta float64,
	expertise Expertise,
	communicationPreference string,
	sentiment float64,
) (Snapshot, error) {
	return model.ObserveAuthoritative(
		userID, domain, requestedTrustDelta, expertise,
		communicationPreference, sentiment, true, true,
	)
}

// ObserveAuthoritative records an interaction while preserving explicit user
// declarations over later inference. Inferred values may initialize a new
// relationship but cannot replace already-declared expertise or preferences.
func (model *Model) ObserveAuthoritative(
	userID string,
	domain string,
	requestedTrustDelta float64,
	expertise Expertise,
	communicationPreference string,
	sentiment float64,
	expertiseDeclared bool,
	preferenceDeclared bool,
) (Snapshot, error) {
	userID = strings.TrimSpace(userID)
	domain = strings.TrimSpace(domain)
	if userID == "" || domain == "" || !expertise.Valid() ||
		math.IsNaN(requestedTrustDelta) || math.IsInf(requestedTrustDelta, 0) ||
		math.IsNaN(sentiment) || math.IsInf(sentiment, 0) {
		return Snapshot{}, fmt.Errorf("relationship: valid user, domain, expertise, trust delta, and sentiment are required")
	}
	now := model.clock.Now().UTC()
	key := userID + "\x00" + domain
	model.mu.Lock()
	defer model.mu.Unlock()
	item := model.items[key]
	if item == nil {
		item = &state{Snapshot: Snapshot{
			UserID: userID, Domain: domain, Trust: 0.5, Expertise: expertise,
		}}
		model.items[key] = item
	}
	day := now.Format("2006-01-02")
	if item.day != day {
		item.day = day
		item.dailyChange = 0
	}
	delta := clamp(requestedTrustDelta, -MaxTrustPerInteraction, MaxTrustPerInteraction)
	remaining := MaxTrustPerDay - item.dailyChange
	if remaining < 0 {
		remaining = 0
	}
	delta = clamp(delta, -remaining, remaining)
	item.Trust = rounded(clamp(item.Trust+delta, 0, MaxTrust))
	item.dailyChange = rounded(item.dailyChange + math.Abs(delta))
	if expertiseDeclared || !item.expertiseDeclared {
		item.Expertise = expertise
		item.expertiseDeclared = expertiseDeclared
	}
	if preferenceDeclared || !item.preferenceDeclared {
		item.CommunicationPreference = strings.TrimSpace(communicationPreference)
		item.preferenceDeclared = preferenceDeclared
	}
	item.RecentSentiment = clamp(sentiment, -1, 1)
	item.InteractionFrequency++
	item.UpdatedAt = now
	return cloneSnapshot(item.Snapshot), nil
}

// Prepare makes explicit declarations available to the current provider step
// without counting an interaction before it has actually completed.
func (model *Model) Prepare(
	userID string,
	domain string,
	expertise Expertise,
	communicationPreference string,
	expertiseDeclared bool,
	preferenceDeclared bool,
) (Snapshot, error) {
	userID = strings.TrimSpace(userID)
	domain = strings.TrimSpace(domain)
	if userID == "" || domain == "" || !expertise.Valid() {
		return Snapshot{}, fmt.Errorf(
			"relationship: valid user, domain, and expertise are required",
		)
	}
	now := model.clock.Now().UTC()
	key := userID + "\x00" + domain
	model.mu.Lock()
	defer model.mu.Unlock()
	item := model.items[key]
	if item == nil {
		item = &state{Snapshot: Snapshot{
			UserID: userID, Domain: domain, Trust: 0.5,
			Expertise: expertise, UpdatedAt: now,
		}, day: now.Format("2006-01-02")}
		model.items[key] = item
	}
	if expertiseDeclared || !item.expertiseDeclared {
		item.Expertise = expertise
		item.expertiseDeclared = expertiseDeclared
	}
	if preferenceDeclared || !item.preferenceDeclared {
		item.CommunicationPreference = strings.TrimSpace(
			communicationPreference,
		)
		item.preferenceDeclared = preferenceDeclared
	}
	item.UpdatedAt = now
	return cloneSnapshot(item.Snapshot), nil
}

// RecordCompleted records interaction and sentiment only after the production
// turn has completed.
func (model *Model) RecordCompleted(
	userID string,
	domain string,
	sentiment float64,
) (Snapshot, error) {
	userID = strings.TrimSpace(userID)
	domain = strings.TrimSpace(domain)
	if userID == "" || domain == "" || math.IsNaN(sentiment) ||
		math.IsInf(sentiment, 0) {
		return Snapshot{}, fmt.Errorf(
			"relationship: valid scope and sentiment are required",
		)
	}
	model.mu.Lock()
	defer model.mu.Unlock()
	item := model.items[userID+"\x00"+domain]
	if item == nil {
		return Snapshot{}, fmt.Errorf("relationship: scope is not initialized")
	}
	item.RecentSentiment = clamp(sentiment, -1, 1)
	item.InteractionFrequency++
	item.UpdatedAt = model.clock.Now().UTC()
	return cloneSnapshot(item.Snapshot), nil
}

// AdjustTrust applies verified outcome evidence without manufacturing a second
// interaction or changing user-declared communication state.
func (model *Model) AdjustTrust(
	userID string,
	domain string,
	requestedTrustDelta float64,
) (Snapshot, error) {
	userID = strings.TrimSpace(userID)
	domain = strings.TrimSpace(domain)
	if userID == "" || domain == "" ||
		math.IsNaN(requestedTrustDelta) || math.IsInf(requestedTrustDelta, 0) {
		return Snapshot{}, fmt.Errorf("relationship: valid scope and trust delta are required")
	}
	now := model.clock.Now().UTC()
	key := userID + "\x00" + domain
	model.mu.Lock()
	defer model.mu.Unlock()
	item := model.items[key]
	if item == nil {
		return Snapshot{}, fmt.Errorf("relationship: scope is not initialized")
	}
	day := now.Format("2006-01-02")
	if item.day != day {
		item.day = day
		item.dailyChange = 0
	}
	delta := clamp(requestedTrustDelta, -MaxTrustPerInteraction, MaxTrustPerInteraction)
	remaining := MaxTrustPerDay - item.dailyChange
	if remaining < 0 {
		remaining = 0
	}
	delta = clamp(delta, -remaining, remaining)
	item.Trust = rounded(clamp(item.Trust+delta, 0, MaxTrust))
	item.dailyChange = rounded(item.dailyChange + math.Abs(delta))
	item.UpdatedAt = now
	return cloneSnapshot(item.Snapshot), nil
}

// CorrectProfile applies an explicit user correction. It deliberately accepts
// no inferred source and therefore always takes precedence over observations.
func (model *Model) CorrectProfile(
	userID string,
	domain string,
	patch ProfilePatch,
) (Snapshot, error) {
	userID = strings.TrimSpace(userID)
	domain = strings.TrimSpace(domain)
	if userID == "" || domain == "" {
		return Snapshot{}, fmt.Errorf("relationship: valid profile scope is required")
	}
	model.mu.Lock()
	defer model.mu.Unlock()
	item := model.items[userID+"\x00"+domain]
	if item == nil {
		return Snapshot{}, fmt.Errorf("relationship: scope is not initialized")
	}
	if patch.ResponseLength != nil {
		value, err := normalizedChoice(*patch.ResponseLength, "brief", "balanced", "detailed")
		if err != nil {
			return Snapshot{}, fmt.Errorf("relationship: response length: %w", err)
		}
		item.Profile.ResponseLength = value
		item.CommunicationPreference = value
		item.preferenceDeclared = true
	}
	if patch.Directness != nil {
		value, err := normalizedChoice(*patch.Directness, "gentle", "balanced", "direct")
		if err != nil {
			return Snapshot{}, fmt.Errorf("relationship: directness: %w", err)
		}
		item.Profile.Directness = value
	}
	if patch.ConclusionFirst != nil {
		value := *patch.ConclusionFirst
		item.Profile.ConclusionFirst = &value
	}
	if patch.DomainExpertise != nil {
		if !patch.DomainExpertise.Valid() {
			return Snapshot{}, fmt.Errorf("relationship: domain expertise is invalid")
		}
		item.Expertise = *patch.DomainExpertise
		item.expertiseDeclared = true
	}
	if patch.PreferredTools != nil {
		value, err := normalizedList(*patch.PreferredTools)
		if err != nil {
			return Snapshot{}, fmt.Errorf("relationship: preferred tools: %w", err)
		}
		item.Profile.PreferredTools = value
	}
	if patch.RiskTolerance != nil {
		value, err := normalizedChoice(*patch.RiskTolerance, "low", "moderate", "high")
		if err != nil {
			return Snapshot{}, fmt.Errorf("relationship: risk tolerance: %w", err)
		}
		item.Profile.RiskTolerance = value
	}
	if patch.ProactiveSuggestions != nil {
		value := *patch.ProactiveSuggestions
		item.Profile.ProactiveSuggestions = &value
	}
	if patch.NotificationCadence != nil {
		value, err := normalizedChoice(
			*patch.NotificationCadence, "quiet", "milestones", "regular",
		)
		if err != nil {
			return Snapshot{}, fmt.Errorf("relationship: notification cadence: %w", err)
		}
		item.Profile.NotificationCadence = value
	}
	for value, target := range map[*[]string]*[]string{
		patch.Dislikes:          &item.Profile.Dislikes,
		patch.Constraints:       &item.Profile.Constraints,
		patch.ProjectPrinciples: &item.Profile.ProjectPrinciples,
	} {
		if value == nil {
			continue
		}
		normalized, err := normalizedList(*value)
		if err != nil {
			return Snapshot{}, fmt.Errorf("relationship: profile list: %w", err)
		}
		*target = normalized
	}
	item.UpdatedAt = model.clock.Now().UTC()
	return cloneSnapshot(item.Snapshot), nil
}

// PinProfileFields changes only review visibility. Pinned values retain the
// same execution authority and never weaken safety or approval.
func (model *Model) PinProfileFields(
	userID string,
	domain string,
	fields []string,
	pinned bool,
) (Snapshot, error) {
	userID = strings.TrimSpace(userID)
	domain = strings.TrimSpace(domain)
	normalized, err := normalizedFields(fields)
	if userID == "" || domain == "" || err != nil {
		return Snapshot{}, fmt.Errorf("relationship: valid profile scope and fields are required")
	}
	model.mu.Lock()
	defer model.mu.Unlock()
	item := model.items[userID+"\x00"+domain]
	if item == nil {
		return Snapshot{}, fmt.Errorf("relationship: scope is not initialized")
	}
	current := make(map[string]struct{}, len(item.Profile.PinnedFields))
	for _, field := range item.Profile.PinnedFields {
		current[field] = struct{}{}
	}
	for _, field := range normalized {
		if pinned {
			if !profileFieldPresent(item.Snapshot, field) {
				return Snapshot{}, fmt.Errorf("relationship: cannot pin an empty profile field")
			}
			current[field] = struct{}{}
		} else {
			delete(current, field)
		}
	}
	item.Profile.PinnedFields = sortedKeys(current)
	item.UpdatedAt = model.clock.Now().UTC()
	return cloneSnapshot(item.Snapshot), nil
}

// RemoveProfileFields removes explicit values and their pins. Trust and
// interaction history are deliberately unaffected.
func (model *Model) RemoveProfileFields(
	userID string,
	domain string,
	fields []string,
) (Snapshot, error) {
	userID = strings.TrimSpace(userID)
	domain = strings.TrimSpace(domain)
	normalized, err := normalizedFields(fields)
	if userID == "" || domain == "" || err != nil {
		return Snapshot{}, fmt.Errorf("relationship: valid profile scope and fields are required")
	}
	model.mu.Lock()
	defer model.mu.Unlock()
	item := model.items[userID+"\x00"+domain]
	if item == nil {
		return Snapshot{}, fmt.Errorf("relationship: scope is not initialized")
	}
	for _, field := range normalized {
		clearProfileField(item, field)
	}
	pinned := make(map[string]struct{}, len(item.Profile.PinnedFields))
	for _, field := range item.Profile.PinnedFields {
		pinned[field] = struct{}{}
	}
	for _, field := range normalized {
		delete(pinned, field)
	}
	item.Profile.PinnedFields = sortedKeys(pinned)
	item.UpdatedAt = model.clock.Now().UTC()
	return cloneSnapshot(item.Snapshot), nil
}

func (model *Model) Snapshot(userID string, domain string) (Snapshot, bool) {
	model.mu.RLock()
	defer model.mu.RUnlock()
	item, exists := model.items[strings.TrimSpace(userID)+"\x00"+strings.TrimSpace(domain)]
	if !exists {
		return Snapshot{}, false
	}
	return cloneSnapshot(item.Snapshot), true
}

func (model *Model) All(userID string) []Snapshot {
	model.mu.RLock()
	defer model.mu.RUnlock()
	var result []Snapshot
	prefix := strings.TrimSpace(userID) + "\x00"
	for key, item := range model.items {
		if strings.HasPrefix(key, prefix) {
			result = append(result, cloneSnapshot(item.Snapshot))
		}
	}
	return result
}

// AllSnapshots returns a defensive cross-user view for bounded internal
// liveness jobs such as proposal generation. Operator projections must continue
// to use All(userID) so authenticated users never see another actor's state.
func (model *Model) AllSnapshots() []Snapshot {
	model.mu.RLock()
	defer model.mu.RUnlock()
	result := make([]Snapshot, 0, len(model.items))
	for _, item := range model.items {
		result = append(result, cloneSnapshot(item.Snapshot))
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].UserID != result[right].UserID {
			return result[left].UserID < result[right].UserID
		}
		return result[left].Domain < result[right].Domain
	})
	return result
}

// States returns complete restart-safe values for one user.
func (model *Model) States(userID string) []State {
	model.mu.RLock()
	defer model.mu.RUnlock()
	var result []State
	prefix := strings.TrimSpace(userID) + "\x00"
	for key, item := range model.items {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		result = append(result, State{
			Version: 1, Snapshot: cloneSnapshot(item.Snapshot),
			Day: item.day, DailyChange: item.dailyChange,
			ExpertiseDeclared:  item.expertiseDeclared,
			PreferenceDeclared: item.preferenceDeclared,
		})
	}
	return result
}

// State returns one complete restart-safe value.
func (model *Model) State(userID string, domain string) (State, bool) {
	model.mu.RLock()
	defer model.mu.RUnlock()
	item, exists := model.items[strings.TrimSpace(userID)+"\x00"+strings.TrimSpace(domain)]
	if !exists {
		return State{}, false
	}
	return State{
		Version: 1, Snapshot: cloneSnapshot(item.Snapshot),
		Day: item.day, DailyChange: item.dailyChange,
		ExpertiseDeclared:  item.expertiseDeclared,
		PreferenceDeclared: item.preferenceDeclared,
	}, true
}

// CommunicationGuidance changes hedging/detail only. It exposes no safety
// policy or approval control.
func CommunicationGuidance(snapshot Snapshot) string {
	detail := "use moderate detail"
	switch snapshot.Profile.ResponseLength {
	case "brief":
		detail = "use brief responses"
	case "detailed":
		detail = "use detailed responses"
	case "balanced":
		detail = "use moderate detail"
	default:
		if snapshot.Expertise == Beginner {
			detail = "explain terminology and use concrete examples"
		} else if snapshot.Expertise == Expert {
			detail = "be concise and assume domain terminology"
		}
	}
	if snapshot.Profile.ConclusionFirst != nil && *snapshot.Profile.ConclusionFirst {
		detail += "; lead with the conclusion"
	}
	switch snapshot.Profile.Directness {
	case "direct":
		detail += "; use direct language"
	case "gentle":
		detail += "; use considerate language"
	}
	if snapshot.Profile.NotificationCadence == "milestones" {
		detail += "; report at meaningful milestones"
	}
	if snapshot.Trust >= 0.8 {
		return detail + "; reduce conversational hedging, never verification"
	}
	return detail + "; state uncertainty explicitly"
}

var profileFields = map[string]struct{}{
	"response_length": {}, "directness": {}, "conclusion_first": {},
	"domain_expertise": {}, "preferred_tools": {}, "risk_tolerance": {},
	"proactive_suggestions": {}, "notification_cadence": {},
	"dislikes": {}, "constraints": {}, "project_principles": {},
}

func normalizedChoice(value string, choices ...string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, choice := range choices {
		if value == choice {
			return value, nil
		}
	}
	return "", fmt.Errorf("value is outside the supported choices")
}

func normalizedList(values []string) ([]string, error) {
	if len(values) > 32 {
		return nil, fmt.Errorf("list exceeds 32 values")
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > 256 {
			return nil, fmt.Errorf("values must contain 1 to 256 bytes")
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result, nil
}

func normalizedFields(fields []string) ([]string, error) {
	if len(fields) == 0 || len(fields) > len(profileFields) {
		return nil, fmt.Errorf("invalid field count")
	}
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		field = strings.ToLower(strings.TrimSpace(field))
		if _, valid := profileFields[field]; !valid {
			return nil, fmt.Errorf("unsupported field")
		}
		seen[field] = struct{}{}
	}
	return sortedKeys(seen), nil
}

func sortedKeys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func validateProfile(profile Profile) error {
	if profile.ResponseLength != "" {
		if _, err := normalizedChoice(profile.ResponseLength, "brief", "balanced", "detailed"); err != nil {
			return fmt.Errorf("relationship: invalid durable response length")
		}
	}
	if profile.Directness != "" {
		if _, err := normalizedChoice(profile.Directness, "gentle", "balanced", "direct"); err != nil {
			return fmt.Errorf("relationship: invalid durable directness")
		}
	}
	if profile.RiskTolerance != "" {
		if _, err := normalizedChoice(profile.RiskTolerance, "low", "moderate", "high"); err != nil {
			return fmt.Errorf("relationship: invalid durable risk tolerance")
		}
	}
	if profile.NotificationCadence != "" {
		if _, err := normalizedChoice(profile.NotificationCadence, "quiet", "milestones", "regular"); err != nil {
			return fmt.Errorf("relationship: invalid durable notification cadence")
		}
	}
	for _, values := range [][]string{
		profile.PreferredTools, profile.Dislikes, profile.Constraints,
		profile.ProjectPrinciples,
	} {
		if _, err := normalizedList(values); err != nil {
			return fmt.Errorf("relationship: invalid durable profile list")
		}
	}
	if _, err := normalizedFieldsOrEmpty(profile.PinnedFields); err != nil {
		return fmt.Errorf("relationship: invalid durable pinned fields")
	}
	return nil
}

func normalizedFieldsOrEmpty(fields []string) ([]string, error) {
	if len(fields) == 0 {
		return nil, nil
	}
	return normalizedFields(fields)
}

func profileFieldPresent(snapshot Snapshot, field string) bool {
	switch field {
	case "response_length":
		return snapshot.Profile.ResponseLength != ""
	case "directness":
		return snapshot.Profile.Directness != ""
	case "conclusion_first":
		return snapshot.Profile.ConclusionFirst != nil
	case "domain_expertise":
		return snapshot.Expertise.Valid()
	case "preferred_tools":
		return len(snapshot.Profile.PreferredTools) > 0
	case "risk_tolerance":
		return snapshot.Profile.RiskTolerance != ""
	case "proactive_suggestions":
		return snapshot.Profile.ProactiveSuggestions != nil
	case "notification_cadence":
		return snapshot.Profile.NotificationCadence != ""
	case "dislikes":
		return len(snapshot.Profile.Dislikes) > 0
	case "constraints":
		return len(snapshot.Profile.Constraints) > 0
	case "project_principles":
		return len(snapshot.Profile.ProjectPrinciples) > 0
	default:
		return false
	}
}

func clearProfileField(item *state, field string) {
	switch field {
	case "response_length":
		item.Profile.ResponseLength = ""
		item.CommunicationPreference = ""
		item.preferenceDeclared = false
	case "directness":
		item.Profile.Directness = ""
	case "conclusion_first":
		item.Profile.ConclusionFirst = nil
	case "domain_expertise":
		item.Expertise = Intermediate
		item.expertiseDeclared = false
	case "preferred_tools":
		item.Profile.PreferredTools = nil
	case "risk_tolerance":
		item.Profile.RiskTolerance = ""
	case "proactive_suggestions":
		item.Profile.ProactiveSuggestions = nil
	case "notification_cadence":
		item.Profile.NotificationCadence = ""
	case "dislikes":
		item.Profile.Dislikes = nil
	case "constraints":
		item.Profile.Constraints = nil
	case "project_principles":
		item.Profile.ProjectPrinciples = nil
	}
}

func cloneSnapshot(snapshot Snapshot) Snapshot {
	copy := snapshot
	copy.Profile.PreferredTools = append([]string(nil), snapshot.Profile.PreferredTools...)
	copy.Profile.Dislikes = append([]string(nil), snapshot.Profile.Dislikes...)
	copy.Profile.Constraints = append([]string(nil), snapshot.Profile.Constraints...)
	copy.Profile.ProjectPrinciples = append([]string(nil), snapshot.Profile.ProjectPrinciples...)
	copy.Profile.PinnedFields = append([]string(nil), snapshot.Profile.PinnedFields...)
	if snapshot.Profile.ConclusionFirst != nil {
		value := *snapshot.Profile.ConclusionFirst
		copy.Profile.ConclusionFirst = &value
	}
	if snapshot.Profile.ProactiveSuggestions != nil {
		value := *snapshot.Profile.ProactiveSuggestions
		copy.Profile.ProactiveSuggestions = &value
	}
	return copy
}

func clamp(value, minimum, maximum float64) float64 {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func rounded(value float64) float64 {
	return math.Round(value*1e12) / 1e12
}
