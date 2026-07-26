// Package policy implements the five-layer tool authorization pipeline.
package policy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/paxlabs-inc/ion-agent/internal/security/safety"
	"github.com/paxlabs-inc/ion-agent/internal/tools"
	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

// Decision is one layer's authorization verdict.
type Decision string

const (
	Allow  Decision = "ALLOW"
	Deny   Decision = "DENY"
	Modify Decision = "MODIFY"
)

// LayerName is closed so the pipeline cannot silently omit or reorder a layer.
type LayerName string

const (
	SandboxLayer  LayerName = "Sandbox"
	ProfileLayer  LayerName = "Profile"
	ProviderLayer LayerName = "Provider"
	SenderLayer   LayerName = "Sender"
	GroupLayer    LayerName = "Group"
)

var requiredOrder = [...]LayerName{
	SandboxLayer,
	ProfileLayer,
	ProviderLayer,
	SenderLayer,
	GroupLayer,
}

var (
	// ErrDenied is returned whenever a layer rejects a dispatch.
	ErrDenied = errors.New("policy: denied")
	// ErrRateLimited indicates YELLOW monitoring rejected an excessive call.
	ErrRateLimited = errors.New("policy: rate limited")
)

// Sender identifies who initiated a dispatch.
type Sender string

const (
	SenderUser        Sender = "user"
	SenderAutomatrix  Sender = "automatrix"
	SenderCuriosity   Sender = "curiosity"
	SenderDreamweaver Sender = "dreamweaver"
	SenderScheduler   Sender = "scheduler"
)

// Principal carries request-scoped policy facts. Approval only authorizes RED
// work initiated by an interactive user; it never relaxes idle-time rules.
type Principal struct {
	Sender   Sender
	Profile  string
	Provider string
	Group    string
	Approved bool
}

type principalKey struct{}

// WithPrincipal binds immutable authorization facts to a dispatch context.
func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, principal)
}

// PrincipalFromContext returns a conservative interactive principal when none
// is supplied. RED calls still require explicit approval.
func PrincipalFromContext(ctx context.Context) Principal {
	principal, ok := ctx.Value(principalKey{}).(Principal)
	if !ok {
		return Principal{Sender: SenderUser}
	}
	if principal.Sender == "" {
		principal.Sender = SenderUser
	}
	return principal
}

// Request is the mutable value passed through each policy layer.
type Request struct {
	Invocation  tools.Invocation
	Principal   Principal
	EvaluatedAt time.Time
}

// Result is a layer verdict. ModifiedCall is required for Modify.
type Result struct {
	Decision     Decision
	Reason       string
	ModifiedCall protocol.NormalizedToolCall
}

// Layer evaluates one defense-in-depth boundary.
type Layer interface {
	Name() LayerName
	Evaluate(context.Context, Request) (Result, error)
}

// LayerFunc makes explicit policies easy to compose without weakening order.
type LayerFunc struct {
	LayerName    LayerName
	EvaluateFunc func(context.Context, Request) (Result, error)
}

func (layer LayerFunc) Name() LayerName {
	return layer.LayerName
}

func (layer LayerFunc) Evaluate(ctx context.Context, request Request) (Result, error) {
	if layer.EvaluateFunc == nil {
		return Result{}, fmt.Errorf("policy: layer %s has no evaluator", layer.LayerName)
	}
	return layer.EvaluateFunc(ctx, request)
}

// AuditEvent is the durable, user-visible record of a denial or monitored call.
type AuditEvent struct {
	At             time.Time            `json:"at"`
	Layer          LayerName            `json:"layer"`
	Decision       Decision             `json:"decision"`
	Reason         string               `json:"reason"`
	ToolCallID     string               `json:"tool_call_id"`
	ToolName       string               `json:"tool_name"`
	Arguments      json.RawMessage      `json:"arguments"`
	Classification tools.Classification `json:"classification"`
	Sender         Sender               `json:"sender"`
	Profile        string               `json:"profile"`
	Provider       string               `json:"provider"`
	Group          string               `json:"group"`
}

// Auditor must durably record policy events before Authorize returns.
type Auditor interface {
	RecordPolicyEvent(context.Context, AuditEvent) error
}

// RateLimiter controls monitored YELLOW calls.
type RateLimiter interface {
	Allow(context.Context, string, time.Time) bool
}

// AnomalyDetector observes every YELLOW call and can reject an anomaly.
type AnomalyDetector interface {
	Observe(context.Context, Request) error
}

// Pipeline evaluates exactly five layers in the binding order.
type Pipeline struct {
	clock   types.Clock
	auditor Auditor
	layers  [5]Layer
}

// New constructs a closed five-layer pipeline.
func New(clock types.Clock, auditor Auditor, layers ...Layer) (*Pipeline, error) {
	if clock == nil || auditor == nil {
		return nil, fmt.Errorf("policy: clock and auditor are required")
	}
	if len(layers) != len(requiredOrder) {
		return nil, fmt.Errorf("policy: exactly five layers are required")
	}
	pipeline := &Pipeline{clock: clock, auditor: auditor}
	for index, expected := range requiredOrder {
		if layers[index] == nil || layers[index].Name() != expected {
			return nil, fmt.Errorf("policy: layer %d must be %s", index+1, expected)
		}
		pipeline.layers[index] = layers[index]
	}
	return pipeline, nil
}

