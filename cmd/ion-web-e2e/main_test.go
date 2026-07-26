//go:build e2e

package main

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/session"
	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
)

type recordingGenerator struct {
	request protocol.GenerationRequest
}

func (generator *recordingGenerator) Generate(
	_ context.Context,
	request protocol.GenerationRequest,
) (protocol.NormalizedGeneration, error) {
	generator.request = request
	return protocol.NormalizedGeneration{
		Content: "ok", FinishReason: protocol.FinishStop,
	}, nil
}

func TestTranscriptProviderInjectsEncryptedHistoryBeforeCurrentInput(t *testing.T) {
	sessionID := uuid.New()
	history := transcriptMessages([]session.Message{
		{
			SessionID: sessionID, Role: session.RoleUser,
			MemoryType: session.MemoryTranscript, Content: []byte("remember this"),
		},
		{
			SessionID: sessionID, Role: session.RoleAssistant,
			MemoryType: session.MemoryTranscript, Content: []byte("remembered"),
		},
		{
			SessionID: sessionID, Role: session.RoleUser,
			MemoryType: session.MemoryTranscript, Content: []byte("current input"),
		},
	})
	if len(history) != 2 {
		t.Fatalf("history messages = %+v", history)
	}
	inner := &recordingGenerator{}
	provider := transcriptProvider{inner: inner, history: history}
	if _, err := provider.Generate(context.Background(), protocol.GenerationRequest{
		Model: "model",
		Messages: []protocol.Message{
			{Role: protocol.RoleSystem, Content: "system"},
			{Role: protocol.RoleUser, Content: "current input"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	got := inner.request.Messages
	if len(got) != 4 ||
		got[0].Role != protocol.RoleSystem || got[0].Content != "system" ||
		got[1].Role != protocol.RoleUser || got[1].Content != "remember this" ||
		got[2].Role != protocol.RoleAssistant || got[2].Content != "remembered" ||
		got[3].Role != protocol.RoleUser || got[3].Content != "current input" {
		t.Fatalf("provider messages = %+v", got)
	}
}
