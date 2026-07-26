package privatecomputer

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	maximumDesktopFrameBytes = 16 << 20
	maximumDesktopRPCBytes   = 24 << 20
)

type DesktopFrame struct {
	Sequence   uint64    `json:"sequence"`
	MIMEType   string    `json:"mime_type"`
	Data       []byte    `json:"-"`
	Digest     string    `json:"digest"`
	Width      int       `json:"width"`
	Height     int       `json:"height"`
	CapturedAt time.Time `json:"captured_at"`
	Degraded   bool      `json:"degraded"`
	Verified   bool      `json:"verified"`
}

type DesktopObservationRequest struct {
	PID         int    `json:"pid,omitempty"`
	WindowID    uint64 `json:"window_id,omitempty"`
	Query       string `json:"query,omitempty"`
	MaxElements int    `json:"max_elements,omitempty"`
	MaxDepth    int    `json:"max_depth,omitempty"`
}

func (request DesktopObservationRequest) Validate() error {
	if (request.PID == 0) != (request.WindowID == 0) ||
		request.PID < 0 ||
		len(request.Query) > 512 ||
		request.MaxElements < 0 || request.MaxElements > 500 ||
		request.MaxDepth < 0 || request.MaxDepth > 64 {
		return ErrInvalidContract
	}
	return nil
}

type DesktopInputKind string

const (
	DesktopInputMove   DesktopInputKind = "move"
	DesktopInputClick  DesktopInputKind = "click"
	DesktopInputScroll DesktopInputKind = "scroll"
	DesktopInputType   DesktopInputKind = "type"
	DesktopInputKey    DesktopInputKind = "key"
	DesktopInputHotkey DesktopInputKind = "hotkey"
)

type DesktopWindowInputKind string

const (
	DesktopWindowClick    DesktopWindowInputKind = "click"
	DesktopWindowType     DesktopWindowInputKind = "type_text"
	DesktopWindowKey      DesktopWindowInputKind = "press_key"
	DesktopWindowHotkey   DesktopWindowInputKind = "hotkey"
	DesktopWindowScroll   DesktopWindowInputKind = "scroll"
	DesktopWindowNavigate DesktopWindowInputKind = "browser_navigate"
)

type DesktopWindowInput struct {
	Kind         DesktopWindowInputKind `json:"kind"`
	PID          int                    `json:"pid"`
	WindowID     uint64                 `json:"window_id"`
	ElementIndex *int                   `json:"element_index,omitempty"`
	ElementToken string                 `json:"element_token,omitempty"`
	TargetID     string                 `json:"target_id,omitempty"`
	TabID        string                 `json:"tab_id,omitempty"`
	Ref          string                 `json:"ref,omitempty"`
	X            *float64               `json:"x,omitempty"`
	Y            *float64               `json:"y,omitempty"`
	Button       string                 `json:"button,omitempty"`
	Count        int                    `json:"count,omitempty"`
	Text         string                 `json:"text,omitempty"`
	Key          string                 `json:"key,omitempty"`
	Modifiers    []string               `json:"modifiers,omitempty"`
	Keys         []string               `json:"keys,omitempty"`
	Direction    string                 `json:"direction,omitempty"`
	Amount       int                    `json:"amount,omitempty"`
	URL          string                 `json:"url,omitempty"`
}

