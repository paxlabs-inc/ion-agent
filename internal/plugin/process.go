package plugin

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
)

// ProcessPlugin runs a Python, Go, or other executable code plugin over a
// newline-delimited JSON lifecycle protocol. Each request must receive one
// {"ok":true} response. The child is owned from Start until Stop.
type ProcessPlugin struct {
	PluginName string
	Command    []string
	Env        []string

	mu     sync.Mutex
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
}

type processRequest struct {
	Method string `json:"method"`
	Event  *Event `json:"event,omitempty"`
}

type processResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func (plugin *ProcessPlugin) Name() string { return plugin.PluginName }

func (plugin *ProcessPlugin) Start(ctx context.Context, sdk SDK) error {
	if err := sdk.Validate(); err != nil {
		return err
	}
	plugin.mu.Lock()
	defer plugin.mu.Unlock()
	if strings.TrimSpace(plugin.PluginName) == "" || len(plugin.Command) == 0 {
		return fmt.Errorf("plugin: process name and command are required")
	}
	if plugin.cmd != nil {
		return fmt.Errorf("plugin: process is already started")
	}
	// Do not bind the process lifetime to the caller's short Start context.
	cmd := exec.Command(plugin.Command[0], plugin.Command[1:]...)
	if len(plugin.Env) > 0 {
		cmd.Env = append(cmd.Environ(), plugin.Env...)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return err
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return err
	}
	plugin.cmd, plugin.stdin, plugin.stdout = cmd, stdin, bufio.NewReader(stdout)
	if err := plugin.exchangeLocked(ctx, processRequest{Method: "start"}); err != nil {
		_ = plugin.killLocked()
		return err
	}
	return nil
}

func (plugin *ProcessPlugin) Hook(ctx context.Context, event Event) error {
	plugin.mu.Lock()
	defer plugin.mu.Unlock()
	copy := cloneEvent(event)
	return plugin.exchangeLocked(ctx, processRequest{Method: "hook", Event: &copy})
}

func (plugin *ProcessPlugin) Stop(ctx context.Context) error {
	plugin.mu.Lock()
	defer plugin.mu.Unlock()
	if plugin.cmd == nil {
		return nil
	}
	exchangeErr := plugin.exchangeLocked(ctx, processRequest{Method: "stop"})
	if plugin.cmd == nil {
		return exchangeErr
	}
	closeErr := plugin.stdin.Close()
	waitErr := plugin.cmd.Wait()
	plugin.cmd, plugin.stdin, plugin.stdout = nil, nil, nil
	return errors.Join(exchangeErr, closeErr, waitErr)
}

func (plugin *ProcessPlugin) exchangeLocked(ctx context.Context, request processRequest) error {
	if plugin.cmd == nil {
		return fmt.Errorf("plugin: process is not started")
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return err
	}
	if _, err := plugin.stdin.Write(append(payload, '\n')); err != nil {
		return fmt.Errorf("plugin: process write: %w", err)
	}
	type readResult struct {
		line []byte
		err  error
	}
	result := make(chan readResult, 1)
	reader := plugin.stdout
	go func() {
		line, readErr := reader.ReadBytes('\n')
		result <- readResult{line: line, err: readErr}
	}()
	select {
	case <-ctx.Done():
		_ = plugin.killLocked()
		return ctx.Err()
	case read := <-result:
		if read.err != nil {
			return fmt.Errorf("plugin: process read: %w", read.err)
		}
		var response processResponse
		if err := json.Unmarshal(read.line, &response); err != nil {
			return fmt.Errorf("plugin: invalid process response: %w", err)
		}
		if !response.OK {
			return fmt.Errorf("plugin: process rejected lifecycle event: %s", response.Error)
		}
		return nil
	}
}

func (plugin *ProcessPlugin) killLocked() error {
	if plugin.cmd == nil || plugin.cmd.Process == nil {
		return nil
	}
	err := plugin.cmd.Process.Kill()
	plugin.cmd, plugin.stdin, plugin.stdout = nil, nil, nil
	return err
}