// NewDefault builds the binding safety layer followed by four explicit
// pass-through boundaries. A YELLOW call fails closed when either monitoring
// dependency is absent. Callers may replace the boundaries with New.
func NewDefault(
	clock types.Clock,
	auditor Auditor,
	limiter RateLimiter,
	detector AnomalyDetector,
) (*Pipeline, error) {
	pass := func(name LayerName) Layer {
		return LayerFunc{
			LayerName: name,
			EvaluateFunc: func(context.Context, Request) (Result, error) {
				return Result{Decision: Allow}, nil
			},
		}
	}
	return New(
		clock,
		auditor,
		&SafetyLayer{
			limiter:  limiter,
			detector: detector,
			catalog:  safety.NewCatalog(),
		},
		pass(ProfileLayer),
		pass(ProviderLayer),
		pass(SenderLayer),
		pass(GroupLayer),
	)
}

// Authorize implements tools.ExecutionPolicy. Evaluation stops immediately on
// the first DENY; later layers cannot weaken it.
func (pipeline *Pipeline) Authorize(
	ctx context.Context,
	invocation tools.Invocation,
) (protocol.NormalizedToolCall, error) {
	if err := ctx.Err(); err != nil {
		return protocol.NormalizedToolCall{}, err
	}
	request := Request{
		Invocation:  cloneInvocation(invocation),
		Principal:   PrincipalFromContext(ctx),
		EvaluatedAt: pipeline.clock.Now(),
	}
	if err := request.Invocation.Call.Validate(); err != nil {
		return pipeline.deny(ctx, SandboxLayer, "invalid tool call: "+err.Error(), request)
	}
	for index, layer := range pipeline.layers {
		layerName := requiredOrder[index]
		result, err := layer.Evaluate(ctx, cloneRequest(request))
		if err != nil {
			return pipeline.deny(ctx, layerName, "layer evaluation failed: "+err.Error(), request)
		}
		switch result.Decision {
		case Allow:
		case Modify:
			if err := validateModification(request.Invocation.Call, result.ModifiedCall); err != nil {
				return pipeline.deny(ctx, layerName, err.Error(), request)
			}
			request.Invocation.Call = cloneCall(result.ModifiedCall)
		case Deny:
			return pipeline.deny(ctx, layerName, result.Reason, request)
		default:
			return pipeline.deny(ctx, layerName, "layer returned invalid decision", request)
		}
	}
	if request.Invocation.Classification == tools.ClassificationYellow ||
		request.Invocation.Classification == tools.ClassificationRed {
		reason := "YELLOW monitored"
		if request.Invocation.Classification == tools.ClassificationRed {
			reason = "RED approved"
		}
		if err := pipeline.auditor.RecordPolicyEvent(ctx, pipeline.event(
			SandboxLayer, Allow, reason, request,
		)); err != nil {
			return protocol.NormalizedToolCall{}, fmt.Errorf(
				"%w: consequential audit failed: %v", ErrDenied, err,
			)
		}
	}
	return cloneCall(request.Invocation.Call), nil
}

func (pipeline *Pipeline) deny(
	ctx context.Context,
	layer LayerName,
	reason string,
	request Request,
) (protocol.NormalizedToolCall, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "denied without reason"
	}
	auditErr := pipeline.auditor.RecordPolicyEvent(
		ctx,
		pipeline.event(layer, Deny, reason, request),
	)
	if auditErr != nil {
		return protocol.NormalizedToolCall{}, fmt.Errorf(
			"%w at %s (%s); audit failed: %v",
			ErrDenied, layer, reason, auditErr,
		)
	}
	return protocol.NormalizedToolCall{}, fmt.Errorf(
		"%w at %s: %s", ErrDenied, layer, reason,
	)
}

func (pipeline *Pipeline) event(
	layer LayerName,
	decision Decision,
	reason string,
	request Request,
) AuditEvent {
	return AuditEvent{
		At:             request.EvaluatedAt,
		Layer:          layer,
		Decision:       decision,
		Reason:         reason,
		ToolCallID:     request.Invocation.Call.ID,
		ToolName:       request.Invocation.Call.Name,
		Arguments:      append(json.RawMessage(nil), request.Invocation.Call.Arguments...),
		Classification: request.Invocation.Classification,
		Sender:         request.Principal.Sender,
		Profile:        request.Principal.Profile,
		Provider:       request.Principal.Provider,
		Group:          request.Principal.Group,
	}
}

// SafetyLayer structurally enforces safety classes and the three idle-time
// non-negotiables at Layer 1.
type SafetyLayer struct {
	limiter  RateLimiter
	detector AnomalyDetector
	catalog  *safety.Catalog
}

func (*SafetyLayer) Name() LayerName {
	return SandboxLayer
}

