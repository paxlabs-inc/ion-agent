# Go Architecture Patterns for Proposals

Patterns discovered from real Go codebases (matrix-core/neo and others) that are useful when proposing new capabilities.

## Interface Design for Provider Systems

Go's implicit interfaces make provider systems clean. Define the interface where it's consumed, not where it's implemented.

```go
// Define in the consuming package (e.g., voice/stt/provider.go)
type Provider interface {
    RecognizeStream(ctx context.Context, audio <-chan []byte, cb ResultCallback) error
    Recognize(ctx context.Context, audio []byte, opts RecognizeOpts) (*Result, error)
    Close() error
}

// Implement in separate files (e.g., voice/stt/deepgram.go, voice/stt/whisper.go)
type DeepgramProvider struct { apiKey string; model string }
func (d *DeepgramProvider) RecognizeStream(...) error { ... }
```

Key rule: the interface should be as small as possible. One-method interfaces are fine in Go.

## Dependency Injection via Struct Fields

Neo uses constructor injection with options structs. New capabilities should follow the same pattern:

```go
type EngineOptions struct {
    Config    config.Config
    Main      *llm.Client
    Tools     *tools.Manager
    Pager     *memory.Pager
    // New voice field:
    VoiceCfg  voice.Config
}

type Engine struct {
    // ... existing fields ...
    voice *voice.Manager  // nil when voice is disabled
}
```

The nil-check pattern (`if e.voice != nil { ... }`) is idiomatic Go for optional capabilities.

## Synthetic Tools Pattern

Neo registers synthetic tools (not backed by MCP servers) alongside real ones. The pattern:

1. Define a constant name: `const SpeakTool = "speak"`
2. Define the JSON schema function: `func speakSchema() llm.Tool { ... }`
3. Wire it in `Manager.Schemas()` when the backing seam is non-nil
4. Handle it in `Manager.Dispatch()` with a dedicated case
5. The handler receives a seam function (injected by the engine): `SpeakFunc func(ctx, text) error`

This pattern means the tool is only advertised when the capability is wired, and the tool manager never knows about the engine internals.

## Reporter/Observer Pattern for Output

Neo's `Reporter` interface decouples the agent loop from output transport:

```go
type Reporter interface {
    Say(text string, completion bool)
    Status(text string)
    Delta(turn int, channel, text string)
    // ...
}
```

To add a new output channel (voice, logging, analytics), wrap the existing reporter:

```go
type VoiceReporter struct {
    inner  Reporter       // the existing SSE reporter
    tts    tts.Provider
    stream chan []byte     // audio output channel
}

func (v *VoiceReporter) Say(text string, completion bool) {
    v.inner.Say(text, completion)  // still emit text
    go v.synthesizeAndSend(text)   // also emit audio
}
```

## Broker/Event Fan-out

Neo's broker pattern for per-run event streams:

- One topic per run ID
- Buffered replay (last N events) for reconnecting subscribers
- Non-blocking publish (slow subscribers get dropped, not blocked)
- Post-publish tap for side effects (persistence, tracing)

New event types (e.g., `voice.audio`, `voice.transcript`) ride the same broker. No new infrastructure needed.

## Session Lifecycle and Interruption

The session pattern for long-running tasks:

1. `submit(message)` -> if active run, enqueue; else dispatch new run
2. `dispatch(message)` -> create run, register, start goroutine
3. `interrupt(run)` -> cancel context, mark stopped
4. `drive(ctx, run, message)` -> supervised agent loop with panic recovery

For voice, the same `interrupt()` call handles barge-in. The voice WebSocket handler calls the same method the HTTP stop endpoint uses.

## SSE + WebSocket Coexistence

When a system uses SSE for server-to-client streaming but needs bidirectional communication:

- Keep SSE for the existing text event stream (backward compatible)
- Add WebSocket at a new endpoint (`/voice`) for the bidirectional voice channel
- Both share the same broker and session infrastructure
- The WebSocket handler publishes events to the same broker topics

This avoids breaking existing SSE clients while adding full-duplex capability.

## Configuration Pattern

Neo's config is a flat struct with nested sections:

```go
type Config struct {
    AgentName      string
    TaskMaxWall    time.Duration
    MaxSubagents   int
    // New section:
    Voice          VoiceConfig
}

type VoiceConfig struct {
    Enabled    bool
    STT        STTConfig
    TTS        TTSConfig
    Conversation ConversationConfig
}
```

Env vars for API keys, config.yaml for everything else. Secrets never in config files.

## Graceful Degradation

Every optional capability should degrade to a no-op when unconfigured:

```go
// In engine setup:
if o.VoiceCfg.Enabled {
    e.voice, err = voice.NewManager(o.VoiceCfg)
    if err != nil {
        log.Warn("voice unavailable, falling back to text", "err", err)
        e.voice = nil
    }
}

// In tool registration:
if e.voice != nil {
    e.tools.SetSpeak(e.voice.Speak)
}
```

The system works identically with or without the capability. No feature flags in the agent loop itself.
