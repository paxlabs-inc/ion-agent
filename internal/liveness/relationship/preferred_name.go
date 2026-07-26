package relationship

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxPreferredNameRunes = 80

var preferredNameDeclarations = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^(?:my name is|i am|i'm|call me)\s+(.+?)\s*[.!]?$`),
}

// PreferredNameFromDeclaration accepts only a bounded standalone name or an
// explicit self-identification. It never guesses a name from ordinary prose.
func PreferredNameFromDeclaration(content string) (string, bool) {
	value := strings.TrimSpace(content)
	for _, declaration := range preferredNameDeclarations {
		if matches := declaration.FindStringSubmatch(value); len(matches) == 2 {
			value = strings.TrimSpace(matches[1])
			break
		}
	}
	value = strings.Trim(value, `"'`)
	value = strings.Join(strings.Fields(value), " ")
	if value == "" || utf8.RuneCountInString(value) > MaxPreferredNameRunes {
		return "", false
	}
	letterOrNumber := false
	for _, character := range value {
		switch {
		case unicode.IsLetter(character), unicode.IsNumber(character):
			letterOrNumber = true
		case unicode.IsMark(character), unicode.IsSpace(character):
		case character == '\'', character == '’', character == '-', character == '.':
		default:
			return "", false
		}
	}
	if !letterOrNumber {
		return "", false
	}
	return value, true
}
