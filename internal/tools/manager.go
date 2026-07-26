// Package tools implements self-registration, readiness-filtered tool
// surfaces, source discovery, and bounded tool execution.
package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
	"github.com/santhosh-tekuri/jsonschema/v5"
)

const (
	defaultReadinessTTL   = 30 * time.Second
	defaultFailureGrace   = 60 * time.Second
	defaultExecutionLimit = 30 * time.Second
)

var (
	// ErrNotFound indicates that no tool is registered under the requested name.
	ErrNotFound = errors.New("tools: tool not found")
	// ErrUnavailable indicates that the readiness probe currently fails.
	ErrUnavailable = errors.New("tools: tool unavailable")
	// ErrDuplicate indicates a conflicting registration.
	ErrDuplicate = errors.New("tools: duplicate registration")
	// ErrTimeout indicates that a tool exceeded its execution deadline.
	ErrTimeout = errors.New("tools: execution timed out")
	// ErrPolicyDenied indicates that the security policy rejected a tool call.
	ErrPolicyDenied = errors.New("tools: execution denied by policy")
	// ErrPolicyRequired indicates that execution was attempted without the
	// mandatory security policy interceptor.
	ErrPolicyRequired = errors.New("tools: execution policy is required")
	// ErrIdempotencyConflict indicates one execution key was reused with a
	// different operation identity.
	ErrIdempotencyConflict = errors.New("tools: idempotency key conflict")
)

// Classification is the immutable safety class assigned to a tool.
type Classification string

const (
	ClassificationGreen  Classification = "GREEN"
	ClassificationYellow Classification = "YELLOW"
	ClassificationRed    Classification = "RED"
)

// Invocation is the complete security-relevant view of one tool dispatch.
type Invocation struct {
	Call                    protocol.NormalizedToolCall
	Description             string
	Classification          Classification
	ExternallyCommunicating bool
}

// ExecutionPolicy intercepts every call after readiness succeeds and before
// the handler receives any arguments. It may return a modified normalized call.
type ExecutionPolicy interface {
	Authorize(context.Context, Invocation) (protocol.NormalizedToolCall, error)
}

// ApprovalAuthorizer obtains exact, authenticated human approval for a RED
// invocation and returns the context carrying that approval.
type ApprovalAuthorizer interface {
	AuthorizeTool(context.Context, Invocation) (context.Context, error)
}

// Handler executes one validated normalized tool call.
type Handler func(context.Context, json.RawMessage) (json.RawMessage, error)

// ArgumentValidationIssue is one exact JSON-instance path rejected by a tool's
// advertised schema.
type ArgumentValidationIssue struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// ArgumentValidationError preserves machine-readable corrective details all
// the way into the tool-result message returned to a provider.
type ArgumentValidationError struct {
	Issues []ArgumentValidationIssue `json:"issues"`
}

func (validation *ArgumentValidationError) Error() string {
	if validation == nil || len(validation.Issues) == 0 {
		return "arguments do not match schema"
	}
	return fmt.Sprintf("arguments do not match schema at %s: %s",
		validation.Issues[0].Path, validation.Issues[0].Message)
}

func (validation *ArgumentValidationError) StructuredToolError() any {
	return struct {
		Code   string                    `json:"code"`
		Issues []ArgumentValidationIssue `json:"issues"`
	}{Code: "argument_validation_failed", Issues: validation.Issues}
}

// CheckFunc probes whether a tool's external dependencies are ready.
type CheckFunc func(context.Context) error

// Registration co-locates a tool's model schema, execution path, and
// availability probe.
type Registration struct {
	Name                    string
	Description             string
	Parameters              json.RawMessage
	Timeout                 time.Duration
	Check                   CheckFunc
	Handler                 Handler
	Classification          Classification
	ExternallyCommunicating bool
}

type readinessState struct {
	cached      bool
	cachedAt    time.Time
	hasCache    bool
	lastGood    time.Time
	hasLastGood bool
	reason      string
}

type entry struct {
	registration Registration
	readiness    readinessState
}

type idempotencyScopeKey struct{}

// WithIdempotencyScope isolates stable tool call IDs to one durable turn.
func WithIdempotencyScope(ctx context.Context, scope string) context.Context {
	return context.WithValue(ctx, idempotencyScopeKey{}, strings.TrimSpace(scope))
}

type idempotentExecution struct {
	signature [32]byte
	done      chan struct{}
	result    json.RawMessage
	err       error
}

