//nolint:paralleltest // These tests exercise process-global cwd and environment config discovery.
package cli

// These tests intentionally do not call t.Parallel because they exercise config
// discovery through process-global cwd and environment variables.

func fakeMCPCreateIssueTool() map[string]any {
	return map[string]any{
		"name":        "create_issue",
		"description": "Create an issue",
		"inputSchema": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"title": map[string]any{
					"type":        "string",
					"description": "Issue title",
				},
				"draft": map[string]any{
					"type":        "boolean",
					"description": "Create as draft",
				},
				"labels": map[string]any{
					"type":        "array",
					"description": "Labels",
					"items":       map[string]any{"type": "string"},
				},
			},
		},
	}
}

func fakeMCPCalculateTool() map[string]any {
	return map[string]any{
		"name":        "calculate",
		"description": "Performs basic arithmetic operations",
		"inputSchema": map[string]any{
			"$schema":              "http://json-schema.org/draft-07/schema#",
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"operation", "a", "b"},
			"properties": map[string]any{
				"a": map[string]any{
					"type":        "number",
					"description": "First operand",
				},
				"b": map[string]any{
					"type":        "number",
					"description": "Second operand",
				},
				"operation": map[string]any{
					"type":        "string",
					"description": "Operation to perform",
					"enum":        []string{"add", "subtract", "multiply", "divide"},
				},
			},
		},
	}
}

func fakeMCPCloudSearchTool() map[string]any {
	return map[string]any{
		"name":        "search",
		"description": "Search cloud resources",
		"inputSchema": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"cloudId", "jql"},
			"properties": map[string]any{
				"cloudId": map[string]any{
					"type":        "string",
					"description": "Cloud site ID",
				},
				"jql": map[string]any{
					"type":        "string",
					"description": "Search query",
				},
			},
		},
	}
}

func fakeMCPCloudSearchRemoteTool() mcpRemoteTool {
	tool := fakeMCPCloudSearchTool()
	return mcpRemoteTool{
		Name:        tool["name"].(string),
		Description: tool["description"].(string),
		InputSchema: tool["inputSchema"].(map[string]any),
	}
}