func (layer *SafetyLayer) Evaluate(ctx context.Context, request Request) (Result, error) {
	if !validSender(request.Principal.Sender) {
		return Result{Decision: Deny, Reason: "unknown sender"}, nil
	}
	classification := request.Invocation.Classification
	switch classification {
	case tools.ClassificationGreen, tools.ClassificationYellow, tools.ClassificationRed:
	default:
		return Result{Decision: Deny, Reason: "unknown safety classification"}, nil
	}
	if layer.catalog != nil {
		if entry, exists := layer.catalog.Lookup(request.Invocation.Call.Name); exists {
			if entry.Classification == safety.ClassBlack {
				return Result{
					Decision: Deny,
					Reason:   "catalog forbids action " + entry.Name,
				}, nil
			}
			expected := catalogClassification(entry.Classification)
			if classificationRisk(classification) < classificationRisk(expected) {
				return Result{
					Decision: Deny,
					Reason: fmt.Sprintf(
						"tool is under-classified: declared %s, catalog requires %s",
						classification,
						expected,
					),
				}, nil
			}
		}
	}

	if isIdle(request.Principal.Sender) {
		switch {
		case classification == tools.ClassificationRed:
			return Result{Decision: Deny, Reason: "idle-time sender cannot execute RED tools"}, nil
		case request.Invocation.ExternallyCommunicating:
			return Result{Decision: Deny, Reason: "idle-time sender cannot communicate externally"}, nil
		}
	}
	if classification == tools.ClassificationRed && !request.Principal.Approved {
		return Result{Decision: Deny, Reason: "RED tool requires human approval"}, nil
	}
	if classification == tools.ClassificationYellow {
		if layer.limiter == nil {
			return Result{Decision: Deny, Reason: "YELLOW rate limiter is not configured"}, nil
		}
		if layer.detector == nil {
			return Result{Decision: Deny, Reason: "YELLOW anomaly detector is not configured"}, nil
		}
		key := string(request.Principal.Sender) + "\x00" + request.Invocation.Call.Name
		if !layer.limiter.Allow(ctx, key, request.EvaluatedAt) {
			return Result{Decision: Deny, Reason: ErrRateLimited.Error()}, nil
		}
		if err := layer.detector.Observe(ctx, request); err != nil {
			return Result{Decision: Deny, Reason: "anomaly detected: " + err.Error()}, nil
		}
	}
	return Result{Decision: Allow}, nil
}

func catalogClassification(classification safety.Classification) tools.Classification {
	switch classification {
	case safety.ClassGreen:
		return tools.ClassificationGreen
	case safety.ClassYellow:
		return tools.ClassificationYellow
	case safety.ClassRed, safety.ClassBlack:
		return tools.ClassificationRed
	default:
		return ""
	}
}

func classificationRisk(classification tools.Classification) int {
	switch classification {
	case tools.ClassificationGreen:
		return 1
	case tools.ClassificationYellow:
		return 2
	case tools.ClassificationRed:
		return 3
	default:
		return 0
	}
}

func validSender(sender Sender) bool {
	switch sender {
	case SenderUser, SenderAutomatrix, SenderCuriosity, SenderDreamweaver,
		SenderScheduler:
		return true
	default:
		return false
	}
}

func isIdle(sender Sender) bool {
	return sender == SenderAutomatrix || sender == SenderCuriosity ||
		sender == SenderDreamweaver || sender == SenderScheduler
}

// WindowLimiter is an in-memory fixed-window limiter for YELLOW calls.
type WindowLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	hits   map[string][]time.Time
}

// NewWindowLimiter constructs a limiter with a positive count and duration.
func NewWindowLimiter(limit int, window time.Duration) (*WindowLimiter, error) {
	if limit <= 0 || window <= 0 {
		return nil, fmt.Errorf("policy: positive rate limit and window are required")
	}
	return &WindowLimiter{limit: limit, window: window, hits: make(map[string][]time.Time)}, nil
}

func (limiter *WindowLimiter) Allow(ctx context.Context, key string, now time.Time) bool {
	if ctx.Err() != nil {
		return false
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	cutoff := now.Add(-limiter.window)
	kept := limiter.hits[key][:0]
	for _, hit := range limiter.hits[key] {
		if hit.After(cutoff) {
			kept = append(kept, hit)
		}
	}
	if len(kept) >= limiter.limit {
		limiter.hits[key] = kept
		return false
	}
	limiter.hits[key] = append(kept, now)
	return true
}

func validateModification(
	original protocol.NormalizedToolCall,
	modified protocol.NormalizedToolCall,
) error {
	if modified.ID != original.ID || modified.Name != original.Name {
		return fmt.Errorf("policy: a layer cannot change tool identity")
	}
	if err := modified.Validate(); err != nil {
		return fmt.Errorf("policy: invalid modified call: %w", err)
	}
	return nil
}

func cloneInvocation(invocation tools.Invocation) tools.Invocation {
	invocation.Call = cloneCall(invocation.Call)
	return invocation
}

func cloneRequest(request Request) Request {
	request.Invocation = cloneInvocation(request.Invocation)
	return request
}

func cloneCall(call protocol.NormalizedToolCall) protocol.NormalizedToolCall {
	call.Arguments = append(json.RawMessage(nil), call.Arguments...)
	return call
}
