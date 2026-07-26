// Package builtin provides the production baseline tool catalog. Every
// registration is root-confined, bounded, cancellation-aware, and routed
// through the parent tools.Manager policy interceptor.
package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	"github.com/paxlabs-inc/ion-agent/internal/memory"
	"github.com/paxlabs-inc/ion-agent/internal/memory/cortex"
	"github.com/paxlabs-inc/ion-agent/internal/skills"
	"github.com/paxlabs-inc/ion-agent/internal/tools"
)

const (
	maxReadBytes   = 1 << 20
	maxOutputBytes = 128 << 10
	maxSearchHits  = 200
)

// Config contains only the dependencies needed by the baseline catalog.
type Config struct {
	Workspace             string
	Skills                *skills.Store
	Memory                memoryStore
	TavilyAPIKey          string
	TavilySearchEndpoint  string
	SearXNGSearchEndpoint string
	HTTPClient            *http.Client
}

type memoryStore interface {
	Write(context.Context, memory.Type, []byte, string) (*cortex.Memory, error)
	Resolve(uuid.UUID) (*cortex.Memory, error)
	Update(context.Context, uuid.UUID, []byte, string) (*cortex.Memory, error)
	ListByType(memory.Type) []uuid.UUID
}

type actorMemoryStore interface {
	WriteForActor(context.Context, string, memory.Type, []byte, string) (*cortex.Memory, error)
	ResolveForActor(string, uuid.UUID) (*cortex.Memory, error)
	UpdateForActor(context.Context, string, uuid.UUID, []byte, string) (*cortex.Memory, error)
	ListByTypeForActor(string, memory.Type) []uuid.UUID
	Actor() string
}

type archivedMemoryStore interface {
	ResolveVersion(uuid.UUID, uint64) (*cortex.Memory, error)
}

type actorArchivedMemoryStore interface {
	ResolveVersionForActor(string, uuid.UUID, uint64) (*cortex.Memory, error)
}

func memoryActor(ctx context.Context, store memoryStore) string {
	if scope, ok := controlplane.ApprovalScopeFromContext(ctx); ok {
		return scope.ActorID.String()
	}
	if scoped, ok := store.(actorMemoryStore); ok {
		return scoped.Actor()
	}
	return "operator"
}

func actorMemoryIDs(
	ctx context.Context,
	store memoryStore,
	memoryType memory.Type,
) []uuid.UUID {
	if scoped, ok := store.(actorMemoryStore); ok {
		return scoped.ListByTypeForActor(memoryActor(ctx, store), memoryType)
	}
	return store.ListByType(memoryType)
}

func resolveActorMemory(
	ctx context.Context,
	store memoryStore,
	id uuid.UUID,
) (*cortex.Memory, error) {
	if scoped, ok := store.(actorMemoryStore); ok {
		return scoped.ResolveForActor(memoryActor(ctx, store), id)
	}
	return store.Resolve(id)
}

func writeActorMemory(
	ctx context.Context,
	store memoryStore,
	memoryType memory.Type,
	payload []byte,
	createdBy string,
) (*cortex.Memory, error) {
	if scoped, ok := store.(actorMemoryStore); ok {
		return scoped.WriteForActor(
			ctx, memoryActor(ctx, store), memoryType, payload, createdBy,
		)
	}
	return store.Write(ctx, memoryType, payload, createdBy)
}

func updateActorMemory(
	ctx context.Context,
	store memoryStore,
	id uuid.UUID,
	payload []byte,
	createdBy string,
) (*cortex.Memory, error) {
	if scoped, ok := store.(actorMemoryStore); ok {
		return scoped.UpdateForActor(
			ctx, memoryActor(ctx, store), id, payload, createdBy,
		)
	}
	return store.Update(ctx, id, payload, createdBy)
}

func resolveArchivedActorMemory(
	ctx context.Context,
	store memoryStore,
	id uuid.UUID,
	version uint64,
) (*cortex.Memory, error) {
	if scoped, ok := store.(actorArchivedMemoryStore); ok {
		return scoped.ResolveVersionForActor(
			memoryActor(ctx, store), id, version,
		)
	}
	if archived, ok := store.(archivedMemoryStore); ok {
		return archived.ResolveVersion(id, version)
	}
	return nil, fmt.Errorf("memory recovery is unavailable")
}

// Register installs the production baseline into manager.
func Register(ctx context.Context, manager *tools.Manager, config Config) error {
	if manager == nil {
		return fmt.Errorf("builtin tools: manager is required")
	}
	workspace, err := secureRoot(config.Workspace)
	if err != nil {
		return err
	}
	if err := RegisterWorkspace(ctx, manager, workspace); err != nil {
		return err
	}
	catalog := []tools.Registration{
		webFetch(config.HTTPClient),
		webSearch(searchProviderConfig{
			TavilyAPIKey:          config.TavilyAPIKey,
			TavilySearchEndpoint:  config.TavilySearchEndpoint,
			SearXNGSearchEndpoint: config.SearXNGSearchEndpoint,
		}, config.HTTPClient),
		unavailableRegistration(
			"mcp_invoke",
			"Invoke a namespaced tool from a configured MCP server.",
			tools.ClassificationYellow,
			"configure and enable an MCP server to import its tools",
			true,
		),
	}
	if config.Skills != nil {
		catalog = append(catalog, skillTools(config.Skills)...)
	}
	if config.Memory != nil {
		catalog = append(catalog, memoryTools(config.Memory)...)
	}
	for _, registration := range catalog {
		if err := manager.Register(ctx, registration); err != nil {
			return fmt.Errorf("builtin tools: register %s: %w", registration.Name, err)
		}
	}
	return nil
}

// RegisterWorkspace installs only the root-confined filesystem, process, and
// Git tools. Studio turns use it to replace the general workspace surface with
// the selected project's real WorkspaceHost root while retaining the shared
// policy pipeline and all non-workspace capabilities.
func RegisterWorkspace(ctx context.Context, manager *tools.Manager, workspace string) error {
	if manager == nil {
		return fmt.Errorf("builtin tools: manager is required")
	}
	root, err := secureRoot(workspace)
	if err != nil {
		return err
	}
	catalog := []tools.Registration{
		fileList(root),
		fileStat(root),
		fileRead(root),
		fileSearch(root),
		fileWrite(root),
		filePatch(root),
		shellExecute(root),
		gitTool(root, "git_status", "Inspect working-tree status.", "status",
			[]string{"status", "--short", "--branch"}),
		gitTool(root, "git_diff", "Inspect working-tree changes.", "diff",
			[]string{"diff", "--no-ext-diff"}),
		gitTool(root, "git_log", "Inspect recent commit history.", "log",
			[]string{"log", "--oneline", "--decorate", "-n"}),
		gitTool(root, "git_show", "Inspect one commit or object.", "show",
			[]string{"show", "--stat", "--oneline"}),
	}
	for _, registration := range catalog {
		if err := manager.Register(ctx, registration); err != nil {
			return fmt.Errorf("builtin tools: register %s: %w", registration.Name, err)
		}
	}
	return nil
}

func fileList(root string) tools.Registration {
	return registration("filesystem_list", "List files and directories inside the workspace.",
		`{"type":"object","properties":{"path":{"type":"string"},"limit":{"type":"integer","minimum":1,"maximum":1000}},"additionalProperties":false}`,
		tools.ClassificationGreen, func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
			var input struct {
				Path  string `json:"path"`
				Limit int    `json:"limit"`
			}
			if err := decode(raw, &input); err != nil {
				return nil, err
			}
			if input.Limit == 0 {
				input.Limit = 200
			}
			path, err := resolveExisting(root, input.Path)
			if err != nil {
				return nil, err
			}
			entries, err := os.ReadDir(path)
			if err != nil {
				return nil, err
			}
			if len(entries) > input.Limit {
				entries = entries[:input.Limit]
			}
			type item struct {
				Name string `json:"name"`
				Type string `json:"type"`
			}
			result := make([]item, 0, len(entries))
			for _, entry := range entries {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				kind := "file"
				if entry.IsDir() {
					kind = "directory"
				} else if entry.Type()&os.ModeSymlink != 0 {
					kind = "symlink"
				}
				result = append(result, item{Name: entry.Name(), Type: kind})
			}
			return marshal(map[string]any{
				"path": relative(root, path), "entries": result,
				"truncated": len(entries) == input.Limit,
			})
		})
}