// Manager owns all registrations. Its zero value is intentionally invalid;
// callers construct it with NewManager so timing behavior is explicit.
type Manager struct {
	mu           sync.Mutex
	clock        types.Clock
	readinessTTL time.Duration
	failureGrace time.Duration
	entries      map[string]*entry
	policy       ExecutionPolicy
	approvals    ApprovalAuthorizer
	lifecycle    LifecycleObserver
	executions   map[string]*idempotentExecution
}

// Status is the operator-facing readiness view of one registered tool. It
// deliberately excludes handlers and raw schemas while retaining an
// actionable dependency failure reason.
type Status struct {
	Name           string         `json:"name"`
	Description    string         `json:"description"`
	Classification Classification `json:"classification"`
	Ready          bool           `json:"ready"`
	Reason         string         `json:"reason,omitempty"`
}

// ManagerOption customizes readiness timing.
type ManagerOption func(*Manager)

// WithReadinessTiming overrides the specification defaults, primarily for
// deterministic tests.
func WithReadinessTiming(ttl, failureGrace time.Duration) ManagerOption {
	return func(manager *Manager) {
		if ttl > 0 {
			manager.readinessTTL = ttl
		}
		if failureGrace > 0 {
			manager.failureGrace = failureGrace
		}
	}
}

// WithExecutionPolicy installs the mandatory pre-execution policy interceptor.
func WithExecutionPolicy(policy ExecutionPolicy) ManagerOption {
	return func(manager *Manager) {
		manager.policy = policy
	}
}

// WithApprovalAuthorizer connects RED dispatch to an authenticated approval
// broker. Without it, RED calls retain the policy's fail-closed behavior.
func WithApprovalAuthorizer(authorizer ApprovalAuthorizer) ManagerOption {
	return func(manager *Manager) {
		manager.approvals = authorizer
	}
}

// WithLifecycleObserver installs the single manager-boundary lifecycle
// projection used by production operator runtimes.
func WithLifecycleObserver(observer LifecycleObserver) ManagerOption {
	return func(manager *Manager) {
		manager.lifecycle = observer
	}
}

// NewManager constructs an empty registry.
func NewManager(clock types.Clock, options ...ManagerOption) (*Manager, error) {
	if clock == nil {
		return nil, fmt.Errorf("tools: clock is required")
	}
	manager := &Manager{
		clock:        clock,
		readinessTTL: defaultReadinessTTL,
		failureGrace: defaultFailureGrace,
		entries:      make(map[string]*entry),
		executions:   make(map[string]*idempotentExecution),
	}
	for _, option := range options {
		option(manager)
	}
	return manager, nil
}

// Register adds a self-contained tool declaration to the registry.
func (manager *Manager) Register(ctx context.Context, registration Registration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateRegistration(registration); err != nil {
		return err
	}
	registration.Parameters = append(json.RawMessage(nil), registration.Parameters...)
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if _, exists := manager.entries[registration.Name]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicate, registration.Name)
	}
	manager.entries[registration.Name] = &entry{registration: registration}
	return nil
}

// Surface returns only ready tools, sorted for deterministic provider prompts.
// Probe errors are deliberately not returned: unavailable tools silently
// vanish from the model-visible surface.
func (manager *Manager) Surface(ctx context.Context) []protocol.ToolDefinition {
	registrations := manager.snapshot()
	surface := make([]protocol.ToolDefinition, 0, len(registrations))
	for _, registration := range registrations {
		if ready, _ := manager.readiness(ctx, registration.Name, registration.Check); ready {
			surface = append(surface, protocol.ToolDefinition{
				Name:        registration.Name,
				Description: registration.Description,
				Parameters:  append(json.RawMessage(nil), registration.Parameters...),
			})
		}
	}
	return surface
}

// Readiness returns every registration, including unavailable tools, in stable
// order. This is the authoritative source for operator setup and diagnostics
// surfaces; the model-visible Surface remains ready-only.
func (manager *Manager) Readiness(ctx context.Context) []Status {
	registrations := manager.snapshot()
	statuses := make([]Status, 0, len(registrations))
	for _, registration := range registrations {
		ready, reason := manager.readiness(ctx, registration.Name, registration.Check)
		statuses = append(statuses, Status{
			Name: registration.Name, Description: registration.Description,
			Classification: registration.Classification,
			Ready:          ready, Reason: reason,
		})
	}
	return statuses
}

