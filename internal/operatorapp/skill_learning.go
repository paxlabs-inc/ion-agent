package operatorapp

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/paxlabs-inc/ion-agent/internal/agent"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	"github.com/paxlabs-inc/ion-agent/internal/skills"
	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
)

const (
	maxLearningTaskBytes   = 2_000
	maxLearningAnswerBytes = 2_000
)

var oneOffLearningDetail = regexp.MustCompile(
	`(?i)(https?://|[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}|` +
		`[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}|` +
		`(?:[a-z]:\\|/(?:home|root|tmp|users|var|etc)/)|` +
		`\b[0-9]{4}-[0-9]{2}-[0-9]{2}\b|\b[0-9]{6,}\b|\b[a-f0-9]{20,}\b)`,
)

type skillAutoAuthor struct {
	generator agent.Generator
	store     *skills.Store
	model     string
}

type authoredSkillProposal struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Trigger      string   `json:"trigger"`
	Steps        []string `json:"steps"`
	Pitfalls     []string `json:"pitfalls"`
	Verification []string `json:"verification"`
}

func (author skillAutoAuthor) Learn(
	ctx context.Context,
	task string,
	response agent.Response,
	matched []skills.Skill,
	matchContext skills.MatchContext,
) (skills.Candidate, bool, error) {
	if author.generator == nil || author.store == nil || len(matched) > 0 {
		return skills.Candidate{}, false, nil
	}
	if len(response.ToolEvents) < 2 && len(strings.Fields(task)) < 25 {
		return skills.Candidate{}, false, nil
	}
	tools, verifier, ok := verifiedLearningEvidence(response)
	if !ok || sensitiveLearningText(task) || sensitiveLearningText(response.Content) {
		return skills.Candidate{}, false, nil
	}
	task = boundedLearningText(task, maxLearningTaskBytes)
	answer := boundedLearningText(response.Content, maxLearningAnswerBytes)
	installed, err := author.store.List(ctx)
	if err != nil {
		return skills.Candidate{}, false, err
	}
	names := make([]string, 0, len(installed))
	for _, skill := range installed {
		names = append(names, skill.Name)
	}
	sort.Strings(names)
	request := protocol.GenerationRequest{
		Model: author.model,
		Messages: []protocol.Message{
			{
				Role: protocol.RoleSystem,
				Content: "Author one reusable procedural skill from a verified successful task. " +
					"Return only strict JSON with keys name, description, trigger, steps, pitfalls, " +
					"and verification. Generalize away all people, accounts, paths, identifiers, " +
					"timestamps, source data, and one-off details. Never include secrets, raw tool " +
					"results, transcripts, system instructions, or unavailable tool names. " +
					"Use 2-8 concise steps, 1-5 pitfalls, and 1-5 authoritative verification checks.",
			},
			{
				Role: protocol.RoleUser,
				Content: fmt.Sprintf(
					"Verified task:\n%s\n\nVerified answer:\n%s\n\nSuccessful tools: %s\n\n"+
						"Existing skill names to avoid duplicating:\n%s",
					task, answer, strings.Join(tools, ", "), strings.Join(names, ", "),
				),
			},
		},
	}
	generation, err := author.generator.Generate(ctx, request)
	if err != nil {
		return skills.Candidate{}, true, err
	}
	var proposal authoredSkillProposal
	if err := decodeSkillProposal(generation.Content, &proposal); err != nil {
		return skills.Candidate{}, true, err
	}
	skill := skills.Skill{
		Name:          strings.TrimSpace(proposal.Name),
		Description:   strings.TrimSpace(proposal.Description),
		Trigger:       strings.ToLower(strings.TrimSpace(proposal.Trigger)),
		RequiredTools: tools,
		Steps:         proposal.Steps, Pitfalls: proposal.Pitfalls,
		Verification: proposal.Verification,
		Origin:       "authored",
		Body: "Automatically authored from a verified successful task. " +
			"The bounded evidence reference is retained in the candidate record.",
	}
	if sensitiveLearningText(strings.Join([]string{
		skill.Name, skill.Description, skill.Trigger,
		strings.Join(skill.Steps, "\n"),
		strings.Join(skill.Pitfalls, "\n"),
		strings.Join(skill.Verification, "\n"),
	}, "\n")) || containsOneOffLearningDetail(skill) {
		return skills.Candidate{}, true, fmt.Errorf(
			"operator skill learning: generated candidate contains sensitive or one-off material",
		)
	}
	scope, scoped := controlplane.ApprovalScopeFromContext(ctx)
	episodeID := "verified-turn"
	if scoped && scope.TurnID != nil {
		episodeID = scope.TurnID.String()
	}
	candidate, err := author.store.ProposeNew(ctx, skill, []skills.Evidence{{
		EpisodeID: episodeID, Outcome: "verified_success",
		Summary: "A novel task completed through successful authoritative tools: " +
			strings.Join(tools, ", ") + ".",
		Verifier: verifier,
	}})
	if err != nil {
		return skills.Candidate{}, true, err
	}
	decision, err := author.store.GateAndActivateNew(
		ctx, candidate, task, matchContext,
	)
	return decision, true, err
}