func fileStat(root string) tools.Registration {
	return registration("filesystem_stat", "Inspect metadata for a workspace path.",
		pathSchema, tools.ClassificationGreen,
		func(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
			var input struct {
				Path string `json:"path"`
			}
			if err := decode(raw, &input); err != nil {
				return nil, err
			}
			path, err := resolveExisting(root, input.Path)
			if err != nil {
				return nil, err
			}
			info, err := os.Stat(path)
			if err != nil {
				return nil, err
			}
			return marshal(map[string]any{
				"path": relative(root, path), "size": info.Size(),
				"mode": info.Mode().String(), "directory": info.IsDir(),
				"modified_at": info.ModTime().UTC(),
			})
		})
}

func fileRead(root string) tools.Registration {
	return registration("filesystem_read", "Read a bounded UTF-8 or text workspace file.",
		`{"type":"object","required":["path"],"properties":{"path":{"type":"string"},"max_bytes":{"type":"integer","minimum":1,"maximum":1048576}},"additionalProperties":false}`,
		tools.ClassificationGreen, func(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
			var input struct {
				Path     string `json:"path"`
				MaxBytes int64  `json:"max_bytes"`
			}
			if err := decode(raw, &input); err != nil {
				return nil, err
			}
			if input.MaxBytes == 0 {
				input.MaxBytes = maxReadBytes
			}
			path, err := resolveExisting(root, input.Path)
			if err != nil {
				return nil, err
			}
			file, err := os.Open(path)
			if err != nil {
				return nil, err
			}
			defer file.Close()
			content, err := io.ReadAll(io.LimitReader(file, input.MaxBytes+1))
			if err != nil {
				return nil, err
			}
			truncated := int64(len(content)) > input.MaxBytes
			if truncated {
				content = content[:input.MaxBytes]
			}
			if bytes.IndexByte(content, 0) >= 0 {
				return nil, fmt.Errorf("filesystem_read: binary files are not supported")
			}
			return marshal(map[string]any{
				"path": relative(root, path), "content": string(content),
				"truncated": truncated,
			})
		})
}

func fileSearch(root string) tools.Registration {
	return registration("filesystem_search", "Search text files inside the workspace.",
		`{"type":"object","required":["query"],"properties":{"query":{"type":"string","minLength":1},"path":{"type":"string"},"regex":{"type":"boolean"},"limit":{"type":"integer","minimum":1,"maximum":200}},"additionalProperties":false}`,
		tools.ClassificationGreen, func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
			var input struct {
				Query string `json:"query"`
				Path  string `json:"path"`
				Regex bool   `json:"regex"`
				Limit int    `json:"limit"`
			}
			if err := decode(raw, &input); err != nil {
				return nil, err
			}
			if input.Limit == 0 {
				input.Limit = 100
			}
			base, err := resolveExisting(root, input.Path)
			if err != nil {
				return nil, err
			}
			var expression *regexp.Regexp
			if input.Regex {
				expression, err = regexp.Compile(input.Query)
				if err != nil {
					return nil, fmt.Errorf("filesystem_search: invalid regex: %w", err)
				}
			}
			type hit struct {
				Path string `json:"path"`
				Line int    `json:"line"`
				Text string `json:"text"`
			}
			hits := make([]hit, 0, input.Limit)
			truncated := false
			err = filepath.WalkDir(base, func(path string, entry os.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if err := ctx.Err(); err != nil {
					return err
				}
				if entry.Type()&os.ModeSymlink != 0 {
					if entry.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
				if entry.IsDir() {
					return nil
				}
				info, err := entry.Info()
				if err != nil || info.Size() > maxReadBytes {
					return err
				}
				payload, err := os.ReadFile(path)
				if err != nil || bytes.IndexByte(payload, 0) >= 0 {
					return err
				}
				for index, line := range strings.Split(string(payload), "\n") {
					match := strings.Contains(line, input.Query)
					if expression != nil {
						match = expression.MatchString(line)
					}
					if match {
						hits = append(hits, hit{
							Path: relative(root, path), Line: index + 1,
							Text: truncate(line, 1024),
						})
						if len(hits) >= input.Limit {
							truncated = true
							return errSearchLimit
						}
					}
				}
				return nil
			})
			if err != nil && !errors.Is(err, errSearchLimit) {
				return nil, err
			}
			return marshal(map[string]any{"matches": hits, "truncated": truncated})
		})
}

func fileWrite(root string) tools.Registration {
	return registration("filesystem_write", "Create or replace a workspace text file. Missing parent directories are created safely inside the workspace.",
		`{"type":"object","required":["path","content"],"properties":{"path":{"type":"string"},"content":{"type":"string","maxLength":1048576},"create_only":{"type":"boolean"}},"additionalProperties":false}`,
		tools.ClassificationYellow, func(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
			var input struct {
				Path       string `json:"path"`
				Content    string `json:"content"`
				CreateOnly bool   `json:"create_only"`
			}
			if err := decode(raw, &input); err != nil {
				return nil, err
			}
			path, err := resolveWritable(root, input.Path)
			if err != nil {
				return nil, err
			}
			flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
			if input.CreateOnly {
				flags |= os.O_EXCL
			}
			file, err := os.OpenFile(path, flags, 0o600)
			if err != nil {
				return nil, err
			}
			_, writeErr := io.WriteString(file, input.Content)
			syncErr := file.Sync()
			closeErr := file.Close()
			if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
				return nil, err
			}
			return marshal(map[string]any{
				"path": relative(root, path), "bytes_written": len(input.Content),
			})
		})
}

func filePatch(root string) tools.Registration {
	return registration("filesystem_patch", "Replace one exact text occurrence in a workspace file.",
		`{"type":"object","required":["path","old_text","new_text"],"properties":{"path":{"type":"string"},"old_text":{"type":"string","minLength":1},"new_text":{"type":"string"},"replace_all":{"type":"boolean"}},"additionalProperties":false}`,
		tools.ClassificationYellow, func(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
			var input struct {
				Path       string `json:"path"`
				OldText    string `json:"old_text"`
				NewText    string `json:"new_text"`
				ReplaceAll bool   `json:"replace_all"`
			}
			if err := decode(raw, &input); err != nil {
				return nil, err
			}
			path, err := resolveExisting(root, input.Path)
			if err != nil {
				return nil, err
			}
			payload, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			if len(payload) > maxReadBytes || bytes.IndexByte(payload, 0) >= 0 {
				return nil, fmt.Errorf("filesystem_patch: file is binary or exceeds %d bytes", maxReadBytes)
			}
			count := strings.Count(string(payload), input.OldText)
			if count == 0 {
				return nil, fmt.Errorf("filesystem_patch: old_text was not found")
			}
			if count != 1 && !input.ReplaceAll {
				return nil, fmt.Errorf("filesystem_patch: old_text occurs %d times; set replace_all", count)
			}
			limit := 1
			if input.ReplaceAll {
				limit = -1
			}
			updated := strings.Replace(string(payload), input.OldText, input.NewText, limit)
			info, err := os.Stat(path)
			if err != nil {
				return nil, err
			}
			if err := writeAtomic(path, []byte(updated), info.Mode().Perm()); err != nil {
				return nil, err
			}
			replaced := 1
			if input.ReplaceAll {
				replaced = count
			}
			return marshal(map[string]any{
				"path": relative(root, path), "replacements": replaced,
			})
		})
}

