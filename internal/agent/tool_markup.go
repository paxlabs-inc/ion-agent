package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/paxlabs-inc/ion-agent/internal/tools"
	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
	"lukechampine.com/blake3"
)

const (
	toolCallOpen      = "<tool_call>"
	toolCallClose     = "</tool_call>"
	toolFunctionOpen  = "<function="
	toolParameterOpen = "<parameter="
)

// normalizeTextualToolCalls contains compatibility drift at the provider
// boundary. Some otherwise OpenAI-compatible models intermittently serialize
// a requested function call into their assistant content. A strict terminal
// block can be recovered into the normal policy-checked tool path; ambiguous
// or malformed internal syntax fails closed instead of reaching a user.
func normalizeTextualToolCalls(
	generation *protocol.NormalizedGeneration,
	tools []protocol.ToolDefinition,
) error {
	if generation == nil {
		return fmt.Errorf("%w: generation is required", ErrTextualToolMarkup)
	}
	if !containsToolMarkup(generation.Content) {
		return nil
	}
	if markupOnlyInMarkdownCode(generation.Content) {
		return nil
	}
	if len(generation.ToolCalls) > 0 {
		return fmt.Errorf(
			"%w: content accompanies structured calls", ErrTextualToolMarkup,
		)
	}
	if generation.FinishReason != protocol.FinishStop &&
		generation.FinishReason != protocol.FinishToolCalls {
		return fmt.Errorf(
			"%w: incomplete provider response", ErrTextualToolMarkup,
		)
	}

	prefix, calls, err := parseTerminalToolCalls(generation.Content, tools)
	if err != nil {
		return err
	}
	generation.Content = strings.TrimSpace(prefix)
	generation.ToolCalls = calls
	generation.FinishReason = protocol.FinishToolCalls
	return nil
}

func containsToolMarkup(content string) bool {
	return strings.Contains(content, toolCallOpen) ||
		strings.Contains(content, toolCallClose) ||
		strings.Contains(content, toolFunctionOpen) ||
		strings.Contains(content, toolParameterOpen)
}

func parseTerminalToolCalls(
	content string,
	tools []protocol.ToolDefinition,
) (string, []protocol.NormalizedToolCall, error) {
	first := strings.Index(content, toolCallOpen)
	if first < 0 {
		return "", nil, fmt.Errorf(
			"%w: opening tool-call tag is missing", ErrTextualToolMarkup,
		)
	}
	prefix := content[:first]
	if containsToolMarkup(prefix) {
		return "", nil, fmt.Errorf(
			"%w: internal markup appears before the call block",
			ErrTextualToolMarkup,
		)
	}
	definitions := make(map[string]protocol.ToolDefinition, len(tools))
	for _, definition := range tools {
		definitions[definition.Name] = definition
	}

	remainder := content[first:]
	var calls []protocol.NormalizedToolCall
	for strings.TrimSpace(remainder) != "" {
		remainder = strings.TrimLeftFunc(remainder, unicode.IsSpace)
		if !strings.HasPrefix(remainder, toolCallOpen) {
			return "", nil, fmt.Errorf(
				"%w: non-call content follows the call block",
				ErrTextualToolMarkup,
			)
		}
		closeAt := strings.Index(remainder, toolCallClose)
		if closeAt < 0 {
			return "", nil, fmt.Errorf(
				"%w: closing tool-call tag is missing", ErrTextualToolMarkup,
			)
		}
		rawBlock := remainder[:closeAt+len(toolCallClose)]
		body := remainder[len(toolCallOpen):closeAt]
		call, err := parseTextualToolCall(body, rawBlock, definitions)
		if err != nil {
			return "", nil, err
		}
		calls = append(calls, call)
		remainder = remainder[closeAt+len(toolCallClose):]
	}
	if len(calls) == 0 {
		return "", nil, fmt.Errorf(
			"%w: no calls were found", ErrTextualToolMarkup,
		)
	}
	return prefix, calls, nil
}

func parseTextualToolCall(
	body string,
	rawBlock string,
	definitions map[string]protocol.ToolDefinition,
) (protocol.NormalizedToolCall, error) {
	body = strings.TrimSpace(body)
	if !strings.HasPrefix(body, toolFunctionOpen) {
		return protocol.NormalizedToolCall{}, fmt.Errorf(
			"%w: function tag is missing", ErrTextualToolMarkup,
		)
	}
	functionEnd := strings.IndexByte(body, '>')
	if functionEnd < len(toolFunctionOpen) {
		return protocol.NormalizedToolCall{}, fmt.Errorf(
			"%w: function tag is malformed", ErrTextualToolMarkup,
		)
	}
	name := strings.TrimSpace(body[len(toolFunctionOpen):functionEnd])
	if !validMarkupName(name) {
		return protocol.NormalizedToolCall{}, fmt.Errorf(
			"%w: function name is invalid", ErrTextualToolMarkup,
		)
	}
	definition, available := definitions[name]
	if !available {
		return protocol.NormalizedToolCall{}, fmt.Errorf(
			"%w: function is not in the active tool surface",
			ErrTextualToolMarkup,
		)
	}

	rawParameters, err := parseTextualParameters(body[functionEnd+1:])
	if err != nil {
		return protocol.NormalizedToolCall{}, err
	}
	arguments, err := encodeTextualParameters(definition, rawParameters)
	if err != nil {
		return protocol.NormalizedToolCall{}, err
	}
	if err := tools.ValidateArguments(definition.Parameters, arguments); err != nil {
		return protocol.NormalizedToolCall{}, fmt.Errorf(
			"%w: arguments do not match the active tool schema",
			ErrTextualToolMarkup,
		)
	}
	digest := blake3.Sum256([]byte(rawBlock))
	call := protocol.NormalizedToolCall{
		ID:        fmt.Sprintf("textual-%x", digest[:12]),
		Name:      name,
		Arguments: arguments,
	}
	if err := call.Validate(); err != nil {
		return protocol.NormalizedToolCall{}, fmt.Errorf(
			"%w: normalized call is invalid", ErrTextualToolMarkup,
		)
	}
	return call, nil
}