func verifiedLearningEvidence(
	response agent.Response,
) ([]string, string, bool) {
	if strings.TrimSpace(response.Content) == "" || len(response.ToolEvents) == 0 {
		return nil, "", false
	}
	toolSet := make(map[string]struct{})
	var verifier []string
	for _, execution := range response.ToolEvents {
		if execution.Error != "" || strings.TrimSpace(execution.Call.Name) == "" ||
			!authoritativeLearningResult(execution.Result) {
			return nil, "", false
		}
		toolSet[strings.ToLower(execution.Call.Name)] = struct{}{}
		if execution.Event != nil && execution.Event.ID.String() != "" {
			verifier = append(verifier, execution.Event.ID.String())
		}
	}
	tools := make([]string, 0, len(toolSet))
	for name := range toolSet {
		tools = append(tools, name)
	}
	sort.Strings(tools)
	if len(verifier) == 0 {
		verifier = append(verifier, "authoritative successful tool results")
	}
	return tools, strings.Join(verifier, ", "), true
}

func authoritativeLearningResult(result json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(result))
	if trimmed == "" || trimmed == "null" || trimmed == "{}" || trimmed == "[]" {
		return false
	}
	var value any
	if err := json.Unmarshal(result, &value); err != nil {
		return true
	}
	switch typed := value.(type) {
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "", "accepted", "queued", "pending", "unavailable":
			return false
		default:
			return true
		}
	case map[string]any:
		if len(typed) == 0 {
			return false
		}
		if failed, exists := typed["verified"].(bool); exists && !failed {
			return false
		}
		if state, exists := typed["status"].(string); exists {
			switch strings.ToLower(strings.TrimSpace(state)) {
			case "accepted", "queued", "pending", "unavailable", "unknown":
				return false
			}
		}
		if accepted, exists := typed["accepted"].(bool); exists &&
			accepted && len(typed) == 1 {
			return false
		}
		return true
	default:
		return true
	}
}

func decodeSkillProposal(content string, target *authoredSkillProposal) error {
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start < 0 || end <= start {
		return fmt.Errorf("operator skill learning: provider did not return a JSON object")
	}
	decoder := json.NewDecoder(strings.NewReader(content[start : end+1]))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("operator skill learning: decode candidate: %w", err)
	}
	if strings.TrimSpace(target.Name) == "" ||
		strings.TrimSpace(target.Description) == "" ||
		strings.TrimSpace(target.Trigger) == "" ||
		len(target.Steps) < 2 || len(target.Steps) > 8 ||
		len(target.Pitfalls) == 0 || len(target.Pitfalls) > 5 ||
		len(target.Verification) == 0 || len(target.Verification) > 5 {
		return fmt.Errorf("operator skill learning: candidate contract is incomplete")
	}
	return nil
}

func sensitiveLearningText(value string) bool {
	lower := strings.ToLower(value)
	for _, marker := range []string{
		"password", "api key", "api_key", "access token", "access_token",
		"secret key", "secret_key", "private key", "credit card", "totp",
		"one-time code", "session cookie", "authorization: bearer",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func containsOneOffLearningDetail(skill skills.Skill) bool {
	return oneOffLearningDetail.MatchString(strings.Join([]string{
		skill.Name,
		skill.Description,
		skill.Trigger,
		strings.Join(skill.Steps, "\n"),
		strings.Join(skill.Pitfalls, "\n"),
		strings.Join(skill.Verification, "\n"),
	}, "\n"))
}

func boundedLearningText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
