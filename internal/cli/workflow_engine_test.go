//nolint:paralleltest // shares process-global HOME.
package cli

import (
	"strings"
	"testing"
	"time"
)

func TestParseWorkflowDuration(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  time.Duration
	}{
		{"8h", 8 * time.Hour},
		{"30m", 30 * time.Minute},
		{"2d", 48 * time.Hour},
		{"1d12h", 36 * time.Hour},
		{"90s", 90 * time.Second},
	} {
		got, err := parseWorkflowDuration(tc.input)
		if err != nil {
			t.Fatalf("%q: %v", tc.input, err)
		}
		if got != tc.want {
			t.Fatalf("%q: got %s, want %s", tc.input, got, tc.want)
		}
	}
	if _, err := parseWorkflowDuration("not-a-duration"); err == nil {
		t.Fatal("expected error for bad duration")
	}
}

func TestCoerceWorkflowInputTypes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input workflowInput
		raw   string
		want  any
	}{
		{name: "string", input: workflowInput{}, raw: "hello", want: "hello"},
		{name: "int", input: workflowInput{Type: workflowFieldInt}, raw: "42", want: int64(42)},
		{name: "bool", input: workflowInput{Type: workflowFieldBool}, raw: "true", want: true},
		{name: "duration", input: workflowInput{Type: workflowFieldDuration}, raw: "8h", want: "8h0m0s"},
	} {
		got, err := coerceWorkflowInput(tc.name, tc.input, tc.raw)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got != tc.want {
			t.Fatalf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestCoerceWorkflowInputJSONWithSchema(t *testing.T) {
	schema := map[string]any{
		"type":     "object",
		"required": []any{"x"},
		"properties": map[string]any{
			"x": map[string]any{"type": "integer"},
		},
	}
	input := workflowInput{Type: workflowFieldJSON, Schema: schema}
	if _, err := coerceWorkflowInput("v", input, `{"x": 7}`); err != nil {
		t.Fatalf("valid payload rejected: %v", err)
	}
	if _, err := coerceWorkflowInput("v", input, `{"y": 1}`); err == nil {
		t.Fatal("expected schema validation failure")
	}
}

func TestValidateWorkflowRejectsReservedIdentifiers(t *testing.T) {
	err := validateWorkflow(workflowFile{
		Version: 1,
		Name:    "bad",
		Prompt:  "x",
		Inputs:  map[string]workflowInput{"previous": {}},
	})
	if err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("expected reserved-name error, got %v", err)
	}
	err = validateWorkflow(workflowFile{
		Version: 1,
		Name:    "bad",
		Steps: []workflowStep{
			{ID: "inputs", Prompt: "x"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("expected reserved step id error, got %v", err)
	}
}

func TestValidateWorkflowRejectsDuplicateStepIDs(t *testing.T) {
	err := validateWorkflow(workflowFile{
		Version: 1,
		Name:    "dup",
		Steps: []workflowStep{
			{ID: "fetch", Prompt: "a"},
			{ID: "fetch", Prompt: "b"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate step id") {
		t.Fatalf("expected duplicate step id error, got %v", err)
	}
}

func TestValidateWorkflowRejectsPromptAndStepsTogether(t *testing.T) {
	err := validateWorkflow(workflowFile{
		Version: 1,
		Name:    "both",
		Prompt:  "x",
		Steps:   []workflowStep{{Prompt: "y"}},
	})
	if err == nil || !strings.Contains(err.Error(), "either prompt or steps") {
		t.Fatalf("expected mutually-exclusive error, got %v", err)
	}
}

func TestLastJSONObjectLine(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  string
		ok    bool
	}{
		{input: "some prose\n{\"a\": 1}", want: `{"a": 1}`, ok: true},
		{input: "prose\n{\"a\":1}\n", want: `{"a":1}`, ok: true},
		{input: "no json here", ok: false},
		{input: "{\"a\": 1}\nthen prose", ok: false},
	} {
		got, ok := lastJSONObjectLine(tc.input)
		if ok != tc.ok || got != tc.want {
			t.Fatalf("%q: got (%q, %v), want (%q, %v)", tc.input, got, ok, tc.want, tc.ok)
		}
	}
}

func TestDecodeStepOutputsTypedAndSchemaValidated(t *testing.T) {
	step := workflowStep{
		ID: "fetch",
		Outputs: map[string]workflowOutput{
			"count": {Type: workflowFieldInt},
			"items": {
				Type: workflowFieldJSON,
				Schema: map[string]any{
					"type":  "array",
					"items": map[string]any{"type": "string"},
				},
			},
		},
	}
	good := `chatter chatter
{"count": 3, "items": ["a", "b"]}`
	outputs, _, err := decodeStepOutputs(step, good)
	if err != nil {
		t.Fatalf("good payload rejected: %v", err)
	}
	if outputs["count"].(int64) != 3 {
		t.Fatalf("count: got %v", outputs["count"])
	}
	bad := `{"count": 3, "items": [1, 2]}`
	if _, _, err := decodeStepOutputs(step, bad); err == nil {
		t.Fatal("expected schema validation failure")
	}
}