// Execute routes a normalized call through readiness and timeout enforcement.
// Handlers own no goroutines and must honor the supplied context.
func (manager *Manager) Execute(
	ctx context.Context,
	call protocol.NormalizedToolCall,
) (json.RawMessage, error) {
	if err := call.Validate(); err != nil {
		return nil, fmt.Errorf("tools: invalid call: %w", err)
	}
	registration, found := manager.lookup(call.Name)
	if !found {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, call.Name)
	}
	invocation := Invocation{
		Call:                    cloneNormalizedCall(call),
		Description:             registration.Description,
		Classification:          registration.Classification,
		ExternallyCommunicating: registration.ExternallyCommunicating,
	}
	var idempotencyKey string
	var err error
	var prior *idempotentExecution
	if registration.Classification == ClassificationYellow ||
		registration.Classification == ClassificationRed {
		idempotencyKey, prior, err = manager.reserveExecution(ctx, call)
		if err != nil {
			return nil, err
		}
	}
	binding, _ := protocol.ToolExecutionBindingFromContext(ctx)
	emit := func(
		emitCtx context.Context,
		phase LifecyclePhase,
		progress json.RawMessage,
		result json.RawMessage,
		executionErr error,
	) error {
		if manager.lifecycle == nil || binding.ToolEventID == [16]byte{} ||
			binding.ActorID == [16]byte{} {
			return nil
		}
		boundedResult := result
		resultOmitted := len(result) > MaximumLifecycleResultBytes
		if resultOmitted {
			boundedResult = nil
		}
		return manager.lifecycle.Observe(emitCtx, LifecycleObservation{
			Binding: binding, Call: cloneNormalizedCall(call),
			Description:    registration.Description,
			Classification: registration.Classification,
			Phase:          phase, OccurredAt: manager.clock.Now(),
			Progress:    append(json.RawMessage(nil), progress...),
			Result:      append(json.RawMessage(nil), boundedResult...),
			ResultBytes: len(result), ResultOmitted: resultOmitted,
			Error: executionErr,
		})
	}
	requested := false
	terminal := false
	started := false
	finish := func(
		result json.RawMessage,
		executionErr error,
	) (json.RawMessage, error) {
		if terminal {
			return result, executionErr
		}
		terminal = true
		phase := TerminalPhase(ctx, executionErr)
		emitCtx := ctx
		if ctx.Err() != nil {
			emitCtx = context.WithoutCancel(ctx)
		}
		if requested {
			if emitErr := emit(
				emitCtx, phase, nil, result, executionErr,
			); emitErr != nil {
				executionErr = lifecycleError(emitErr)
				result = nil
			}
		}
		if prior == nil {
			manager.resolveExecution(idempotencyKey, result, executionErr)
			if !started && executionErr != nil {
				manager.forgetExecution(idempotencyKey)
			}
		}
		return result, executionErr
	}
	if prior == nil {
		if emitErr := emit(
			ctx, LifecycleRequested, nil, nil, nil,
		); emitErr != nil {
			projectionErr := lifecycleError(emitErr)
			manager.resolveExecution(idempotencyKey, nil, projectionErr)
			manager.forgetExecution(idempotencyKey)
			return nil, projectionErr
		}
		requested = true
	}
	if err := ValidateArguments(registration.Parameters, call.Arguments); err != nil {
		return finish(nil, fmt.Errorf(
			"tools: invalid arguments for %s: %w", call.Name, err,
		))
	}
	if ready, _ := manager.readiness(ctx, registration.Name, registration.Check); !ready {
		if err := ctx.Err(); err != nil {
			return finish(nil, err)
		}
		return finish(nil, fmt.Errorf("%w: %s", ErrUnavailable, call.Name))
	}
	if manager.policy == nil {
		return finish(nil, fmt.Errorf(
			"%w: %s: %w", ErrPolicyDenied, call.Name, ErrPolicyRequired,
		))
	}
	if registration.Classification == ClassificationRed && manager.approvals != nil {
		if prior == nil {
			if emitErr := emit(
				ctx, LifecycleAwaitingApproval, nil, nil, nil,
			); emitErr != nil {
				return finish(nil, lifecycleError(emitErr))
			}
		}
		authorizedContext, approvalErr := manager.approvals.AuthorizeTool(ctx, invocation)
		if approvalErr != nil {
			return finish(nil, fmt.Errorf(
				"%w: %s: %w", ErrPolicyDenied, call.Name, approvalErr,
			))
		}
		ctx = authorizedContext
	}
	authorized, err := manager.policy.Authorize(ctx, invocation)
	if err != nil {
		return finish(nil, fmt.Errorf(
			"%w: %s: %w", ErrPolicyDenied, call.Name, err,
		))
	}
	if err := authorized.Validate(); err != nil || authorized.Name != call.Name ||
		authorized.ID != call.ID {
		return finish(nil, fmt.Errorf(
			"%w: %s: policy returned invalid modified call",
			ErrPolicyDenied, call.Name,
		))
	}
	call = authorized
	if err := ValidateArguments(registration.Parameters, call.Arguments); err != nil {
		return finish(nil, fmt.Errorf(
			"tools: policy returned invalid arguments for %s: %w",
			call.Name,
			err,
		))
	}
	if prior != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-prior.done:
			return append(json.RawMessage(nil), prior.result...), prior.err
		}
	}
	if emitErr := emit(ctx, LifecycleStarted, nil, nil, nil); emitErr != nil {
		return finish(nil, lifecycleError(emitErr))
	}
	started = true
	limit := registration.Timeout
	if limit <= 0 {
		limit = defaultExecutionLimit
	}
	runCtx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	reporter := &progressReporter{
		clock: manager.clock.Now,
		emit: func(progress json.RawMessage) error {
			return emit(runCtx, LifecycleProgress, progress, nil, nil)
		},
	}
	runCtx = context.WithValue(runCtx, progressReporterKey{}, reporter)
	result, err := registration.Handler(runCtx, append(json.RawMessage(nil), call.Arguments...))
	reporter.mu.Lock()
	reporter.terminal = true
	reporter.mu.Unlock()
	if err != nil {
		var uncertain *OutcomeUnknownError
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) &&
			!errors.As(err, &uncertain) {
			err = fmt.Errorf("%w: %s", ErrTimeout, call.Name)
		} else {
			err = fmt.Errorf("tools: execute %s: %w", call.Name, err)
		}
		return finish(nil, err)
	}
	if err := runCtx.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			err = fmt.Errorf("%w: %s", ErrTimeout, call.Name)
		}
		return finish(nil, err)
	}
	if len(bytes.TrimSpace(result)) == 0 {
		result = json.RawMessage(`null`)
	}
	if !json.Valid(result) {
		err = fmt.Errorf("tools: execute %s returned invalid JSON", call.Name)
		return finish(nil, err)
	}
	result = append(json.RawMessage(nil), result...)
	return finish(result, nil)
}