func shellExecute(root string) tools.Registration {
	item := registration("shell_execute", "Run one bounded process inside the workspace without a shell. Command may be an executable or a quoted command line; args may be an array or a quoted argument string. Use working_directory instead of cd; a leading `cd DIR && COMMAND` is normalized safely. Workspace-contained absolute directories are accepted. Pipes, redirects, substitutions, and other shell syntax are passed literally, never evaluated.",
		`{"type":"object","required":["command"],"properties":{"command":{"type":"string","minLength":1,"maxLength":65536,"description":"Executable name or quoted command line."},"args":{"description":"Arguments as a JSON string array or quoted argument string.","oneOf":[{"type":"array","maxItems":128,"items":{"type":"string"}},{"type":"string","maxLength":65536}]},"working_directory":{"type":"string"},"timeout_seconds":{"type":"integer","minimum":1,"maximum":120}},"additionalProperties":false}`,
		tools.ClassificationYellow, func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
			var input struct {
				Command          string          `json:"command"`
				Args             json.RawMessage `json:"args"`
				WorkingDirectory string          `json:"working_directory"`
				TimeoutSeconds   int             `json:"timeout_seconds"`
			}
			if err := decode(raw, &input); err != nil {
				return nil, err
			}
			executable, arguments, err := normalizeProcessInvocation(
				input.Command, input.Args,
			)
			if err != nil {
				return nil, err
			}
			if executable == "cd" {
				var changed bool
				input.WorkingDirectory, executable, arguments, changed, err =
					normalizeChangeDirectoryIntent(input.WorkingDirectory, arguments)
				if err != nil {
					return nil, err
				}
				if !changed {
					directory, resolveErr := resolveExisting(root, input.WorkingDirectory)
					if resolveErr != nil {
						return nil, resolveErr
					}
					return marshal(map[string]any{
						"exit_code": 0, "output": directory + "\n", "truncated": false,
						"timed_out": false, "corrected_intent": "working_directory",
						"suggestion": "Pass this directory as working_directory on the command you want to run.",
					})
				}
			}
			directory, err := resolveExisting(root, input.WorkingDirectory)
			if err != nil {
				return nil, err
			}
			if err := validateProcessPathArguments(root, directory, arguments); err != nil {
				return nil, err
			}
			if input.TimeoutSeconds == 0 {
				input.TimeoutSeconds = 30
			}
			runCtx, cancel := context.WithTimeout(ctx, time.Duration(input.TimeoutSeconds)*time.Second)
			defer cancel()
			command := exec.Command(executable, arguments...)
			command.Dir = directory
			command.Env = safeEnvironment()
			command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			var output boundedBuffer
			command.Stdout = &output
			command.Stderr = &output
			if err := command.Start(); err != nil {
				if errors.Is(err, os.ErrNotExist) || errors.Is(err, exec.ErrNotFound) {
					return marshal(map[string]any{
						"exit_code": 127, "output": "", "truncated": false,
						"timed_out": false, "error_code": "executable_not_found",
						"error":      fmt.Sprintf("executable %q was not found", executable),
						"suggestion": "Inspect the project toolchain or use an available executable; do not retry the identical command.",
					})
				}
				return nil, fmt.Errorf("shell_execute: start process: %w", err)
			}
			wait := make(chan error, 1)
			go func() { wait <- command.Wait() }()
			var waitErr error
			select {
			case waitErr = <-wait:
			case <-runCtx.Done():
				_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
				waitErr = <-wait
			}
			exitCode := 0
			if waitErr != nil {
				var exitErr *exec.ExitError
				if errors.As(waitErr, &exitErr) {
					exitCode = exitErr.ExitCode()
				} else {
					return nil, waitErr
				}
			}
			return marshal(map[string]any{
				"exit_code": exitCode, "output": output.String(),
				"truncated": output.truncated,
				"timed_out": errors.Is(runCtx.Err(), context.DeadlineExceeded),
			})
		})
	item.Timeout = 125 * time.Second
	return item
}

func normalizeChangeDirectoryIntent(
	workingDirectory string,
	arguments []string,
) (string, string, []string, bool, error) {
	if len(arguments) == 0 {
		return "", "", nil, false, fmt.Errorf(
			"shell_execute: cd requires a workspace directory; prefer working_directory",
		)
	}
	if strings.TrimSpace(workingDirectory) != "" {
		return "", "", nil, false, fmt.Errorf(
			"shell_execute: do not combine cd with working_directory",
		)
	}
	directory := strings.TrimSuffix(strings.TrimSpace(arguments[0]), ";")
	if directory == "" {
		return "", "", nil, false, fmt.Errorf("shell_execute: cd directory is empty")
	}
	if len(arguments) == 1 {
		return directory, "", nil, false, nil
	}
	separator := arguments[1]
	if separator != "&&" && separator != ";" {
		return "", "", nil, false, fmt.Errorf(
			"shell_execute: cd is not persistent; use working_directory or `cd DIR && COMMAND`",
		)
	}
	if len(arguments) < 3 || strings.TrimSpace(arguments[2]) == "" {
		return "", "", nil, false, fmt.Errorf("shell_execute: command after cd is required")
	}
	return directory, arguments[2], append([]string(nil), arguments[3:]...), true, nil
}

func normalizeProcessInvocation(
	command string,
	rawArgs json.RawMessage,
) (string, []string, error) {
	commandWords, err := splitQuotedWords(command)
	if err != nil || len(commandWords) == 0 {
		return "", nil, fmt.Errorf("shell_execute: command line is invalid")
	}
	arguments := append([]string(nil), commandWords[1:]...)
	if len(rawArgs) > 0 && string(rawArgs) != "null" {
		var array []string
		if err := json.Unmarshal(rawArgs, &array); err == nil {
			arguments = append(arguments, array...)
		} else {
			var line string
			if stringErr := json.Unmarshal(rawArgs, &line); stringErr != nil {
				return "", nil, fmt.Errorf(
					"shell_execute: args must be a string array or quoted argument string",
				)
			}
			words, splitErr := splitQuotedWords(line)
			if splitErr != nil {
				return "", nil, fmt.Errorf("shell_execute: argument line is invalid")
			}
			arguments = append(arguments, words...)
		}
	}
	if len(arguments) > 128 {
		return "", nil, fmt.Errorf("shell_execute: at most 128 arguments are allowed")
	}
	return commandWords[0], arguments, nil
}

func validateProcessPathArguments(root, directory string, arguments []string) error {
	for _, argument := range arguments {
		candidate := strings.TrimSpace(argument)
		if separator := strings.IndexByte(candidate, '='); separator >= 0 {
			candidate = strings.TrimSpace(candidate[separator+1:])
		}
		candidate = strings.TrimPrefix(candidate, "file://")
		if candidate == "" || strings.Contains(candidate, "://") {
			continue
		}
		if !filepath.IsAbs(candidate) &&
			candidate != ".." &&
			!strings.HasPrefix(candidate, ".."+string(filepath.Separator)) {
			continue
		}
		path := candidate
		if !filepath.IsAbs(path) {
			path = filepath.Join(directory, path)
		}
		if !within(root, filepath.Clean(path)) {
			return fmt.Errorf("shell_execute: path argument is outside the registered workspace")
		}
	}
	return nil
}

// splitQuotedWords implements only tokenization. It deliberately performs no
// shell expansion, substitution, piping, redirection, or command chaining.
func splitQuotedWords(value string) ([]string, error) {
	var (
		words       []string
		word        strings.Builder
		quote       rune
		escaped     bool
		wordStarted bool
	)
	flush := func() {
		if !wordStarted {
			return
		}
		words = append(words, word.String())
		word.Reset()
		wordStarted = false
	}
	for _, current := range strings.TrimSpace(value) {
		if escaped {
			if current != '\n' {
				word.WriteRune(current)
				wordStarted = true
			}
			escaped = false
			continue
		}
		if quote != 0 {
			if current == quote {
				quote = 0
				wordStarted = true
				continue
			}
			if quote == '"' && current == '\\' {
				escaped = true
				continue
			}
			word.WriteRune(current)
			wordStarted = true
			continue
		}
		switch current {
		case '\'', '"':
			quote = current
			wordStarted = true
		case '\\':
			escaped = true
		case ' ', '\t', '\r', '\n':
			flush()
		default:
			word.WriteRune(current)
			wordStarted = true
		}
	}
	if escaped || quote != 0 {
		return nil, fmt.Errorf("unterminated quote or escape")
	}
	flush()
	return words, nil
}