func (input DesktopWindowInput) Validate() error {
	if input.PID < 1 || input.WindowID == 0 ||
		(input.ElementIndex != nil && *input.ElementIndex < 0) ||
		len(input.ElementToken) > 128 ||
		len(input.TargetID) > 128 || len(input.TabID) > 128 ||
		len(input.Ref) > 128 || len(input.URL) > 4096 ||
		(strings.TrimSpace(input.TargetID) == "") !=
			(strings.TrimSpace(input.TabID) == "") ||
		(input.X == nil) != (input.Y == nil) {
		return ErrInvalidContract
	}
	if input.X != nil &&
		(*input.X < 0 || *input.X > 3840 || *input.Y < 0 || *input.Y > 2160) {
		return ErrInvalidContract
	}
	located := input.ElementIndex != nil ||
		strings.TrimSpace(input.ElementToken) != "" ||
		strings.TrimSpace(input.Ref) != "" || input.X != nil
	switch input.Kind {
	case DesktopWindowClick:
		if !located ||
			(input.Button != "" && input.Button != "left" &&
				input.Button != "right" && input.Button != "middle") ||
			input.Count < 0 || input.Count > 3 {
			return ErrInvalidContract
		}
	case DesktopWindowType:
		if input.Text == "" || len(input.Text) > 4096 {
			return ErrInvalidContract
		}
	case DesktopWindowKey:
		if !validDesktopKey(input.Key) || len(input.Modifiers) > 4 {
			return ErrInvalidContract
		}
		for _, modifier := range input.Modifiers {
			if !validDesktopKey(modifier) {
				return ErrInvalidContract
			}
		}
	case DesktopWindowHotkey:
		if len(input.Keys) < 2 || len(input.Keys) > 5 {
			return ErrInvalidContract
		}
		for _, key := range input.Keys {
			if !validDesktopKey(key) {
				return ErrInvalidContract
			}
		}
	case DesktopWindowScroll:
		if (input.Direction != "up" && input.Direction != "down" &&
			input.Direction != "left" && input.Direction != "right") ||
			input.Amount < 1 || input.Amount > 20 {
			return ErrInvalidContract
		}
	case DesktopWindowNavigate:
		if strings.TrimSpace(input.TargetID) == "" ||
			(!strings.HasPrefix(input.URL, "http://") &&
				!strings.HasPrefix(input.URL, "https://") &&
				!strings.HasPrefix(input.URL, "about:")) {
			return ErrInvalidContract
		}
	default:
		return ErrInvalidContract
	}
	return nil
}

type DesktopInput struct {
	Kind      DesktopInputKind `json:"kind"`
	X         float64          `json:"x,omitempty"`
	Y         float64          `json:"y,omitempty"`
	Button    string           `json:"button,omitempty"`
	Count     int              `json:"count,omitempty"`
	Direction string           `json:"direction,omitempty"`
	Amount    int              `json:"amount,omitempty"`
	Text      string           `json:"text,omitempty"`
	Key       string           `json:"key,omitempty"`
	Keys      []string         `json:"keys,omitempty"`
	Modifiers []string         `json:"modifiers,omitempty"`
}

func (input DesktopInput) Validate(width, height int) error {
	if width < 1 || height < 1 {
		return ErrInvalidContract
	}
	coordinateValid := input.X >= 0 && input.X < float64(width) &&
		input.Y >= 0 && input.Y < float64(height)
	switch input.Kind {
	case DesktopInputMove:
		if !coordinateValid {
			return ErrInvalidContract
		}
	case DesktopInputClick:
		if !coordinateValid ||
			(input.Button != "" && input.Button != "left" &&
				input.Button != "right" && input.Button != "middle") ||
			input.Count < 0 || input.Count > 3 {
			return ErrInvalidContract
		}
	case DesktopInputScroll:
		if !coordinateValid ||
			(input.Direction != "up" && input.Direction != "down" &&
				input.Direction != "left" && input.Direction != "right") ||
			input.Amount < 1 || input.Amount > 20 {
			return ErrInvalidContract
		}
	case DesktopInputType:
		if input.Text == "" || len(input.Text) > 4096 {
			return ErrInvalidContract
		}
	case DesktopInputKey:
		if !validDesktopKey(input.Key) || len(input.Modifiers) > 4 {
			return ErrInvalidContract
		}
		for _, modifier := range input.Modifiers {
			if !validDesktopKey(modifier) {
				return ErrInvalidContract
			}
		}
	case DesktopInputHotkey:
		if len(input.Keys) < 2 || len(input.Keys) > 5 {
			return ErrInvalidContract
		}
		for _, key := range input.Keys {
			if !validDesktopKey(key) {
				return ErrInvalidContract
			}
		}
	default:
		return ErrInvalidContract
	}
	return nil
}

func validDesktopKey(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 32 {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character > 0x7e {
			return false
		}
	}
	return true
}

type DesktopControl interface {
	Probe(context.Context) (DesktopControlProbe, error)
	Frame(context.Context) (DesktopFrame, error)
	Observe(context.Context, DesktopObservationRequest) (json.RawMessage, error)
	Input(context.Context, DesktopInput) error
	WindowInput(context.Context, DesktopWindowInput) (json.RawMessage, error)
	Close(context.Context) error
	Available() bool
}

type DesktopControlProbe struct {
	DriverVersion   string `json:"driver_version"`
	ContractVersion string `json:"contract_version"`
	Width           int    `json:"width"`
	Height          int    `json:"height"`
	Embedded        bool   `json:"embedded"`
}

type CuaDesktopControl struct {
	command *exec.Cmd
	stdin   io.WriteCloser
	scanner *bufio.Scanner
	done    chan error

	mu        sync.Mutex
	available atomic.Bool
	requestID atomic.Uint64
	sequence  atomic.Uint64
	probe     DesktopControlProbe
}

