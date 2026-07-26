package adversarial

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/paxlabs-inc/ion-agent/internal/security/policy"
	"github.com/paxlabs-inc/ion-agent/internal/security/safety"
	"github.com/paxlabs-inc/ion-agent/internal/tools"
	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

func Test_All44SafetyItemsAreEnforcedThroughLiveToolDispatch(t *testing.T) {
	catalog := safety.NewCatalog()
	limiter, err := policy.NewWindowLimiter(100, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	detector := &liveDetector{}
	auditor := &policy.MemoryAuditor{}
	pipeline, err := policy.NewDefault(types.SystemClock{}, auditor, limiter, detector)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := tools.NewManager(
		types.SystemClock{},
		tools.WithExecutionPolicy(pipeline),
	)
	if err != nil {
		t.Fatal(err)
	}
	executions := make(map[string]int)
	yellowCount := 0
	for _, entry := range catalog.All() {
		classification := tools.ClassificationGreen
		switch entry.Classification {
		case safety.ClassYellow:
			classification = tools.ClassificationYellow
			yellowCount++
		case safety.ClassRed, safety.ClassBlack:
			classification = tools.ClassificationRed
		}
		name := entry.Name
		if err := manager.Register(context.Background(), tools.Registration{
			Name:           name,
			Description:    entry.Description,
			Parameters:     json.RawMessage(`{}`),
			Classification: classification,
			Check:          func(context.Context) error { return nil },
			Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
				executions[name]++
				return json.RawMessage(`{"executed":true}`), nil
			},
		}); err != nil {
			t.Fatal(err)
		}
	}

	for _, entry := range catalog.All() {
		call := protocol.NormalizedToolCall{
			ID: entry.ID, Name: entry.Name, Arguments: json.RawMessage(`{}`),
		}
		unapproved := policy.WithPrincipal(
			context.Background(),
			policy.Principal{Sender: policy.SenderUser},
		)
		_, err := manager.Execute(unapproved, call)
		switch entry.Classification {
		case safety.ClassGreen, safety.ClassYellow:
			if err != nil {
				t.Errorf("%s dispatch error = %v", entry.ID, err)
			}
		case safety.ClassRed, safety.ClassBlack:
			if !errors.Is(err, policy.ErrDenied) {
				t.Errorf("%s unapproved error = %v", entry.ID, err)
			}
		}

		if entry.Classification == safety.ClassRed || entry.Classification == safety.ClassBlack {
			approved := policy.WithPrincipal(
				context.Background(),
				policy.Principal{Sender: policy.SenderUser, Approved: true},
			)
			_, approvedErr := manager.Execute(approved, call)
			if entry.Classification == safety.ClassRed && approvedErr != nil {
				t.Errorf("%s approved RED error = %v", entry.ID, approvedErr)
			}
			if entry.Classification == safety.ClassBlack &&
				!errors.Is(approvedErr, policy.ErrDenied) {
				t.Errorf("%s approved BLACK error = %v", entry.ID, approvedErr)
			}
		}
	}

	for _, entry := range catalog.All() {
		want := 0
		switch entry.Classification {
		case safety.ClassGreen, safety.ClassYellow, safety.ClassRed:
			want = 1
		}
		if executions[entry.Name] != want {
			t.Errorf("%s executions = %d, want %d", entry.ID, executions[entry.Name], want)
		}
	}
	if detector.Count() != yellowCount {
		t.Fatalf("YELLOW detector observations = %d, want %d", detector.Count(), yellowCount)
	}
	if len(catalog.All()) != 44 {
		t.Fatalf("catalog size = %d", len(catalog.All()))
	}
}

type liveDetector struct {
	mu    sync.Mutex
	count int
}

func (detector *liveDetector) Observe(context.Context, policy.Request) error {
	detector.mu.Lock()
	detector.count++
	detector.mu.Unlock()
	return nil
}

func (detector *liveDetector) Count() int {
	detector.mu.Lock()
	defer detector.mu.Unlock()
	return detector.count
}