func gitTool(root, name, description, mode string, base []string) tools.Registration {
	schema := `{"type":"object","properties":{"revision":{"type":"string"},"limit":{"type":"integer","minimum":1,"maximum":100},"path":{"type":"string"}},"additionalProperties":false}`
	return registration(name, description, schema, tools.ClassificationGreen,
		func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
			var input struct {
				Revision string `json:"revision"`
				Path     string `json:"path"`
				Limit    int    `json:"limit"`
			}
			if err := decode(raw, &input); err != nil {
				return nil, err
			}
			args := append([]string{"-C", root}, base...)
			switch mode {
			case "log":
				if input.Limit == 0 {
					input.Limit = 20
				}
				args = append(args, fmt.Sprint(input.Limit))
			case "show":
				revision := strings.TrimSpace(input.Revision)
				if revision == "" {
					revision = "HEAD"
				}
				if strings.HasPrefix(revision, "-") {
					return nil, fmt.Errorf("%s: invalid revision", name)
				}
				args = append(args, revision)
			}
			if path := strings.TrimSpace(input.Path); path != "" {
				resolved, err := resolveExisting(root, path)
				if err != nil {
					return nil, err
				}
				args = append(args, "--", relative(root, resolved))
			}
			command := exec.CommandContext(ctx, "git", args...)
			command.Env = safeEnvironment()
			var output boundedBuffer
			command.Stdout, command.Stderr = &output, &output
			err := command.Run()
			exitCode := 0
			if err != nil {
				var exitErr *exec.ExitError
				if !errors.As(err, &exitErr) {
					return nil, err
				}
				exitCode = exitErr.ExitCode()
			}
			return marshal(map[string]any{
				"exit_code": exitCode, "output": output.String(),
				"truncated": output.truncated,
			})
		})
}

func webFetch(client *http.Client) tools.Registration {
	if client == nil {
		client = secureHTTPClient()
	}
	item := registration("web_fetch", "Fetch bounded public HTTP or HTTPS text with SSRF protection.",
		`{"type":"object","required":["url"],"properties":{"url":{"type":"string","minLength":1},"max_bytes":{"type":"integer","minimum":1,"maximum":1048576}},"additionalProperties":false}`,
		tools.ClassificationYellow, func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
			var input struct {
				URL      string `json:"url"`
				MaxBytes int64  `json:"max_bytes"`
			}
			if err := decode(raw, &input); err != nil {
				return nil, err
			}
			if input.MaxBytes == 0 {
				input.MaxBytes = maxReadBytes
			}
			if err := validatePublicURL(ctx, input.URL); err != nil {
				return nil, err
			}
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, input.URL, nil)
			if err != nil {
				return nil, err
			}
			request.Header.Set("User-Agent", "Ion-Agent/1.0")
			response, err := client.Do(request)
			if err != nil {
				return nil, err
			}
			defer response.Body.Close()
			body, err := io.ReadAll(io.LimitReader(response.Body, input.MaxBytes+1))
			if err != nil {
				return nil, err
			}
			truncated := int64(len(body)) > input.MaxBytes
			if truncated {
				body = body[:input.MaxBytes]
			}
			return marshal(map[string]any{
				"url": response.Request.URL.String(), "status": response.StatusCode,
				"content_type": response.Header.Get("Content-Type"),
				"text":         extractText(string(body)), "truncated": truncated,
			})
		})
	item.ExternallyCommunicating = true
	return item
}

type searXNGResponse struct {
	Query               string     `json:"query"`
	UnresponsiveEngines [][]string `json:"unresponsive_engines"`
	Results             []struct {
		URL           string   `json:"url"`
		Title         string   `json:"title"`
		Content       string   `json:"content"`
		PublishedDate string   `json:"publishedDate"`
		Engine        string   `json:"engine"`
		Engines       []string `json:"engines"`
		Score         float64  `json:"score"`
	} `json:"results"`
}

type tavilySearchResponse struct {
	Query        string `json:"query"`
	Answer       string `json:"answer"`
	ResponseTime string `json:"response_time"`
	RequestID    string `json:"request_id"`
	Results      []struct {
		URL           string  `json:"url"`
		Title         string  `json:"title"`
		Content       string  `json:"content"`
		PublishedDate string  `json:"published_date"`
		Score         float64 `json:"score"`
	} `json:"results"`
}

type rankedSearchResult struct {
	URL           string   `json:"url"`
	Title         string   `json:"title"`
	Snippet       string   `json:"snippet,omitempty"`
	PublishedDate string   `json:"published_date,omitempty"`
	Source        string   `json:"source,omitempty"`
	Engine        string   `json:"engine,omitempty"`
	Engines       []string `json:"engines,omitempty"`
	Score         float64  `json:"score,omitempty"`
}

func normalizeTavilyResults(
	body []byte,
	query string,
	limit int,
) (map[string]any, error) {
	var response tavilySearchResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf(
			"search endpoint did not return Tavily JSON: %w", err,
		)
	}
	if limit <= 0 {
		limit = 8
	}
	results := make([]rankedSearchResult, 0, min(limit, len(response.Results)))
	for _, item := range response.Results {
		target, err := url.Parse(strings.TrimSpace(item.URL))
		if err != nil || (target.Scheme != "http" && target.Scheme != "https") ||
			strings.TrimSpace(target.Hostname()) == "" {
			continue
		}
		results = append(results, rankedSearchResult{
			URL:           target.String(),
			Title:         boundedSearchText(item.Title, 512),
			Snippet:       boundedSearchText(item.Content, 2_000),
			PublishedDate: boundedSearchText(item.PublishedDate, 128),
			Source:        strings.ToLower(target.Hostname()),
			Score:         item.Score,
		})
		if len(results) == limit {
			break
		}
	}
	if strings.TrimSpace(response.Query) != "" {
		query = strings.TrimSpace(response.Query)
	}
	state := "ready"
	if len(results) == 0 {
		state = "empty"
	}
	return map[string]any{
		"query": query, "answer": boundedSearchText(response.Answer, 4_000),
		"result_count": len(results), "results": results,
		"source": "tavily", "provider": "tavily", "state": state,
		"response_time": boundedSearchText(response.ResponseTime, 64),
		"request_id":    boundedSearchText(response.RequestID, 160),
	}, nil
}

func normalizeSearXNGResults(
	body []byte,
	query string,
	limit int,
) (map[string]any, error) {
	var response searXNGResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf(
			"search endpoint did not return SearXNG JSON: %w", err,
		)
	}
	if limit <= 0 {
		limit = 8
	}
	results := make([]rankedSearchResult, 0, min(limit, len(response.Results)))
	for _, item := range response.Results {
		target, err := url.Parse(strings.TrimSpace(item.URL))
		if err != nil || (target.Scheme != "http" && target.Scheme != "https") ||
			strings.TrimSpace(target.Hostname()) == "" {
			continue
		}
		results = append(results, rankedSearchResult{
			URL: target.String(), Title: strings.TrimSpace(item.Title),
			Snippet:       strings.TrimSpace(item.Content),
			PublishedDate: strings.TrimSpace(item.PublishedDate),
			Engine:        strings.TrimSpace(item.Engine),
			Engines:       append([]string(nil), item.Engines...),
			Score:         item.Score,
		})
		if len(results) == limit {
			break
		}
	}
	if strings.TrimSpace(response.Query) != "" {
		query = strings.TrimSpace(response.Query)
	}
	unresponsive := make([][]string, 0, min(8, len(response.UnresponsiveEngines)))
	for _, engine := range response.UnresponsiveEngines {
		if len(engine) < 2 {
			continue
		}
		unresponsive = append(unresponsive, []string{
			boundedSearchMetadata(engine[0]),
			boundedSearchMetadata(engine[1]),
		})
		if len(unresponsive) == 8 {
			break
		}
	}
	state := "ready"
	if len(results) == 0 && len(unresponsive) > 0 {
		state = "degraded"
	}
	return map[string]any{
		"query": query, "result_count": len(results),
		"results": results, "source": "searxng", "provider": "searxng",
		"state":                state,
		"unresponsive_engines": unresponsive,
	}, nil
}

