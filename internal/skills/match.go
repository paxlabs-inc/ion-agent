package skills

import (
	"runtime"
	"sort"
	"strings"
	"unicode"
)

const minimumMatchScore = 12

var matchStopwords = map[string]struct{}{
	"about": {}, "after": {}, "again": {}, "also": {}, "another": {},
	"before": {}, "could": {}, "from": {}, "have": {}, "help": {},
	"into": {}, "need": {}, "please": {}, "task": {}, "that": {},
	"this": {}, "using": {}, "want": {}, "what": {}, "when": {},
	"where": {}, "which": {}, "with": {}, "would": {},
}

func skillApplicable(skill Skill, matchContext MatchContext) bool {
	platform := normalizePlatform(matchContext.Platform)
	if platform == "" {
		platform = normalizePlatform(runtime.GOOS)
	}
	if len(skill.Platforms) > 0 {
		supported := false
		for _, candidate := range skill.Platforms {
			candidate = normalizePlatform(candidate)
			if candidate == "" || candidate == "all" || candidate == "any" ||
				candidate == "cross-platform" || candidate == platform {
				supported = true
				break
			}
		}
		if !supported {
			return false
		}
	}
	if len(matchContext.Tools) == 0 {
		return true
	}
	for _, tool := range unique(skill.RequiredTools) {
		if _, exists := matchContext.Tools[strings.ToLower(tool)]; !exists {
			return false
		}
	}
	return true
}

func normalizePlatform(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "darwin", "mac", "macos", "osx":
		return "darwin"
	case "win", "windows", "win32":
		return "windows"
	case "linux", "ubuntu", "debian":
		return "linux"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func skillMatchScore(skill Skill, problem string) int {
	problem = strings.ToLower(strings.TrimSpace(problem))
	if problem == "" {
		return 0
	}
	score := 0
	for _, phrase := range append([]string{skill.Trigger, skill.Name}, skill.Aliases...) {
		phrase = strings.ToLower(strings.TrimSpace(phrase))
		if len(phrase) >= 3 && strings.Contains(problem, phrase) {
			score += 80 + len(tokenize(phrase))*4
		}
	}
	problemTokens := tokenSet(problem)
	for _, field := range []struct {
		value  string
		weight int
	}{
		{skill.Name, 8},
		{skill.Trigger, 7},
		{strings.Join(skill.Aliases, " "), 6},
		{skill.Description, 4},
		{skill.Category, 3},
	} {
		for _, token := range tokenize(field.value) {
			if _, exists := problemTokens[token]; exists {
				score += field.weight
			}
		}
	}
	return score
}

func tokenize(value string) []string {
	normalized := strings.Map(func(char rune) rune {
		if unicode.IsLetter(char) || unicode.IsDigit(char) {
			return unicode.ToLower(char)
		}
		return ' '
	}, value)
	seen := make(map[string]struct{})
	result := make([]string, 0)
	for _, token := range strings.Fields(normalized) {
		if len(token) < 3 {
			continue
		}
		if _, stopped := matchStopwords[token]; stopped {
			continue
		}
		if _, exists := seen[token]; exists {
			continue
		}
		seen[token] = struct{}{}
		result = append(result, token)
	}
	sort.Strings(result)
	return result
}

func tokenSet(value string) map[string]struct{} {
	result := make(map[string]struct{})
	for _, token := range tokenize(value) {
		result[token] = struct{}{}
	}
	return result
}