// ValidateArguments applies the registered JSON Schema to one raw argument
// object. Structured and compatibility-normalized calls share this boundary.
func ValidateArguments(schemaDocument, arguments json.RawMessage) error {
	compiled, err := jsonschema.CompileString(
		"ion://tool-parameters.json",
		string(schemaDocument),
	)
	if err != nil {
		return fmt.Errorf("invalid tool schema: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("arguments must be valid JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("arguments must contain exactly one JSON value")
	}
	if _, ok := value.(map[string]any); !ok {
		return fmt.Errorf("arguments must be a JSON object")
	}
	if err := compiled.Validate(value); err != nil {
		var validation *jsonschema.ValidationError
		if errors.As(err, &validation) {
			basic := validation.BasicOutput()
			issues := make([]ArgumentValidationIssue, 0, len(basic.Errors))
			for _, item := range basic.Errors {
				if strings.TrimSpace(item.Error) == "" {
					continue
				}
				path := strings.TrimPrefix(item.InstanceLocation, "/")
				path = strings.ReplaceAll(path, "/", ".")
				if path == "" {
					path = "$"
				}
				issues = append(issues, ArgumentValidationIssue{
					Path: path, Message: item.Error,
				})
			}
			if len(issues) > 1 && issues[0].Path == "$" &&
				strings.Contains(issues[0].Message, "doesn't validate") {
				issues = issues[1:]
			}
			return &ArgumentValidationError{Issues: issues}
		}
		return fmt.Errorf("arguments do not match schema: %w", err)
	}
	return nil
}

func (manager *Manager) reserveExecution(
	ctx context.Context,
	call protocol.NormalizedToolCall,
) (string, *idempotentExecution, error) {
	scope, _ := ctx.Value(idempotencyScopeKey{}).(string)
	key := scope + "\x00" + call.ID
	signaturePayload := append([]byte(call.Name+"\x00"), call.Arguments...)
	signature := sha256.Sum256(signaturePayload)
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if prior := manager.executions[key]; prior != nil {
		if prior.signature != signature {
			return "", nil, fmt.Errorf(
				"%w: %s", ErrIdempotencyConflict, call.ID,
			)
		}
		return key, prior, nil
	}
	manager.executions[key] = &idempotentExecution{
		signature: signature, done: make(chan struct{}),
	}
	return key, nil, nil
}

func (manager *Manager) resolveExecution(
	key string,
	result json.RawMessage,
	err error,
) {
	if key == "" {
		return
	}
	manager.mu.Lock()
	execution := manager.executions[key]
	if execution != nil {
		execution.result = append(json.RawMessage(nil), result...)
		execution.err = err
		close(execution.done)
	}
	manager.mu.Unlock()
}

func (manager *Manager) forgetExecution(key string) {
	if key == "" {
		return
	}
	manager.mu.Lock()
	delete(manager.executions, key)
	manager.mu.Unlock()
}

// InvalidateReadiness clears one tool's cached verdict while preserving its
// last-good timestamp for failure grace semantics.
func (manager *Manager) InvalidateReadiness(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	found, exists := manager.entries[name]
	if !exists {
		return fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	found.readiness.hasCache = false
	return nil
}

func (manager *Manager) snapshot() []Registration {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	registrations := make([]Registration, 0, len(manager.entries))
	for _, registered := range manager.entries {
		registrations = append(registrations, registered.registration)
	}
	sort.Slice(registrations, func(left, right int) bool {
		return registrations[left].Name < registrations[right].Name
	})
	return registrations
}

func (manager *Manager) lookup(name string) (Registration, bool) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	found, exists := manager.entries[name]
	if !exists {
		return Registration{}, false
	}
	return found.registration, true
}

func (manager *Manager) readiness(
	ctx context.Context,
	name string,
	check CheckFunc,
) (bool, string) {
	if ctx.Err() != nil {
		return false, "request canceled"
	}
	now := manager.clock.Now()
	manager.mu.Lock()
	found, exists := manager.entries[name]
	if !exists {
		manager.mu.Unlock()
		return false, "tool is not registered"
	}
	state := found.readiness
	if state.hasCache && now.Sub(state.cachedAt) < manager.readinessTTL {
		manager.mu.Unlock()
		return state.cached, state.reason
	}
	manager.mu.Unlock()

	err := check(ctx)
	available := err == nil
	now = manager.clock.Now()

	manager.mu.Lock()
	defer manager.mu.Unlock()
	found, exists = manager.entries[name]
	if !exists {
		return false, "tool is not registered"
	}
	if available {
		found.readiness = readinessState{
			cached:      true,
			cachedAt:    now,
			hasCache:    true,
			lastGood:    now,
			hasLastGood: true,
		}
		return true, ""
	}
	reason := readinessReason(err)
	lastGood := found.readiness.lastGood
	hasLastGood := found.readiness.hasLastGood
	if hasLastGood && now.Sub(lastGood) < manager.failureGrace {
		// Do not cache transient failures; the next surface build re-probes.
		found.readiness.hasCache = false
		found.readiness.reason = "temporarily degraded: " + reason
		return true, found.readiness.reason
	}
	found.readiness.cached = false
	found.readiness.cachedAt = now
	found.readiness.hasCache = true
	found.readiness.reason = reason
	return false, reason
}

func readinessReason(err error) string {
	if err == nil {
		return ""
	}
	reason := strings.TrimSpace(err.Error())
	if reason == "" {
		return "readiness probe failed"
	}
	const maxReasonBytes = 512
	if len(reason) > maxReasonBytes {
		reason = reason[:maxReasonBytes] + "..."
	}
	return reason
}

func validateRegistration(registration Registration) error {
	if strings.TrimSpace(registration.Name) == "" {
		return fmt.Errorf("tools: registration name is required")
	}
	if strings.TrimSpace(registration.Description) == "" {
		return fmt.Errorf("tools: registration description is required")
	}
	parameters := bytes.TrimSpace(registration.Parameters)
	if len(parameters) < 2 || parameters[0] != '{' ||
		parameters[len(parameters)-1] != '}' || !json.Valid(parameters) {
		return fmt.Errorf("tools: registration parameters must be a JSON object")
	}
	if _, err := jsonschema.CompileString(
		"ion://tool-registration.json",
		string(parameters),
	); err != nil {
		return fmt.Errorf(
			"tools: registration parameters must be a valid JSON Schema: %w",
			err,
		)
	}
	if registration.Check == nil || registration.Handler == nil {
		return fmt.Errorf("tools: readiness check and handler are required")
	}
	if registration.Timeout < 0 {
		return fmt.Errorf("tools: timeout cannot be negative")
	}
	switch registration.Classification {
	case ClassificationGreen, ClassificationYellow, ClassificationRed:
	case "":
		return fmt.Errorf("tools: safety classification is required")
	default:
		return fmt.Errorf("tools: invalid safety classification %q", registration.Classification)
	}
	return nil
}

func cloneNormalizedCall(call protocol.NormalizedToolCall) protocol.NormalizedToolCall {
	call.Arguments = append(json.RawMessage(nil), call.Arguments...)
	return call
}