func boundedSearchMetadata(value string) string {
	return boundedSearchText(value, 160)
}

func boundedSearchText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit > 0 && len(value) > limit {
		value = value[:limit]
	}
	return value
}

type searchProviderConfig struct {
	TavilyAPIKey          string
	TavilySearchEndpoint  string
	SearXNGSearchEndpoint string
}

func webSearch(config searchProviderConfig, client *http.Client) tools.Registration {
	config.TavilyAPIKey = strings.TrimSpace(config.TavilyAPIKey)
	config.TavilySearchEndpoint = strings.TrimSpace(config.TavilySearchEndpoint)
	config.SearXNGSearchEndpoint = strings.TrimSpace(config.SearXNGSearchEndpoint)
	if client == nil {
		client = secureHTTPClient()
	}
	item := registration("web_search", "Search the public web through Tavily by default and return bounded ranked source metadata for Ion's native Research view. Tavily automatically falls back to SearXNG when unavailable. Use the browser only when direct page interaction, authentication, downloads, visual inspection, or JavaScript-only content is required.",
		`{"type":"object","required":["query"],"properties":{"query":{"type":"string","minLength":1},"category":{"type":"string","enum":["general","news"]},"limit":{"type":"integer","minimum":1,"maximum":20}},"additionalProperties":false}`,
		tools.ClassificationYellow, func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
			var input struct {
				Query    string `json:"query"`
				Category string `json:"category"`
				Limit    int    `json:"limit"`
			}
			if err := decode(raw, &input); err != nil {
				return nil, err
			}
			input.Query = strings.TrimSpace(input.Query)
			if input.Query == "" || len(input.Query) > 512 {
				return nil, fmt.Errorf(
					"web_search query must contain 1 to 512 bytes",
				)
			}
			if input.Limit == 0 {
				input.Limit = 8
			}
			category := searchCategory(input.Query, input.Category)
			if config.TavilyAPIKey != "" {
				results, tavilyErr := searchTavily(
					ctx, client, config.TavilySearchEndpoint,
					config.TavilyAPIKey, input.Query, category, input.Limit,
				)
				if tavilyErr == nil {
					results["category"] = category
					results["attempted_categories"] = []string{category}
					results["attempted_providers"] = []string{"tavily"}
					if searchResultCount(results) > 0 {
						return marshal(results)
					}
				}
				fallback, fallbackErr := searchSearXNG(
					ctx, client, config.SearXNGSearchEndpoint,
					input.Query, category, input.Category, input.Limit,
				)
				if fallbackErr == nil {
					fallback["attempted_providers"] = []string{
						"tavily", "searxng",
					}
					fallback["provider_fallback_from"] = "tavily"
					if tavilyErr != nil {
						fallback["provider_fallback_reason"] =
							boundedSearchMetadata(tavilyErr.Error())
					} else {
						fallback["provider_fallback_reason"] =
							"tavily returned no ranked results"
					}
					return marshal(fallback)
				}
				if tavilyErr != nil {
					return nil, fmt.Errorf(
						"tavily search unavailable: %v; SearXNG fallback unavailable: %w",
						tavilyErr, fallbackErr,
					)
				}
				results["attempted_providers"] = []string{
					"tavily", "searxng",
				}
				results["fallback_state"] = "unavailable"
				results["fallback_reason"] =
					boundedSearchMetadata(fallbackErr.Error())
				return marshal(results)
			}
			results, err := searchSearXNG(
				ctx, client, config.SearXNGSearchEndpoint,
				input.Query, category, input.Category, input.Limit,
			)
			if err != nil {
				return nil, err
			}
			results["attempted_providers"] = []string{"searxng"}
			return marshal(results)
		})
	item.ExternallyCommunicating = true
	item.Check = func(context.Context) error {
		if config.TavilyAPIKey != "" {
			if err := validateSearchEndpoint(
				config.TavilySearchEndpoint, "ION_TAVILY_SEARCH_ENDPOINT",
			); err != nil {
				return err
			}
			return nil
		}
		if config.SearXNGSearchEndpoint == "" {
			return fmt.Errorf(
				"set TAVILY_API_KEY or ION_SEARCH_ENDPOINT to enable web search",
			)
		}
		return validateSearchEndpoint(
			config.SearXNGSearchEndpoint, "ION_SEARCH_ENDPOINT",
		)
	}
	return item
}

func validateSearchEndpoint(endpoint string, setting string) error {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		strings.TrimSpace(parsed.Hostname()) == "" {
		return fmt.Errorf("%s must be a valid HTTP or HTTPS URL", setting)
	}
	return nil
}

func searchTavily(
	ctx context.Context,
	client *http.Client,
	endpoint string,
	apiKey string,
	searchQuery string,
	category string,
	limit int,
) (map[string]any, error) {
	if err := validatePublicURL(ctx, endpoint); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(map[string]any{
		"query": searchQuery, "topic": category, "search_depth": "basic",
		"max_results": limit, "include_answer": "basic",
		"include_raw_content": false, "include_images": false,
	})
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(
		ctx, http.MethodPost, endpoint, bytes.NewReader(payload),
	)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+apiKey)
	request.Header.Set("User-Agent", "Ion-Agent/1.0")
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxReadBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxReadBytes {
		return nil, fmt.Errorf("search response exceeds bounded size")
	}
	if response.StatusCode < http.StatusOK ||
		response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf(
			"search endpoint returned HTTP %d", response.StatusCode,
		)
	}
	results, err := normalizeTavilyResults(body, searchQuery, limit)
	if err != nil {
		return nil, err
	}
	results["status"] = response.StatusCode
	results["category"] = category
	return results, nil
}

func searchSearXNG(
	ctx context.Context,
	client *http.Client,
	endpoint string,
	searchQuery string,
	category string,
	explicitCategory string,
	limit int,
) (map[string]any, error) {
	results, err := searchSearXNGCategory(
		ctx, client, endpoint, searchQuery, category, limit,
	)
	if err != nil {
		return nil, err
	}
	results["attempted_categories"] = []string{category}
	if fallbackCategory, ok := searchFallbackCategory(
		category, explicitCategory, searchResultCount(results),
	); ok {
		fallback, fallbackErr := searchSearXNGCategory(
			ctx, client, endpoint, searchQuery, fallbackCategory, limit,
		)
		results["attempted_categories"] = []string{
			category, fallbackCategory,
		}
		if fallbackErr == nil && searchResultCount(fallback) > 0 {
			fallback["attempted_categories"] = []string{
				category, fallbackCategory,
			}
			fallback["fallback_from"] = category
			results = fallback
		} else if fallbackErr != nil {
			results["fallback_state"] = "unavailable"
		} else {
			results["fallback_state"] = fallback["state"]
		}
	}
	return results, nil
}

func searchSearXNGCategory(
	ctx context.Context,
	client *http.Client,
	endpoint string,
	searchQuery string,
	category string,
	limit int,
) (map[string]any, error) {
	target, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	query := target.Query()
	query.Set("q", searchQuery)
	query.Set("format", "json")
	query.Set("categories", category)
	target.RawQuery = query.Encode()
	if err := validatePublicURL(ctx, target.String()); err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(
		ctx, http.MethodGet, target.String(), nil,
	)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "Ion-Agent/1.0")
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxReadBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxReadBytes {
		return nil, fmt.Errorf("search response exceeds bounded size")
	}
	if response.StatusCode < http.StatusOK ||
		response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf(
			"search endpoint returned HTTP %d", response.StatusCode,
		)
	}
	results, err := normalizeSearXNGResults(body, searchQuery, limit)
	if err != nil {
		return nil, err
	}
	results["status"] = response.StatusCode
	results["category"] = category
	return results, nil
}

func searchResultCount(results map[string]any) int {
	count, _ := results["result_count"].(int)
	return count
}

func searchFallbackCategory(
	category string,
	explicitCategory string,
	resultCount int,
) (string, bool) {
	if strings.TrimSpace(explicitCategory) != "" ||
		category != "general" ||
		resultCount != 0 {
		return "", false
	}
	return "news", true
}