func parseTextualParameters(body string) (map[string]string, error) {
	parameters := map[string]string{}
	remainder := strings.TrimSpace(body)
	for remainder != "" {
		if !strings.HasPrefix(remainder, toolParameterOpen) {
			return nil, fmt.Errorf(
				"%w: parameter tag is malformed", ErrTextualToolMarkup,
			)
		}
		tagEnd := strings.IndexByte(remainder, '>')
		if tagEnd < len(toolParameterOpen) {
			return nil, fmt.Errorf(
				"%w: parameter tag is malformed", ErrTextualToolMarkup,
			)
		}
		name := strings.TrimSpace(
			remainder[len(toolParameterOpen):tagEnd],
		)
		if !validMarkupName(name) {
			return nil, fmt.Errorf(
				"%w: parameter name is invalid", ErrTextualToolMarkup,
			)
		}
		if _, duplicate := parameters[name]; duplicate {
			return nil, fmt.Errorf(
				"%w: duplicate parameter", ErrTextualToolMarkup,
			)
		}
		valueAndRest := remainder[tagEnd+1:]
		next := strings.Index(valueAndRest, toolParameterOpen)
		if next < 0 {
			parameters[name] = strings.TrimSpace(valueAndRest)
			remainder = ""
			continue
		}
		parameters[name] = strings.TrimSpace(valueAndRest[:next])
		remainder = strings.TrimSpace(valueAndRest[next:])
	}
	return parameters, nil
}

func encodeTextualParameters(
	definition protocol.ToolDefinition,
	parameters map[string]string,
) (json.RawMessage, error) {
	var schema struct {
		Properties map[string]struct {
			Type string `json:"type"`
		} `json:"properties"`
		AdditionalProperties any `json:"additionalProperties"`
	}
	if err := json.Unmarshal(definition.Parameters, &schema); err != nil {
		return nil, fmt.Errorf(
			"%w: active tool schema is invalid", ErrTextualToolMarkup,
		)
	}
	converted := make(map[string]any, len(parameters))
	for name, raw := range parameters {
		property, known := schema.Properties[name]
		if !known {
			if allowed, isBool := schema.AdditionalProperties.(bool); isBool && !allowed {
				return nil, fmt.Errorf(
					"%w: parameter is not in the active schema",
					ErrTextualToolMarkup,
				)
			}
			converted[name] = raw
			continue
		}
		value, err := coerceTextualParameter(property.Type, raw)
		if err != nil {
			return nil, fmt.Errorf(
				"%w: parameter %q does not match its schema",
				ErrTextualToolMarkup, name,
			)
		}
		converted[name] = value
	}
	encoded, err := json.Marshal(converted)
	if err != nil || !bytes.HasPrefix(bytes.TrimSpace(encoded), []byte("{")) {
		return nil, fmt.Errorf(
			"%w: parameters could not be encoded", ErrTextualToolMarkup,
		)
	}
	return encoded, nil
}

func coerceTextualParameter(kind string, raw string) (any, error) {
	switch kind {
	case "", "string":
		return raw, nil
	case "integer":
		return strconv.ParseInt(raw, 10, 64)
	case "number":
		return strconv.ParseFloat(raw, 64)
	case "boolean":
		return strconv.ParseBool(raw)
	case "object", "array":
		var value any
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			return nil, err
		}
		if kind == "object" {
			if _, ok := value.(map[string]any); !ok {
				return nil, fmt.Errorf("not an object")
			}
		} else if _, ok := value.([]any); !ok {
			return nil, fmt.Errorf("not an array")
		}
		return value, nil
	default:
		return nil, fmt.Errorf("unsupported schema type")
	}
}

func validMarkupName(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, current := range value {
		if unicode.IsLetter(current) || unicode.IsDigit(current) ||
			current == '_' || current == '-' || current == '.' ||
			current == ':' {
			continue
		}
		return false
	}
	return true
}

func markupOnlyInMarkdownCode(content string) bool {
	var outside strings.Builder
	inFence := false
	inInline := false
	for index := 0; index < len(content); {
		if strings.HasPrefix(content[index:], "```") {
			inFence = !inFence
			index += 3
			continue
		}
		if !inFence && content[index] == '`' {
			inInline = !inInline
			index++
			continue
		}
		if !inFence && !inInline {
			outside.WriteByte(content[index])
		}
		index++
	}
	return !containsToolMarkup(outside.String())
}