type cuaRPCRequest struct {
	ID     uint64         `json:"id"`
	Method string         `json:"method"`
	Params map[string]any `json:"params,omitempty"`
}

type cuaRPCResponse struct {
	ID     uint64          `json:"id"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

func StartCuaDesktopControl(
	ctx context.Context,
	pythonExecutable string,
	bridgePath string,
	environment []string,
	stderr io.Writer,
) (*CuaDesktopControl, error) {
	if strings.TrimSpace(pythonExecutable) == "" ||
		strings.TrimSpace(bridgePath) == "" {
		return nil, ErrInvalidContract
	}
	command := exec.CommandContext(ctx, pythonExecutable, bridgePath)
	command.Env = append([]string(nil), environment...)
	command.Stderr = stderr
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	if err := command.Start(); err != nil {
		_ = stdin.Close()
		return nil, err
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64<<10), maximumDesktopRPCBytes)
	control := &CuaDesktopControl{
		command: command,
		stdin:   stdin,
		scanner: scanner,
		done:    make(chan error, 1),
	}
	go func() {
		control.done <- command.Wait()
		close(control.done)
		control.available.Store(false)
	}()
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	probe, err := control.Probe(probeCtx)
	if err != nil {
		_ = control.Close(context.Background())
		return nil, fmt.Errorf("private computer: Cua driver probe: %w", err)
	}
	control.probe = probe
	control.available.Store(true)
	return control, nil
}

func (control *CuaDesktopControl) Available() bool {
	return control != nil && control.available.Load()
}

func (control *CuaDesktopControl) Probe(
	ctx context.Context,
) (DesktopControlProbe, error) {
	var response struct {
		DriverVersion   string `json:"driver_version"`
		ContractVersion string `json:"contract_version"`
		Embedded        bool   `json:"embedded"`
		Screen          struct {
			Structured struct {
				Width  int `json:"width"`
				Height int `json:"height"`
			} `json:"structuredContent"`
		} `json:"screen"`
	}
	if err := control.call(ctx, "probe", nil, &response); err != nil {
		return DesktopControlProbe{}, err
	}
	probe := DesktopControlProbe{
		DriverVersion:   response.DriverVersion,
		ContractVersion: response.ContractVersion,
		Width:           response.Screen.Structured.Width,
		Height:          response.Screen.Structured.Height,
		Embedded:        response.Embedded,
	}
	if strings.TrimSpace(probe.DriverVersion) == "" ||
		strings.TrimSpace(probe.ContractVersion) == "" ||
		probe.Width < 640 || probe.Width > 3840 ||
		probe.Height < 480 || probe.Height > 2160 ||
		!probe.Embedded {
		return DesktopControlProbe{}, ErrUnsupported
	}
	return probe, nil
}

func (control *CuaDesktopControl) Frame(
	ctx context.Context,
) (DesktopFrame, error) {
	if !control.Available() {
		return DesktopFrame{}, ErrUnsupported
	}
	var response struct {
		MIMEType string `json:"mime_type"`
		Data     string `json:"data_base64"`
		Degraded bool   `json:"degraded"`
		Verified *bool  `json:"verified"`
	}
	if err := control.call(ctx, "frame", nil, &response); err != nil {
		return DesktopFrame{}, err
	}
	if response.MIMEType != "image/png" && response.MIMEType != "image/jpeg" {
		return DesktopFrame{}, ErrUnsupported
	}
	data, err := base64.StdEncoding.DecodeString(response.Data)
	if err != nil || len(data) == 0 || len(data) > maximumDesktopFrameBytes {
		return DesktopFrame{}, ErrInvalidContract
	}
	sum := sha256.Sum256(data)
	return DesktopFrame{
		Sequence:   control.sequence.Add(1),
		MIMEType:   response.MIMEType,
		Data:       data,
		Digest:     "sha256:" + hex.EncodeToString(sum[:]),
		Width:      control.probe.Width,
		Height:     control.probe.Height,
		CapturedAt: time.Now().UTC(),
		Degraded:   response.Degraded,
		Verified:   response.Verified != nil && *response.Verified,
	}, nil
}

func (control *CuaDesktopControl) Observe(
	ctx context.Context,
	request DesktopObservationRequest,
) (json.RawMessage, error) {
	if !control.Available() {
		return nil, ErrUnsupported
	}
	if err := request.Validate(); err != nil {
		return nil, err
	}
	params := map[string]any{}
	if request.PID > 0 {
		params["pid"] = request.PID
		params["window_id"] = request.WindowID
	}
	if request.Query != "" {
		params["query"] = request.Query
	}
	if request.MaxElements > 0 {
		params["max_elements"] = request.MaxElements
	}
	if request.MaxDepth > 0 {
		params["max_depth"] = request.MaxDepth
	}
	var response json.RawMessage
	if err := control.call(ctx, "observe", params, &response); err != nil {
		return nil, err
	}
	if len(response) == 0 || len(response) > 1<<20 || !json.Valid(response) {
		return nil, ErrInvalidContract
	}
	return response, nil
}

func (control *CuaDesktopControl) Input(
	ctx context.Context,
	input DesktopInput,
) error {
	if !control.Available() {
		return ErrUnsupported
	}
	if err := input.Validate(control.probe.Width, control.probe.Height); err != nil {
		return err
	}
	params := map[string]any{}
	method := string(input.Kind)
	switch input.Kind {
	case DesktopInputMove:
		params["x"], params["y"] = input.X, input.Y
	case DesktopInputClick:
		params["x"], params["y"] = input.X, input.Y
		params["button"] = input.Button
		params["count"] = max(input.Count, 1)
	case DesktopInputScroll:
		params["x"], params["y"] = input.X, input.Y
		params["direction"], params["amount"] = input.Direction, input.Amount
	case DesktopInputType:
		params["text"] = input.Text
	case DesktopInputKey:
		params["key"], params["modifiers"] = input.Key, input.Modifiers
	case DesktopInputHotkey:
		params["keys"] = input.Keys
	}
	return control.call(ctx, method, params, &struct{}{})
}

func (control *CuaDesktopControl) WindowInput(
	ctx context.Context,
	input DesktopWindowInput,
) (json.RawMessage, error) {
	if !control.Available() {
		return nil, ErrUnsupported
	}
	if err := input.Validate(); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	var params map[string]any
	if err := json.Unmarshal(encoded, &params); err != nil {
		return nil, err
	}
	var response json.RawMessage
	if err := control.call(ctx, "window_input", params, &response); err != nil {
		return nil, err
	}
	if len(response) == 0 || len(response) > 1<<20 || !json.Valid(response) {
		return nil, ErrInvalidContract
	}
	return response, nil
}

func (control *CuaDesktopControl) Close(ctx context.Context) error {
	if control == nil {
		return nil
	}
	control.available.Store(false)
	_ = control.stdin.Close()
	select {
	case err := <-control.done:
		if err == nil || errors.Is(err, os.ErrProcessDone) {
			return nil
		}
		return err
	case <-ctx.Done():
		if control.command.Process != nil {
			_ = control.command.Process.Kill()
		}
		return ctx.Err()
	}
}

func (control *CuaDesktopControl) call(
	ctx context.Context,
	method string,
	params map[string]any,
	result any,
) error {
	if control == nil {
		return ErrUnsupported
	}
	control.mu.Lock()
	defer control.mu.Unlock()
	request := cuaRPCRequest{
		ID: control.requestID.Add(1), Method: method, Params: params,
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return err
	}
	callDone := make(chan error, 1)
	var response cuaRPCResponse
	go func() {
		if _, writeErr := control.stdin.Write(append(encoded, '\n')); writeErr != nil {
			callDone <- writeErr
			return
		}
		if !control.scanner.Scan() {
			if scanErr := control.scanner.Err(); scanErr != nil {
				callDone <- scanErr
				return
			}
			callDone <- io.ErrUnexpectedEOF
			return
		}
		if decodeErr := json.Unmarshal(control.scanner.Bytes(), &response); decodeErr != nil {
			callDone <- decodeErr
			return
		}
		callDone <- nil
	}()
	select {
	case <-ctx.Done():
		control.available.Store(false)
		if control.command.Process != nil {
			_ = control.command.Process.Kill()
		}
		return ctx.Err()
	case err := <-callDone:
		if err != nil {
			control.available.Store(false)
			return err
		}
	}
	if response.ID != request.ID {
		control.available.Store(false)
		return ErrReplayConflict
	}
	if !response.OK {
		return fmt.Errorf("private computer: Cua driver: %s",
			strings.TrimSpace(response.Error))
	}
	if result == nil {
		return nil
	}
	if len(response.Result) == 0 {
		return ErrInvalidContract
	}
	return json.Unmarshal(response.Result, result)
}