func searchCategory(query, explicit string) string {
	explicit = strings.ToLower(strings.TrimSpace(explicit))
	if explicit == "general" || explicit == "news" {
		return explicit
	}
	query = strings.ToLower(strings.TrimSpace(query))
	for _, marker := range []string{
		"news", "latest", "today", "recent", "breaking", "this week",
	} {
		if strings.Contains(query, marker) {
			return "news"
		}
	}
	return "general"
}

func skillTools(store *skills.Store) []tools.Registration {
	return []tools.Registration{
		registration("skill_list", "List installed procedural skills.",
			`{"type":"object","additionalProperties":false}`, tools.ClassificationGreen,
			func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
				found, err := store.List(ctx)
				if err != nil {
					return nil, err
				}
				return marshal(found)
			}),
		registration("skill_view", "Load one installed procedural skill.",
			`{"type":"object","required":["name"],"properties":{"name":{"type":"string","minLength":1}},"additionalProperties":false}`,
			tools.ClassificationGreen, func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
				var input struct {
					Name string `json:"name"`
				}
				if err := decode(raw, &input); err != nil {
					return nil, err
				}
				found, err := store.Load(ctx, input.Name)
				if err != nil {
					return nil, err
				}
				return marshal(found)
			}),
		registration("skill_match", "Find the installed skill most relevant to a problem.",
			`{"type":"object","required":["problem"],"properties":{"problem":{"type":"string","minLength":1}},"additionalProperties":false}`,
			tools.ClassificationGreen, func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
				var input struct {
					Problem string `json:"problem"`
				}
				if err := decode(raw, &input); err != nil {
					return nil, err
				}
				found, err := store.Match(ctx, input.Problem)
				if err != nil {
					return nil, err
				}
				return marshal(found)
			}),
		registration("skill_save", "Save a proven procedural skill for later use.",
			`{"type":"object","required":["name","trigger","steps","pitfalls","verification"],"properties":{"name":{"type":"string"},"trigger":{"type":"string"},"steps":{"type":"array","items":{"type":"string"}},"pitfalls":{"type":"array","items":{"type":"string"}},"verification":{"type":"array","items":{"type":"string"}},"body":{"type":"string"}},"additionalProperties":false}`,
			tools.ClassificationYellow, func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
				var skill skills.Skill
				if err := decode(raw, &skill); err != nil {
					return nil, err
				}
				path, err := store.Save(ctx, skill)
				if err != nil {
					return nil, err
				}
				return marshal(map[string]any{"saved": true, "bundle": filepath.Base(filepath.Dir(path))})
			}),
		registration("skill_refine", "Stage a bounded skill revision supported by concrete outcome evidence; the active skill is unchanged until validation.",
			`{"type":"object","required":["name","evidence"],"properties":{"name":{"type":"string","minLength":1},"steps":{"type":"array","maxItems":8,"items":{"type":"string"}},"pitfalls":{"type":"array","maxItems":8,"items":{"type":"string"}},"verification":{"type":"array","maxItems":8,"items":{"type":"string"}},"body_note":{"type":"string","maxLength":4000},"evidence":{"type":"array","minItems":1,"maxItems":20,"items":{"type":"object","required":["episode_id","outcome","summary"],"properties":{"episode_id":{"type":"string","minLength":1},"outcome":{"type":"string","minLength":1},"summary":{"type":"string","minLength":1,"maxLength":1000},"verifier":{"type":"string"}},"additionalProperties":false}}},"additionalProperties":false}`,
			tools.ClassificationYellow, func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
				var input struct {
					Name         string            `json:"name"`
					Steps        []string          `json:"steps"`
					Pitfalls     []string          `json:"pitfalls"`
					Verification []string          `json:"verification"`
					BodyNote     string            `json:"body_note"`
					Evidence     []skills.Evidence `json:"evidence"`
				}
				if err := decode(raw, &input); err != nil {
					return nil, err
				}
				candidate, err := store.Propose(ctx, input.Name, skills.Refinement{
					Steps: input.Steps, Pitfalls: input.Pitfalls,
					Verification: input.Verification, BodyNote: input.BodyNote,
				}, input.Evidence)
				if err != nil {
					return nil, err
				}
				return marshal(candidate)
			}),
		registration("skill_candidate_list", "List staged, adopted, and rejected revisions for one skill.",
			`{"type":"object","required":["name"],"properties":{"name":{"type":"string","minLength":1}},"additionalProperties":false}`,
			tools.ClassificationGreen, func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
				var input struct {
					Name string `json:"name"`
				}
				if err := decode(raw, &input); err != nil {
					return nil, err
				}
				found, err := store.Candidates(ctx, input.Name)
				if err != nil {
					return nil, err
				}
				return marshal(found)
			}),
		registration("skill_candidate_evaluate", "Apply independently verified held-out scores; only a safe material improvement is promoted.",
			`{"type":"object","required":["name","candidate_id","baseline_score","candidate_score","validation_cases","safety_passed","validation_run_ids"],"properties":{"name":{"type":"string","minLength":1},"candidate_id":{"type":"string","minLength":1},"baseline_score":{"type":"number","minimum":0,"maximum":1},"candidate_score":{"type":"number","minimum":0,"maximum":1},"validation_cases":{"type":"integer","minimum":3},"safety_passed":{"type":"boolean"},"validation_run_ids":{"type":"array","minItems":3,"items":{"type":"string","minLength":1}},"notes":{"type":"array","items":{"type":"string"}}},"additionalProperties":false}`,
			tools.ClassificationRed, func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
				var input struct {
					Name             string   `json:"name"`
					CandidateID      string   `json:"candidate_id"`
					BaselineScore    float64  `json:"baseline_score"`
					CandidateScore   float64  `json:"candidate_score"`
					ValidationCases  int      `json:"validation_cases"`
					SafetyPassed     bool     `json:"safety_passed"`
					ValidationRunIDs []string `json:"validation_run_ids"`
					Notes            []string `json:"notes"`
				}
				if err := decode(raw, &input); err != nil {
					return nil, err
				}
				decision, err := store.Evaluate(ctx, input.Name, input.CandidateID, skills.Evaluation{
					BaselineScore: input.BaselineScore, CandidateScore: input.CandidateScore,
					ValidationCases: input.ValidationCases, SafetyPassed: input.SafetyPassed,
					ValidationRunIDs: input.ValidationRunIDs, Notes: input.Notes,
				})
				if err != nil {
					return nil, err
				}
				return marshal(decision)
			}),
	}
}

type memoryDocument struct {
	Content string   `json:"content"`
	Pinned  bool     `json:"pinned,omitempty"`
	Tags    []string `json:"tags,omitempty"`
	Kind    string   `json:"kind,omitempty"`
	Status  string   `json:"status,omitempty"`
}

