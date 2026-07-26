package policy

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/paxlabs-inc/ion-agent/internal/security/safety"
	"github.com/paxlabs-inc/ion-agent/internal/tools"
	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
)

type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time { return clock.now }

func TestPipelineOrderModifyAndFirstDeny(t *testing.T) {
	t.Parallel()
	auditor := &MemoryAuditor{}
	var evaluated []LayerName
	layer := func(name LayerName, result Result) Layer {
		return LayerFunc{
			LayerName: name,
			EvaluateFunc: func(_ context.Context, request Request) (Result, error) {
				evaluated = append(evaluated, name)
				if result.Decision == Modify {
					result.ModifiedCall = request.Invocation.Call
					result.ModifiedCall.Arguments = json.RawMessage(`{"sanitized":true}`)
				}
				return result, nil
			},
		}
	}
	pipeline, err := New(
		fixedClock{time.Unix(10, 0)},
		auditor,
		layer(SandboxLayer, Result{Decision: Allow}),
		layer(ProfileLayer, Result{Decision: Modify}),
		layer(ProviderLayer, Result{Decision: Deny, Reason: "quota exhausted"}),
		layer(SenderLayer, Result{Decision: Allow}),
		layer(GroupLayer, Result{Decision: Allow}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = pipeline.Authorize(context.Background(), invocation(tools.ClassificationGreen))
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("Authorize() error = %v", err)
	}
	if want := []LayerName{SandboxLayer, ProfileLayer, ProviderLayer}; !reflect.DeepEqual(evaluated, want) {
		t.Fatalf("evaluated = %v, want %v", evaluated, want)
	}
	events := auditor.Events()
	if len(events) != 1 || events[0].Layer != ProviderLayer ||
		events[0].Reason != "quota exhausted" {
		t.Fatalf("events = %+v", events)
	}
}

func TestPipelineEvaluatesAllFiveLayersInBindingOrder(t *testing.T) {
	t.Parallel()
	var evaluated []LayerName
	layer := func(name LayerName) Layer {
		return LayerFunc{
			LayerName: name,
			EvaluateFunc: func(context.Context, Request) (Result, error) {
				evaluated = append(evaluated, name)
				return Result{Decision: Allow}, nil
			},
		}
	}
	pipeline, err := New(
		fixedClock{time.Unix(5, 0)},
		&MemoryAuditor{},
		layer(SandboxLayer),
		layer(ProfileLayer),
		layer(ProviderLayer),
		layer(SenderLayer),
		layer(GroupLayer),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pipeline.Authorize(
		context.Background(),
		invocation(tools.ClassificationGreen),
	); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(evaluated, requiredOrder[:]) {
		t.Fatalf("evaluated = %v, want %v", evaluated, requiredOrder)
	}
}

func TestRealToolDispatchTraversesPipelineBeforeHandler(t *testing.T) {
	t.Parallel()
	var evaluated []LayerName
	layer := func(name LayerName) Layer {
		return LayerFunc{
			LayerName: name,
			EvaluateFunc: func(_ context.Context, request Request) (Result, error) {
				evaluated = append(evaluated, name)
				if name == GroupLayer {
					modified := request.Invocation.Call
					modified.Arguments = json.RawMessage(`{"policy":"modified"}`)
					return Result{Decision: Modify, ModifiedCall: modified}, nil
				}
				return Result{Decision: Allow}, nil
			},
		}
	}
	pipeline, err := New(
		fixedClock{time.Unix(5, 0)},
		&MemoryAuditor{},
		layer(SandboxLayer),
		layer(ProfileLayer),
		layer(ProviderLayer),
		layer(SenderLayer),
		layer(GroupLayer),
	)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := tools.NewManager(
		fixedClock{time.Unix(5, 0)},
		tools.WithExecutionPolicy(pipeline),
	)
	if err != nil {
		t.Fatal(err)
	}
	var handled json.RawMessage
	if err := manager.Register(context.Background(), tools.Registration{
		Name:           "example",
		Description:    "Exercise the real dispatch path.",
		Parameters:     json.RawMessage(`{}`),
		Classification: tools.ClassificationGreen,
		Check:          func(context.Context) error { return nil },
		Handler: func(_ context.Context, arguments json.RawMessage) (json.RawMessage, error) {
			handled = append(json.RawMessage(nil), arguments...)
			return json.RawMessage(`{"ok":true}`), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	result, err := manager.Execute(
		WithPrincipal(context.Background(), Principal{Sender: SenderUser}),
		invocation(tools.ClassificationGreen).Call,
	)
	if err != nil {
		t.Fatal(err)
	}
	if string(result) != `{"ok":true}` || string(handled) != `{"policy":"modified"}` {
		t.Fatalf("result = %s, handled = %s", result, handled)
	}
	if !reflect.DeepEqual(evaluated, requiredOrder[:]) {
		t.Fatalf("evaluated = %v, want %v", evaluated, requiredOrder)
	}
}

func TestRealToolDispatchBlocksIdleRedAndLogsDenial(t *testing.T) {
	t.Parallel()
	auditor := &MemoryAuditor{}
	pipeline, err := NewDefault(fixedClock{time.Unix(8, 0)}, auditor, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := tools.NewManager(
		fixedClock{time.Unix(8, 0)},
		tools.WithExecutionPolicy(pipeline),
	)
	if err != nil {
		t.Fatal(err)
	}
	handled := false
	if err := manager.Register(context.Background(), tools.Registration{
		Name:                    "example",
		Description:             "A consequential external action.",
		Parameters:              json.RawMessage(`{}`),
		Classification:          tools.ClassificationRed,
		ExternallyCommunicating: true,
		Check:                   func(context.Context) error { return nil },
		Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			handled = true
			return json.RawMessage(`null`), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	ctx := WithPrincipal(context.Background(), Principal{
		Sender:   SenderAutomatrix,
		Approved: true,
	})
	if _, err := manager.Execute(
		ctx,
		invocation(tools.ClassificationRed).Call,
	); !errors.Is(err, tools.ErrPolicyDenied) || !errors.Is(err, ErrDenied) {
		t.Fatalf("Execute() error = %v", err)
	}
	if handled {
		t.Fatal("RED handler ran for idle-time sender")
	}
	events := auditor.Events()
	if len(events) != 1 || events[0].Layer != SandboxLayer ||
		events[0].Reason != "idle-time sender cannot execute RED tools" {
		t.Fatalf("events = %+v", events)
	}
}

func TestLayerCannotModifyCallWithoutModifyDecision(t *testing.T) {
	t.Parallel()
	pass := func(name LayerName) Layer {
		return LayerFunc{
			LayerName: name,
			EvaluateFunc: func(context.Context, Request) (Result, error) {
				return Result{Decision: Allow}, nil
			},
		}
	}
	mutatingAllow := LayerFunc{
		LayerName: ProfileLayer,
		EvaluateFunc: func(_ context.Context, request Request) (Result, error) {
			request.Invocation.Call.Arguments[0] = '['
			return Result{Decision: Allow}, nil
		},
	}
	pipeline, err := New(
		fixedClock{time.Unix(5, 0)},
		&MemoryAuditor{},
		pass(SandboxLayer),
		mutatingAllow,
		pass(ProviderLayer),
		pass(SenderLayer),
		pass(GroupLayer),
	)
	if err != nil {
		t.Fatal(err)
	}
	call := invocation(tools.ClassificationGreen)
	authorized, err := pipeline.Authorize(context.Background(), call)
	if err != nil {
		t.Fatal(err)
	}
	if string(authorized.Arguments) != `{"value":1}` {
		t.Fatalf("authorized arguments = %s", authorized.Arguments)
	}
}

func TestSafetyClassificationAndIdleTimeRules(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		principal  Principal
		invocation tools.Invocation
		allowed    bool
	}{
		{
			name:       "green interactive",
			principal:  Principal{Sender: SenderUser},
			invocation: invocation(tools.ClassificationGreen),
			allowed:    true,
		},
		{
			name:       "red requires approval",
			principal:  Principal{Sender: SenderUser},
			invocation: invocation(tools.ClassificationRed),
		},
		{
			name:       "red approved",
			principal:  Principal{Sender: SenderUser, Approved: true},
			invocation: invocation(tools.ClassificationRed),
			allowed:    true,
		},
		{
			name:       "unknown classification",
			principal:  Principal{Sender: SenderUser},
			invocation: invocation("BLUE"),
		},
		{
			name:       "idle red cannot be approved",
			principal:  Principal{Sender: SenderAutomatrix, Approved: true},
			invocation: invocation(tools.ClassificationRed),
		},
		{
			name:      "idle external",
			principal: Principal{Sender: SenderDreamweaver},
			invocation: func() tools.Invocation {
				value := invocation(tools.ClassificationGreen)
				value.ExternallyCommunicating = true
				return value
			}(),
		},
		{
			name:      "scheduled external",
			principal: Principal{Sender: SenderScheduler},
			invocation: func() tools.Invocation {
				value := invocation(tools.ClassificationGreen)
				value.ExternallyCommunicating = true
				return value
			}(),
		},
		{
			name:       "scheduled red cannot be approved",
			principal:  Principal{Sender: SenderScheduler, Approved: true},
			invocation: invocation(tools.ClassificationRed),
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			pipeline, err := NewDefault(
				fixedClock{time.Unix(1, 0)},
				&MemoryAuditor{},
				nil,
				nil,
			)
			if err != nil {
				t.Fatalf("NewDefault() error = %v", err)
			}
			ctx := WithPrincipal(context.Background(), test.principal)
			_, err = pipeline.Authorize(ctx, test.invocation)
			if test.allowed && err != nil {
				t.Fatalf("Authorize() error = %v", err)
			}
			if !test.allowed && !errors.Is(err, ErrDenied) {
				t.Fatalf("Authorize() error = %v, want denial", err)
			}
		})
	}
}

func TestCatalogPreventsKnownActionClassificationDowngrade(t *testing.T) {
	t.Parallel()
	pipeline, err := NewDefault(
		fixedClock{time.Unix(1, 0)},
		&MemoryAuditor{},
		nil,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	call := invocation(tools.ClassificationGreen)
	call.Call.Name = "payment"
	if _, err := pipeline.Authorize(
		WithPrincipal(context.Background(), Principal{
			Sender:   SenderUser,
			Approved: true,
		}),
		call,
	); !errors.Is(err, ErrDenied) {
		t.Fatalf("under-classified payment error = %v", err)
	}
}

func TestCatalogSafetyBoundaryCannotBeApproved(t *testing.T) {
	t.Parallel()
	pipeline, err := NewDefault(
		fixedClock{time.Unix(1, 0)},
		&MemoryAuditor{},
		nil,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	call := invocation(tools.ClassificationRed)
	call.Call.Name = "audit_delete"
	if _, err := pipeline.Authorize(
		WithPrincipal(context.Background(), Principal{
			Sender:   SenderUser,
			Approved: true,
		}),
		call,
	); !errors.Is(err, ErrDenied) {
		t.Fatalf("approved audit deletion error = %v", err)
	}
}

func TestCatalogClassificationAndRiskMappingsAreClosed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		classification safety.Classification
		want           tools.Classification
	}{
		{classification: safety.ClassGreen, want: tools.ClassificationGreen},
		{classification: safety.ClassYellow, want: tools.ClassificationYellow},
		{classification: safety.ClassRed, want: tools.ClassificationRed},
		{classification: safety.ClassBlack, want: tools.ClassificationRed},
		{classification: safety.Classification(99), want: ""},
	}
	for _, test := range tests {
		if got := catalogClassification(test.classification); got != test.want {
			t.Fatalf("catalogClassification(%d) = %s, want %s",
				test.classification, got, test.want)
		}
	}
	if classificationRisk(tools.ClassificationGreen) != 1 ||
		classificationRisk(tools.ClassificationYellow) != 2 ||
		classificationRisk(tools.ClassificationRed) != 3 ||
		classificationRisk("BLUE") != 0 {
		t.Fatal("classification risk ordering is incorrect")
	}
}

func TestPipelineInvalidCallIsDeniedAndAudited(t *testing.T) {
	t.Parallel()
	auditor := &MemoryAuditor{}
	pipeline, err := NewDefault(fixedClock{time.Unix(1, 0)}, auditor, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	call := invocation(tools.ClassificationGreen)
	call.Call.Arguments = json.RawMessage(`[]`)
	if _, err := pipeline.Authorize(context.Background(), call); !errors.Is(err, ErrDenied) {
		t.Fatalf("invalid call error = %v", err)
	}
	if events := auditor.Events(); len(events) != 1 ||
		events[0].Layer != SandboxLayer {
		t.Fatalf("audit events = %+v", events)
	}
}

func TestYellowCallsAreRateLimitedDetectedAndAudited(t *testing.T) {
	t.Parallel()
	limiter, err := NewWindowLimiter(1, time.Minute)
	if err != nil {
		t.Fatalf("NewWindowLimiter() error = %v", err)
	}
	auditor := &MemoryAuditor{}
	detector := &recordingDetector{}
	pipeline, err := NewDefault(fixedClock{time.Unix(1, 0)}, auditor, limiter, detector)
	if err != nil {
		t.Fatalf("NewDefault() error = %v", err)
	}
	call := invocation(tools.ClassificationYellow)
	if _, err := pipeline.Authorize(context.Background(), call); err != nil {
		t.Fatalf("first Authorize() error = %v", err)
	}
	if _, err := pipeline.Authorize(context.Background(), call); !errors.Is(err, ErrDenied) {
		t.Fatalf("second Authorize() error = %v", err)
	}
	events := auditor.Events()
	if len(events) != 2 || events[0].Decision != Allow || events[1].Decision != Deny {
		t.Fatalf("events = %+v", events)
	}
	if detector.calls != 1 {
		t.Fatalf("anomaly detector calls = %d, want 1", detector.calls)
	}
}

func TestApprovedRedCallIsAuditedBeforeExecution(t *testing.T) {
	t.Parallel()
	auditor := &MemoryAuditor{}
	pipeline, err := NewDefault(fixedClock{time.Unix(1, 0)}, auditor, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithPrincipal(context.Background(), Principal{
		Sender: SenderUser, Approved: true,
	})
	call := invocation(tools.ClassificationRed)
	if _, err := pipeline.Authorize(ctx, call); err != nil {
		t.Fatal(err)
	}
	events := auditor.Events()
	if len(events) != 1 || events[0].Decision != Allow ||
		events[0].Reason != "RED approved" {
		t.Fatalf("events = %+v", events)
	}
}

func TestYellowMonitoringFailsClosedAndDetectsAnomaly(t *testing.T) {
	t.Parallel()
	limiter, err := NewWindowLimiter(2, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		limiter  RateLimiter
		detector AnomalyDetector
	}{
		{name: "missing limiter", detector: &recordingDetector{}},
		{name: "missing detector", limiter: limiter},
		{
			name:     "detected anomaly",
			limiter:  limiter,
			detector: &recordingDetector{err: errors.New("argument spike")},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			auditor := &MemoryAuditor{}
			pipeline, newErr := NewDefault(
				fixedClock{time.Unix(1, 0)},
				auditor,
				test.limiter,
				test.detector,
			)
			if newErr != nil {
				t.Fatal(newErr)
			}
			if _, authorizeErr := pipeline.Authorize(
				context.Background(),
				invocation(tools.ClassificationYellow),
			); !errors.Is(authorizeErr, ErrDenied) {
				t.Fatalf("Authorize() error = %v", authorizeErr)
			}
			events := auditor.Events()
			if len(events) != 1 || events[0].Decision != Deny {
				t.Fatalf("events = %+v", events)
			}
		})
	}
}

func TestIdleAndUnknownSendersCannotBypassSafety(t *testing.T) {
	t.Parallel()
	for _, sender := range []Sender{"unknown", SenderAutomatrix, SenderCuriosity, SenderDreamweaver} {
		sender := sender
		t.Run(string(sender), func(t *testing.T) {
			t.Parallel()
			call := invocation(tools.ClassificationRed)
			pipeline, err := NewDefault(
				fixedClock{time.Unix(1, 0)},
				&MemoryAuditor{},
				nil,
				nil,
			)
			if err != nil {
				t.Fatal(err)
			}
			ctx := WithPrincipal(context.Background(), Principal{
				Sender:   sender,
				Approved: true,
			})
			if _, err := pipeline.Authorize(ctx, call); !errors.Is(err, ErrDenied) {
				t.Fatalf("Authorize() error = %v", err)
			}
		})
	}
}

func TestEveryLayerFailureIsAuditedWithModifiedCallDetails(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		result Result
		err    error
	}{
		{name: "deny without reason", result: Result{Decision: Deny}},
		{name: "evaluation error", err: errors.New("broken evaluator")},
		{name: "invalid decision", result: Result{}},
		{name: "invalid modification", result: Result{
			Decision: Modify,
			ModifiedCall: protocol.NormalizedToolCall{
				ID: "different", Name: "different", Arguments: json.RawMessage(`{}`),
			},
		}},
		{name: "malformed modification", result: Result{
			Decision: Modify,
			ModifiedCall: protocol.NormalizedToolCall{
				ID: "call-1", Name: "example", Arguments: json.RawMessage(`[]`),
			},
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			auditor := &MemoryAuditor{}
			failing := LayerFunc{
				LayerName: ProviderLayer,
				EvaluateFunc: func(context.Context, Request) (Result, error) {
					return test.result, test.err
				},
			}
			pass := func(name LayerName) Layer {
				return LayerFunc{
					LayerName: name,
					EvaluateFunc: func(context.Context, Request) (Result, error) {
						return Result{Decision: Allow}, nil
					},
				}
			}
			pipeline, err := New(
				fixedClock{time.Unix(7, 0)},
				auditor,
				pass(SandboxLayer),
				pass(ProfileLayer),
				failing,
				pass(SenderLayer),
				pass(GroupLayer),
			)
			if err != nil {
				t.Fatal(err)
			}
			ctx := WithPrincipal(context.Background(), Principal{
				Sender: SenderUser, Profile: "default", Provider: "openai", Group: "owners",
			})
			if _, err := pipeline.Authorize(
				ctx,
				invocation(tools.ClassificationGreen),
			); !errors.Is(err, ErrDenied) {
				t.Fatalf("Authorize() error = %v", err)
			}
			events := auditor.Events()
			if len(events) != 1 || events[0].Layer != ProviderLayer ||
				events[0].Reason == "" || string(events[0].Arguments) != `{"value":1}` ||
				events[0].Profile != "default" || events[0].Provider != "openai" ||
				events[0].Group != "owners" || !events[0].At.Equal(time.Unix(7, 0)) {
				t.Fatalf("event = %+v", events)
			}
		})
	}
}

func TestPipelineRequiresClosedLayerOrder(t *testing.T) {
	t.Parallel()
	pass := func(name LayerName) Layer {
		return LayerFunc{
			LayerName: name,
			EvaluateFunc: func(context.Context, Request) (Result, error) {
				return Result{Decision: Allow}, nil
			},
		}
	}
	_, err := New(
		fixedClock{},
		&MemoryAuditor{},
		pass(ProfileLayer),
		pass(SandboxLayer),
		pass(ProviderLayer),
		pass(SenderLayer),
		pass(GroupLayer),
	)
	if err == nil {
		t.Fatal("New() accepted reordered layers")
	}
	if _, err := New(nil, &MemoryAuditor{}, nil, nil, nil, nil, nil); err == nil {
		t.Fatal("New(nil clock) succeeded")
	}
	if _, err := New(fixedClock{}, nil, nil, nil, nil, nil, nil); err == nil {
		t.Fatal("New(nil auditor) succeeded")
	}
	if _, err := New(fixedClock{}, &MemoryAuditor{}, pass(SandboxLayer)); err == nil {
		t.Fatal("New(with fewer than five layers) succeeded")
	}
	if _, err := (LayerFunc{LayerName: SandboxLayer}).Evaluate(
		context.Background(),
		Request{},
	); err == nil {
		t.Fatal("LayerFunc without evaluator succeeded")
	}
}

func TestAuditFailurePreventsExecution(t *testing.T) {
	t.Parallel()
	pipeline, err := NewDefault(
		fixedClock{time.Unix(1, 0)},
		errorAuditor{},
		nil,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pipeline.Authorize(
		context.Background(),
		invocation(tools.ClassificationRed),
	); !errors.Is(err, ErrDenied) {
		t.Fatalf("RED Authorize() error = %v", err)
	}

	limiter, err := NewWindowLimiter(1, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	pipeline, err = NewDefault(
		fixedClock{time.Unix(1, 0)},
		errorAuditor{},
		limiter,
		&recordingDetector{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pipeline.Authorize(
		context.Background(),
		invocation(tools.ClassificationYellow),
	); !errors.Is(err, ErrDenied) {
		t.Fatalf("YELLOW Authorize() error = %v", err)
	}
}

func TestPrincipalDefaultsAndCancelledAuthorization(t *testing.T) {
	t.Parallel()
	if principal := PrincipalFromContext(context.Background()); principal.Sender != SenderUser {
		t.Fatalf("default principal = %+v", principal)
	}
	ctx := WithPrincipal(context.Background(), Principal{})
	if principal := PrincipalFromContext(ctx); principal.Sender != SenderUser {
		t.Fatalf("empty sender principal = %+v", principal)
	}
	pipeline, err := NewDefault(fixedClock{}, &MemoryAuditor{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := pipeline.Authorize(
		cancelled,
		invocation(tools.ClassificationGreen),
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("Authorize(cancelled) error = %v", err)
	}
}

func TestWindowLimiterValidationExpiryAndCancellation(t *testing.T) {
	t.Parallel()
	if _, err := NewWindowLimiter(0, time.Minute); err == nil {
		t.Fatal("zero limit accepted")
	}
	if _, err := NewWindowLimiter(1, 0); err == nil {
		t.Fatal("zero window accepted")
	}
	limiter, err := NewWindowLimiter(1, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0)
	if !limiter.Allow(context.Background(), "key", now) {
		t.Fatal("first call denied")
	}
	if limiter.Allow(context.Background(), "key", now.Add(time.Second)) {
		t.Fatal("call within window allowed")
	}
	if !limiter.Allow(context.Background(), "key", now.Add(time.Minute)) {
		t.Fatal("call at window boundary denied")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if limiter.Allow(cancelled, "other", now) {
		t.Fatal("cancelled call allowed")
	}
}

func invocation(classification tools.Classification) tools.Invocation {
	return tools.Invocation{
		Call: protocol.NormalizedToolCall{
			ID:        "call-1",
			Name:      "example",
			Arguments: json.RawMessage(`{"value":1}`),
		},
		Classification: classification,
	}
}

type recordingDetector struct {
	calls int
	err   error
}

func (detector *recordingDetector) Observe(context.Context, Request) error {
	detector.calls++
	return detector.err
}

type errorAuditor struct{}

func (errorAuditor) RecordPolicyEvent(context.Context, AuditEvent) error {
	return errors.New("audit unavailable")
}
