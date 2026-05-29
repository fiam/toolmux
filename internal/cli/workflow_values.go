package cli

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// parseWorkflowDuration accepts the Go time.ParseDuration grammar plus a `d`
// (days) suffix. It returns the duration in nanoseconds.
func parseWorkflowDuration(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("empty duration")
	}
	expanded := strings.Builder{}
	numStart := -1
	for i, r := range value {
		switch {
		case unicode.IsDigit(r) || r == '.' || r == '-' || r == '+':
			if numStart == -1 {
				numStart = i
			}
		case r == 'd' || r == 'D':
			if numStart == -1 {
				return 0, fmt.Errorf("invalid duration %q", value)
			}
			days, err := strconv.ParseFloat(value[numStart:i], 64)
			if err != nil {
				return 0, fmt.Errorf("invalid duration %q: %w", value, err)
			}
			expanded.WriteString(strconv.FormatFloat(days*24, 'f', -1, 64))
			expanded.WriteString("h")
			numStart = -1
		default:
			if numStart != -1 {
				expanded.WriteString(value[numStart:i])
				numStart = -1
			}
			expanded.WriteRune(r)
		}
	}
	if numStart != -1 {
		return 0, fmt.Errorf("invalid duration %q: trailing number without unit", value)
	}
	return time.ParseDuration(expanded.String())
}

// coerceWorkflowInput converts a raw string value into the typed input
// representation used by the template context. It returns the value to expose
// in the template context.
func coerceWorkflowInput(name string, input workflowInput, raw string) (any, error) {
	switch input.Type {
	case "", workflowFieldString:
		return raw, nil
	case workflowFieldInt:
		n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("input %s: expected int, got %q", name, raw)
		}
		return n, nil
	case workflowFieldBool:
		b, err := strconv.ParseBool(strings.TrimSpace(raw))
		if err != nil {
			return nil, fmt.Errorf("input %s: expected bool, got %q", name, raw)
		}
		return b, nil
	case workflowFieldDuration:
		d, err := parseWorkflowDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("input %s: %w", name, err)
		}
		return d.String(), nil
	case workflowFieldJSON:
		var decoded any
		if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
			return nil, fmt.Errorf("input %s: expected JSON, got %q: %w", name, raw, err)
		}
		if len(input.Schema) > 0 {
			if err := validateWorkflowValueAgainstSchema(decoded, input.Schema, "input "+name); err != nil {
				return nil, err
			}
		}
		return decoded, nil
	}
	return nil, fmt.Errorf("input %s: unsupported type %q", name, input.Type)
}

func validateWorkflowValueAgainstSchema(value any, schema map[string]any, label string) error {
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(label, schema); err != nil {
		return fmt.Errorf("%s: invalid json schema: %w", label, err)
	}
	compiled, err := compiler.Compile(label)
	if err != nil {
		return fmt.Errorf("%s: invalid json schema: %w", label, err)
	}
	if err := compiled.Validate(value); err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	return nil
}