func memoryTools(store memoryStore) []tools.Registration {
	registrations := []tools.Registration{
		registration("memory_search", "Search encrypted durable memories by text, or list them when no query is supplied.",
			`{"type":"object","properties":{"query":{"type":"string","minLength":1,"maxLength":4096},"limit":{"type":"integer","minimum":1,"maximum":100}},"additionalProperties":false}`,
			tools.ClassificationGreen, func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
				var input struct {
					Query string `json:"query"`
					Limit int    `json:"limit"`
				}
				if err := decode(raw, &input); err != nil {
					return nil, err
				}
				if input.Limit == 0 {
					input.Limit = 20
				}
				query := strings.ToLower(strings.TrimSpace(input.Query))
				type result struct {
					ID      uuid.UUID   `json:"id"`
					Type    memory.Type `json:"type"`
					Content string      `json:"content"`
					Pinned  bool        `json:"pinned"`
				}
				matches := make([]result, 0)
				for _, memoryType := range memory.Types() {
					for _, id := range actorMemoryIDs(ctx, store, memoryType) {
						if err := ctx.Err(); err != nil {
							return nil, err
						}
						found, err := resolveActorMemory(ctx, store, id)
						if err != nil {
							continue
						}
						document := decodeMemoryDocument(found.Version.Data)
						if query == "" ||
							strings.Contains(strings.ToLower(document.Content), query) {
							matches = append(matches, result{
								ID: id, Type: found.Head.Type,
								Content: truncate(document.Content, 4096),
								Pinned:  document.Pinned,
							})
							if len(matches) >= input.Limit {
								return marshal(map[string]any{
									"memories": matches, "truncated": true,
								})
							}
						}
					}
				}
				return marshal(map[string]any{"memories": matches, "truncated": false})
			}),
		registration("memory_recall", "Recall one encrypted durable memory by ID.",
			`{"type":"object","required":["id"],"properties":{"id":{"type":"string","format":"uuid"}},"additionalProperties":false}`,
			tools.ClassificationGreen, func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
				var input struct {
					ID string `json:"id"`
				}
				if err := decode(raw, &input); err != nil {
					return nil, err
				}
				id, err := uuid.Parse(input.ID)
				if err != nil {
					return nil, fmt.Errorf("memory_recall: invalid ID")
				}
				found, err := resolveActorMemory(ctx, store, id)
				if err != nil {
					return nil, err
				}
				return marshal(map[string]any{
					"id": found.Head.ID, "type": found.Head.Type,
					"memory":     decodeMemoryDocument(found.Version.Data),
					"updated_at": found.Head.LastUpdatedAt,
				})
			}),
		registration("memory_save", "Save a durable encrypted memory explicitly.",
			`{"type":"object","required":["type","content"],"properties":{"type":{"type":"string","description":"Memory type: 0x01 Identity, 0x02 Fact, 0x03 Preference, 0x04 Belief, 0x05 Event, 0x06 Goal, 0x07 Constraint, 0x08 Capability, 0x09 Pattern.","enum":["0x01","0x02","0x03","0x04","0x05","0x06","0x07","0x08","0x09"]},"content":{"type":"string","minLength":1,"maxLength":1048576},"pinned":{"type":"boolean"},"tags":{"type":"array","maxItems":32,"items":{"type":"string"}}},"additionalProperties":false}`,
			tools.ClassificationYellow, func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
				var input struct {
					Type    memory.Type `json:"type"`
					Content string      `json:"content"`
					Pinned  bool        `json:"pinned"`
					Tags    []string    `json:"tags"`
				}
				if err := decode(raw, &input); err != nil {
					return nil, err
				}
				if err := input.Type.Validate(); err != nil {
					return nil, err
				}
				payload, err := json.Marshal(memoryDocument{
					Content: input.Content, Pinned: input.Pinned, Tags: input.Tags,
				})
				if err != nil {
					return nil, err
				}
				saved, err := writeActorMemory(
					ctx, store, input.Type, payload, "operator-agent",
				)
				if err != nil {
					return nil, err
				}
				return marshal(map[string]any{
					"id": saved.Head.ID, "type": saved.Head.Type,
					"pinned": input.Pinned,
				})
			}),
		registration("memory_pin", "Pin or unpin one durable memory.",
			`{"type":"object","required":["id","pinned"],"properties":{"id":{"type":"string","format":"uuid"},"pinned":{"type":"boolean"}},"additionalProperties":false}`,
			tools.ClassificationYellow, func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
				var input struct {
					ID     string `json:"id"`
					Pinned bool   `json:"pinned"`
				}
				if err := decode(raw, &input); err != nil {
					return nil, err
				}
				id, err := uuid.Parse(input.ID)
				if err != nil {
					return nil, fmt.Errorf("memory_pin: invalid ID")
				}
				found, err := resolveActorMemory(ctx, store, id)
				if err != nil {
					return nil, err
				}
				document := decodeMemoryDocument(found.Version.Data)
				document.Pinned = input.Pinned
				payload, err := json.Marshal(document)
				if err != nil {
					return nil, err
				}
				if _, err := updateActorMemory(
					ctx, store, id, payload, "operator-agent",
				); err != nil {
					return nil, err
				}
				return marshal(map[string]any{"id": id, "pinned": input.Pinned})
			}),
		registration("memory_recover", "Recover one archived memory as a new durable live memory.",
			`{"type":"object","required":["id"],"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":0}},"additionalProperties":false}`,
			tools.ClassificationYellow, func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
				var input struct {
					ID      string `json:"id"`
					Version uint64 `json:"version"`
				}
				if err := decode(raw, &input); err != nil {
					return nil, err
				}
				id, err := uuid.Parse(input.ID)
				if err != nil {
					return nil, fmt.Errorf("memory_recover: invalid ID")
				}
				archived, err := resolveArchivedActorMemory(
					ctx, store, id, input.Version,
				)
				if err != nil {
					return nil, err
				}
				if archived.Head.Tombstoned == nil {
					return nil, fmt.Errorf(
						"memory_recover: source memory is not archived",
					)
				}
				recovered, err := writeActorMemory(
					ctx, store, archived.Head.Type, archived.Version.Data,
					"operator-recovery",
				)
				if err != nil {
					return nil, err
				}
				return marshal(map[string]any{
					"source_id": id, "source_version": archived.Version.Version,
					"recovered_id": recovered.Head.ID,
					"type":         recovered.Head.Type,
				})
			}),
	}
	return append(registrations, todoTools(store)...)
}

func decodeMemoryDocument(payload []byte) memoryDocument {
	var document memoryDocument
	if err := json.Unmarshal(payload, &document); err == nil &&
		strings.TrimSpace(document.Content) != "" {
		return document
	}
	return memoryDocument{Content: string(payload)}
}

func todoTools(store memoryStore) []tools.Registration {
	return []tools.Registration{
		registration("todo_list", "List the durable task graph todo projection.",
			`{"type":"object","properties":{"status":{"type":"string","enum":["pending","in_progress","completed"]}},"additionalProperties":false}`,
			tools.ClassificationGreen, func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
				var input struct {
					Status string `json:"status"`
				}
				if err := decode(raw, &input); err != nil {
					return nil, err
				}
				type todo struct {
					ID      uuid.UUID `json:"id"`
					Content string    `json:"content"`
					Status  string    `json:"status"`
					Updated time.Time `json:"updated_at"`
				}
				var result []todo
				for _, id := range actorMemoryIDs(ctx, store, memory.Goal) {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					found, err := resolveActorMemory(ctx, store, id)
					if err != nil {
						continue
					}
					document := decodeMemoryDocument(found.Version.Data)
					if document.Kind != "todo" ||
						(input.Status != "" && document.Status != input.Status) {
						continue
					}
					result = append(result, todo{
						ID: id, Content: document.Content, Status: document.Status,
						Updated: found.Head.LastUpdatedAt,
					})
				}
				return marshal(result)
			}),
		registration("todo_add", "Add one durable task graph todo.",
			`{"type":"object","required":["content"],"properties":{"content":{"type":"string","minLength":1,"maxLength":16384}},"additionalProperties":false}`,
			tools.ClassificationYellow, func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
				var input struct {
					Content string `json:"content"`
				}
				if err := decode(raw, &input); err != nil {
					return nil, err
				}
				payload, err := json.Marshal(memoryDocument{
					Kind: "todo", Content: input.Content, Status: "pending",
				})
				if err != nil {
					return nil, err
				}
				saved, err := writeActorMemory(
					ctx, store, memory.Goal, payload, "operator-agent",
				)
				if err != nil {
					return nil, err
				}
				return marshal(map[string]any{
					"id": saved.Head.ID, "content": input.Content, "status": "pending",
				})
			}),
		registration("todo_update", "Update one durable task graph todo.",
			`{"type":"object","required":["id","status"],"properties":{"id":{"type":"string","format":"uuid"},"status":{"type":"string","enum":["pending","in_progress","completed"]}},"additionalProperties":false}`,
			tools.ClassificationYellow, func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
				var input struct {
					ID     string `json:"id"`
					Status string `json:"status"`
				}
				if err := decode(raw, &input); err != nil {
					return nil, err
				}
				id, err := uuid.Parse(input.ID)
				if err != nil {
					return nil, fmt.Errorf("todo_update: invalid ID")
				}
				found, err := resolveActorMemory(ctx, store, id)
				if err != nil {
					return nil, err
				}
				document := decodeMemoryDocument(found.Version.Data)
				if found.Head.Type != memory.Goal || document.Kind != "todo" {
					return nil, fmt.Errorf("todo_update: memory is not a todo")
				}
				document.Status = input.Status
				payload, err := json.Marshal(document)
				if err != nil {
					return nil, err
				}
				if _, err := updateActorMemory(
					ctx, store, id, payload, "operator-agent",
				); err != nil {
					return nil, err
				}
				return marshal(map[string]any{"id": id, "status": input.Status})
			}),
	}
}

