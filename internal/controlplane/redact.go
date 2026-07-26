package controlplane

import (
	"encoding/json"
	"fmt"
	"strings"
)

const redactedValue = "[REDACTED]"

// JSONRedactor recursively removes known credential and secret fields.
type JSONRedactor struct{}

// Redact returns a detached, server-redacted JSON value.
func (JSONRedactor) Redact(payload json.RawMessage) (json.RawMessage, error) {
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	if len(payload) > MaximumPayloadBytes || !json.Valid(payload) {
		return nil, fmt.Errorf("controlplane: cannot redact invalid payload")
	}
	var value any
	if err := json.Unmarshal(payload, &value); err != nil {
		return nil, fmt.Errorf("controlplane: decode payload for redaction: %w", err)
	}
	redactValue(value)
	redacted, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("controlplane: encode redacted payload: %w", err)
	}
	return redacted, nil
}

func redactValue(value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if sensitiveKey(key) {
				typed[key] = redactedValue
				continue
			}
			redactValue(child)
		}
	case []any:
		for _, child := range typed {
			redactValue(child)
		}
	}
}

func sensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
	for _, fragment := range []string{
		"password", "passwd", "secret", "access_token", "refresh_token",
		"bearer", "authorization", "credential", "cookie", "private_key",
		"key_material", "prompt_value",
	} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}
