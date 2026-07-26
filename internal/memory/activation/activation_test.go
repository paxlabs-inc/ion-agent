package activation

import (
	"testing"
)

func Test_Composer_BasicComposition(t *testing.T) {
	c := NewComposer(1000)
	c.Add(TierPinned, "identity: I am Ion", 1.0)
	c.Add(TierRecent, "recent context", 0.8)
	c.Add(TierTranscript, "user: hello", 0.5)

	result := c.Compose()
	if len(result) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(result))
	}

	// Pinned should be first.
	if result[0].Tier != TierPinned {
		t.Fatalf("expected pinned first, got %s", result[0].Tier)
	}
}

func Test_Composer_TrimByBudget(t *testing.T) {
	// Very small budget.
	c := NewComposer(10)
	c.Add(TierPinned, "pinned content", 1.0)
	c.Add(TierRecent, "low salience", 0.1)
	c.Add(TierRecent, "high salience", 0.9)

	result := c.Compose()

	// Pinned should always be included.
	foundPinned := false
	for _, entry := range result {
		if entry.Tier == TierPinned {
			foundPinned = true
		}
	}
	if !foundPinned {
		t.Fatal("pinned entry should never be trimmed")
	}
}

func Test_Composer_TrimsLowestSalienceFirst(t *testing.T) {
	c := NewComposer(3)
	c.Add(TierPinned, "p", 1)
	c.Add(TierRecent, "12345678", 0.1)
	c.Add(TierRecent, "abcdefgh", 0.9)
	result := c.Compose()
	if len(result) != 2 {
		t.Fatalf("composition = %+v", result)
	}
	if result[0].Tier != TierPinned || result[1].Content != "abcdefgh" {
		t.Fatalf("lowest-salience entry was not trimmed first: %+v", result)
	}
}

func Test_Composer_PinnedMayExceedBudgetButIsNeverDropped(t *testing.T) {
	c := NewComposer(1)
	c.Add(TierPinned, "identity and immutable constraints", 1)
	c.Add(TierRecent, "recent", 0.9)
	result := c.Compose()
	if len(result) != 1 || result[0].Tier != TierPinned {
		t.Fatalf("composition = %+v", result)
	}
	if result[0].Tokens <= c.TokenBudget() {
		t.Fatal("test setup did not exceed budget")
	}
}

func Test_Composer_DefaultBudget(t *testing.T) {
	c := NewComposer(0)
	if c.TokenBudget() != DefaultTokenBudget {
		t.Fatalf("expected default budget %d, got %d", DefaultTokenBudget, c.TokenBudget())
	}
}

func Test_Composer_Clear(t *testing.T) {
	c := NewComposer(1000)
	c.Add(TierPinned, "test", 1.0)
	c.Clear()
	if c.EntryCount() != 0 {
		t.Fatal("expected 0 entries after clear")
	}
}

func Test_Render(t *testing.T) {
	entries := []Entry{
		{Tier: TierPinned, Content: "identity", Salience: 1.0},
		{Tier: TierRecent, Content: "recent", Salience: 0.8},
	}
	rendered := Render(entries)
	if rendered == "" {
		t.Fatal("expected non-empty render")
	}
	if !contains(rendered, "## pinned") {
		t.Fatal("expected pinned header")
	}
}

func Test_EstimateTokens(t *testing.T) {
	if tokens := estimateTokens("hello"); tokens < 1 {
		t.Fatal("expected at least 1 token")
	}
	if tokens := estimateTokens(""); tokens < 1 {
		t.Fatal("expected at least 1 token for empty string")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && findSubstr(s, sub))
}

func findSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
