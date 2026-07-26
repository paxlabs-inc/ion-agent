package operatorapp

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestBrowserAuditArgumentsRedactURLsAndFieldValues(t *testing.T) {
	navigation := redactToolArguments(
		"browser_navigate",
		json.RawMessage(`{"url":"https://example.test/verify?token=secret"}`),
	)
	if bytes.Contains(navigation, []byte("secret")) ||
		bytes.Contains(navigation, []byte("example.test")) {
		t.Fatalf("navigation audit leaked URL: %s", navigation)
	}
	interaction := redactToolArguments(
		"browser_interact",
		json.RawMessage(`{"action":"fill","ref":"p1","value":"private@example.test"}`),
	)
	if bytes.Contains(interaction, []byte("private@example.test")) ||
		!bytes.Contains(interaction, []byte(`"ref":"p1"`)) {
		t.Fatalf("interaction audit redaction = %s", interaction)
	}
}