const pathSchema = `{"type":"object","required":["path"],"properties":{"path":{"type":"string"}},"additionalProperties":false}`

var errSearchLimit = errors.New("search result limit reached")

func registration(
	name, description, schema string,
	classification tools.Classification,
	handler tools.Handler,
) tools.Registration {
	return tools.Registration{
		Name: name, Description: description, Parameters: json.RawMessage(schema),
		Timeout: 30 * time.Second, Classification: classification,
		Check: func(context.Context) error { return nil }, Handler: handler,
	}
}

func unavailableRegistration(
	name, description string,
	classification tools.Classification,
	reason string,
	externallyCommunicating bool,
) tools.Registration {
	item := registration(
		name, description,
		`{"type":"object","additionalProperties":true}`,
		classification,
		func(context.Context, json.RawMessage) (json.RawMessage, error) {
			return nil, errors.New(reason)
		},
	)
	item.Check = func(context.Context) error { return errors.New(reason) }
	item.ExternallyCommunicating = externallyCommunicating
	return item
}

func secureRoot(root string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", fmt.Errorf("builtin tools: workspace root is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("builtin tools: resolve workspace: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("builtin tools: workspace must be a directory")
	}
	return filepath.Clean(resolved), nil
}

func resolveExisting(root, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" || requested == "." {
		return root, nil
	}
	var candidate string
	if filepath.IsAbs(requested) {
		candidate = filepath.Clean(requested)
		if !within(root, candidate) {
			return "", fmt.Errorf("workspace path is outside the registered root; use a contained path")
		}
	} else {
		candidate = filepath.Join(root, filepath.Clean(requested))
	}
	if !within(root, candidate) {
		return "", fmt.Errorf("workspace path escapes root")
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", err
	}
	if !within(root, resolved) {
		return "", fmt.Errorf("workspace symlink escapes root")
	}
	return resolved, nil
}

func resolveWritable(root, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return "", fmt.Errorf("workspace write path is required")
	}
	var candidate string
	if filepath.IsAbs(requested) {
		candidate = filepath.Clean(requested)
	} else {
		candidate = filepath.Join(root, filepath.Clean(requested))
	}
	if !within(root, candidate) {
		return "", fmt.Errorf("workspace write path is outside the registered root")
	}
	if _, err := os.Lstat(candidate); err == nil {
		return resolveExisting(root, requested)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	parent, err := ensureWorkspaceDirectory(root, filepath.Dir(candidate))
	if err != nil {
		return "", err
	}
	if !within(root, parent) {
		return "", fmt.Errorf("workspace parent symlink escapes root")
	}
	return filepath.Join(parent, filepath.Base(candidate)), nil
}

func ensureWorkspaceDirectory(root, requested string) (string, error) {
	requested = filepath.Clean(requested)
	if !within(root, requested) {
		return "", fmt.Errorf("workspace directory escapes root")
	}
	relativePath, err := filepath.Rel(root, requested)
	if err != nil {
		return "", err
	}
	current := root
	if relativePath == "." {
		return current, nil
	}
	for _, component := range strings.Split(relativePath, string(filepath.Separator)) {
		if component == "" || component == "." || component == ".." {
			return "", fmt.Errorf("workspace directory contains an invalid component")
		}
		next := filepath.Join(current, component)
		info, statErr := os.Lstat(next)
		if os.IsNotExist(statErr) {
			if mkdirErr := os.Mkdir(next, 0o700); mkdirErr != nil && !os.IsExist(mkdirErr) {
				return "", fmt.Errorf("filesystem_write: create parent %q: %w", component, mkdirErr)
			}
			info, statErr = os.Lstat(next)
		}
		if statErr != nil {
			return "", statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			resolved, resolveErr := filepath.EvalSymlinks(next)
			if resolveErr != nil || !within(root, resolved) {
				return "", fmt.Errorf("workspace parent symlink escapes root")
			}
			info, statErr = os.Stat(resolved)
			if statErr != nil || !info.IsDir() {
				return "", fmt.Errorf("workspace parent is not a directory")
			}
			current = resolved
			continue
		}
		if !info.IsDir() {
			return "", fmt.Errorf("workspace parent is not a directory")
		}
		current = next
	}
	resolved, err := filepath.EvalSymlinks(current)
	if err != nil || !within(root, resolved) {
		return "", fmt.Errorf("workspace parent symlink escapes root")
	}
	return resolved, nil
}

func within(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func relative(root, path string) string {
	value, err := filepath.Rel(root, path)
	if err != nil {
		return "."
	}
	return filepath.ToSlash(value)
}

func writeAtomic(path string, payload []byte, mode os.FileMode) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".ion-write-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(payload); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func decode(raw json.RawMessage, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	return nil
}

func marshal(value any) (json.RawMessage, error) {
	payload, err := json.Marshal(value)
	return json.RawMessage(payload), err
}

func safeEnvironment() []string {
	allowed := map[string]bool{
		"HOME": true, "LANG": true, "LC_ALL": true, "PATH": true,
		"TERM": true, "TMPDIR": true, "GOCACHE": true, "GOMODCACHE": true,
	}
	result := make([]string, 0, len(allowed))
	for _, item := range os.Environ() {
		name, _, _ := strings.Cut(item, "=")
		if allowed[name] {
			result = append(result, item)
		}
	}
	sort.Strings(result)
	return result
}

type boundedBuffer struct {
	mu        sync.Mutex
	data      []byte
	truncated bool
}

func (buffer *boundedBuffer) Write(payload []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	remaining := maxOutputBytes - len(buffer.data)
	if remaining > 0 {
		take := len(payload)
		if take > remaining {
			take = remaining
		}
		buffer.data = append(buffer.data, payload[:take]...)
	}
	if len(payload) > remaining {
		buffer.truncated = true
	}
	return len(payload), nil
}

func (buffer *boundedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return string(buffer.data)
}

func secureHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			for _, address := range addresses {
				if !publicIP(address.IP) {
					return nil, fmt.Errorf("web request blocked a non-public destination")
				}
			}
			if len(addresses) == 0 {
				return nil, fmt.Errorf("web request destination did not resolve")
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(addresses[0].IP.String(), port))
		},
		TLSHandshakeTimeout: 10 * time.Second,
		MaxIdleConns:        10, IdleConnTimeout: 30 * time.Second,
	}
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second}
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("web request exceeded redirect limit")
		}
		return validatePublicURL(request.Context(), request.URL.String())
	}
	return client
}

func validatePublicURL(ctx context.Context, raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() == "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.User != nil {
		return fmt.Errorf("web request requires a public HTTP or HTTPS URL without credentials")
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, parsed.Hostname())
	if err != nil {
		return fmt.Errorf("web request resolve destination: %w", err)
	}
	for _, address := range addresses {
		if !publicIP(address.IP) {
			return fmt.Errorf("web request blocked a non-public destination")
		}
	}
	return nil
}

func publicIP(ip net.IP) bool {
	return ip != nil && !ip.IsLoopback() && !ip.IsPrivate() &&
		!ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() &&
		!ip.IsUnspecified() && !ip.IsMulticast()
}

var (
	scriptPattern = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>|<style[^>]*>.*?</style>|<noscript[^>]*>.*?</noscript>`)
	tagPattern    = regexp.MustCompile(`(?s)<[^>]+>`)
	spacePattern  = regexp.MustCompile(`\s+`)
)

func extractText(value string) string {
	value = scriptPattern.ReplaceAllString(value, " ")
	value = tagPattern.ReplaceAllString(value, " ")
	return truncate(strings.TrimSpace(spacePattern.ReplaceAllString(value, " ")), maxReadBytes)
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}
