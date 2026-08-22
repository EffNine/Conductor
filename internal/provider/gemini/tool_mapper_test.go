package gemini

import (
	"encoding/json"
	"testing"

	"github.com/EffNine/conductor/internal/apitypes"
)

// --- normalizeGeminiSchema unit tests ---------------------------------------

func TestNormalizeGeminiSchema(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]interface{}
		expected map[string]interface{}
	}{
		{
			name:     "nil input",
			input:    nil,
			expected: nil,
		},
		{
			name:     "empty input",
			input:    map[string]interface{}{},
			expected: map[string]interface{}{},
		},
		// 1. object
		{
			name: "object type",
			input: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"city": map[string]interface{}{"type": "string"},
				},
			},
			expected: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"city": map[string]interface{}{"type": "string"},
				},
			},
		},
		// 2. string
		{
			name: "string type",
			input: map[string]interface{}{
				"type": "string",
			},
			expected: map[string]interface{}{
				"type": "string",
			},
		},
		// 3. number
		{
			name: "number type",
			input: map[string]interface{}{
				"type": "number",
			},
			expected: map[string]interface{}{
				"type": "number",
			},
		},
		// 4. integer
		{
			name: "integer type",
			input: map[string]interface{}{
				"type": "integer",
			},
			expected: map[string]interface{}{
				"type": "integer",
			},
		},
		// 5. boolean
		{
			name: "boolean type",
			input: map[string]interface{}{
				"type": "boolean",
			},
			expected: map[string]interface{}{
				"type": "boolean",
			},
		},
		// 6. array
		{
			name: "array type",
			input: map[string]interface{}{
				"type": "array",
				"items": map[string]interface{}{
					"type": "string",
				},
			},
			expected: map[string]interface{}{
				"type": "array",
				"items": map[string]interface{}{
					"type": "string",
				},
			},
		},
		// 7. nested object
		{
			name: "nested object",
			input: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"address": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"street": map[string]interface{}{"type": "string"},
							"zip":    map[string]interface{}{"type": "string"},
						},
					},
				},
			},
			expected: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"address": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"street": map[string]interface{}{"type": "string"},
							"zip":    map[string]interface{}{"type": "string"},
						},
					},
				},
			},
		},
		// 8. required
		{
			name: "required fields",
			input: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"city": map[string]interface{}{"type": "string"},
				},
				"required": []interface{}{"city"},
			},
			expected: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"city": map[string]interface{}{"type": "string"},
				},
				"required": []interface{}{"city"},
			},
		},
		// 9. enum
		{
			name: "enum",
			input: map[string]interface{}{
				"type": "string",
				"enum": []interface{}{"sunny", "cloudy", "rainy"},
			},
			expected: map[string]interface{}{
				"type": "string",
				"enum": []interface{}{"sunny", "cloudy", "rainy"},
			},
		},
		// 10. nullable / type union
		{
			name: "nullable type union string null",
			input: map[string]interface{}{
				"type": []interface{}{"string", "null"},
			},
			expected: map[string]interface{}{
				"type":     "string",
				"nullable": true,
			},
		},
		{
			name: "nullable type union null string",
			input: map[string]interface{}{
				"type": []interface{}{"null", "string"},
			},
			expected: map[string]interface{}{
				"type":     "string",
				"nullable": true,
			},
		},
		{
			name: "nullable type union preserves existing nullable",
			input: map[string]interface{}{
				"type":     []interface{}{"number", "null"},
				"nullable": true,
			},
			expected: map[string]interface{}{
				"type":     "number",
				"nullable": true,
			},
		},
		// 11. additionalProperties
		{
			name: "additionalProperties removed",
			input: map[string]interface{}{
				"type":                 "object",
				"properties":           map[string]interface{}{"city": map[string]interface{}{"type": "string"}},
				"additionalProperties": false,
			},
			expected: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"city": map[string]interface{}{"type": "string"},
				},
			},
		},
		{
			name: "additionalProperties true removed",
			input: map[string]interface{}{
				"type":                 "object",
				"additionalProperties": true,
			},
			expected: map[string]interface{}{
				"type": "object",
			},
		},
		// 12. anyOf
		{
			name: "anyOf nullable union",
			input: map[string]interface{}{
				"anyOf": []interface{}{
					map[string]interface{}{"type": "string"},
					map[string]interface{}{"type": "null"},
				},
			},
			expected: map[string]interface{}{
				"type":     "string",
				"nullable": true,
			},
		},
		{
			name: "anyOf single non-null option",
			input: map[string]interface{}{
				"anyOf": []interface{}{
					map[string]interface{}{"type": "string", "description": "a string"},
				},
			},
			expected: map[string]interface{}{
				"type":        "string",
				"description": "a string",
			},
		},
		{
			name: "anyOf mixed types falls back to first",
			input: map[string]interface{}{
				"anyOf": []interface{}{
					map[string]interface{}{"type": "string"},
					map[string]interface{}{"type": "number"},
				},
			},
			expected: map[string]interface{}{
				"type": "string",
			},
		},
		// 13. oneOf
		{
			name: "oneOf nullable union",
			input: map[string]interface{}{
				"oneOf": []interface{}{
					map[string]interface{}{"type": "integer"},
					map[string]interface{}{"type": "null"},
				},
			},
			expected: map[string]interface{}{
				"type":     "integer",
				"nullable": true,
			},
		},
		{
			name: "oneOf single option",
			input: map[string]interface{}{
				"oneOf": []interface{}{
					map[string]interface{}{"type": "boolean", "description": "flag"},
				},
			},
			expected: map[string]interface{}{
				"type":        "boolean",
				"description": "flag",
			},
		},
		// 14. minimum / maximum
		{
			name: "minimum and maximum preserved",
			input: map[string]interface{}{
				"type":    "integer",
				"minimum": float64(0),
				"maximum": float64(100),
			},
			expected: map[string]interface{}{
				"type":    "integer",
				"minimum": float64(0),
				"maximum": float64(100),
			},
		},
		// 15. exclusiveMinimum / exclusiveMaximum removed
		{
			name: "exclusiveMinimum removed",
			input: map[string]interface{}{
				"type":             "integer",
				"minimum":          float64(0),
				"exclusiveMinimum": float64(0),
			},
			expected: map[string]interface{}{
				"type":    "integer",
				"minimum": float64(0),
			},
		},
		{
			name: "exclusiveMaximum removed",
			input: map[string]interface{}{
				"type":             "integer",
				"maximum":          float64(100),
				"exclusiveMaximum": float64(100),
			},
			expected: map[string]interface{}{
				"type":    "integer",
				"maximum": float64(100),
			},
		},
		{
			name: "both exclusive constraints removed",
			input: map[string]interface{}{
				"type":             "number",
				"minimum":          float64(0),
				"maximum":          float64(10),
				"exclusiveMinimum": float64(0),
				"exclusiveMaximum": float64(10),
			},
			expected: map[string]interface{}{
				"type":    "number",
				"minimum": float64(0),
				"maximum": float64(10),
			},
		},
		// 16. nested combinations
		{
			name: "nested combinations",
			input: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"offset": map[string]interface{}{
						"type":             "integer",
						"description":      "Byte offset",
						"minimum":          float64(0),
						"exclusiveMinimum": float64(0),
					},
					"limit": map[string]interface{}{
						"type":             "integer",
						"description":      "Max bytes",
						"minimum":          float64(1),
						"maximum":          float64(10000),
						"exclusiveMaximum": float64(10000),
					},
					"encoding": map[string]interface{}{
						"type":        []interface{}{"string", "null"},
						"description": "File encoding",
					},
					"tags": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"key": map[string]interface{}{"type": "string"},
								"value": map[string]interface{}{
									"anyOf": []interface{}{
										map[string]interface{}{"type": "string"},
										map[string]interface{}{"type": "number"},
									},
								},
							},
							"required":             []interface{}{"key"},
							"additionalProperties": true,
						},
					},
				},
				"required":             []interface{}{"offset", "limit"},
				"additionalProperties": false,
			},
			expected: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"offset": map[string]interface{}{
						"type":        "integer",
						"description": "Byte offset",
						"minimum":     float64(0),
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"description": "Max bytes",
						"minimum":     float64(1),
						"maximum":     float64(10000),
					},
					"encoding": map[string]interface{}{
						"type":        "string",
						"description": "File encoding",
						"nullable":    true,
					},
					"tags": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"key":   map[string]interface{}{"type": "string"},
								"value": map[string]interface{}{"type": "string"},
							},
							"required": []interface{}{"key"},
						},
					},
				},
				"required": []interface{}{"offset", "limit"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeGeminiSchema(tt.input)
			if deepEqualMap(got, tt.expected) {
				return
			}
			gotB, _ := json.MarshalIndent(got, "", "  ")
			wantB, _ := json.MarshalIndent(tt.expected, "", "  ")
			t.Errorf("normalizeGeminiSchema() mismatch:\ngot:  %s\nwant: %s", gotB, wantB)
		})
	}
}

// --- openCode regression fixture --------------------------------------------

func TestOpenCodeRegressionFixture(t *testing.T) {
	tools := []apitypes.Tool{
		{
			Type: "function",
			Function: apitypes.FunctionDef{
				Name:        "read_file",
				Description: "Read a file from the filesystem",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path": map[string]interface{}{
							"type":        "string",
							"description": "Path to the file",
						},
						"offset": map[string]interface{}{
							"type":             "integer",
							"description":      "Byte offset to start reading",
							"minimum":          float64(0),
							"exclusiveMinimum": float64(0),
						},
						"limit": map[string]interface{}{
							"type":             "integer",
							"description":      "Max bytes to read",
							"minimum":          float64(1),
							"maximum":          float64(10000),
							"exclusiveMaximum": float64(10000),
						},
						"encoding": map[string]interface{}{
							"type":        []interface{}{"string", "null"},
							"description": "File encoding",
						},
					},
					"required":             []interface{}{"path"},
					"additionalProperties": false,
				},
			},
		},
		{
			Type: "function",
			Function: apitypes.FunctionDef{
				Name:        "search_code",
				Description: "Search codebase",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"query": map[string]interface{}{
							"type": "string",
						},
						"filters": map[string]interface{}{
							"type": "array",
							"items": map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"key": map[string]interface{}{"type": "string"},
									"value": map[string]interface{}{
										"anyOf": []interface{}{
											map[string]interface{}{"type": "string"},
											map[string]interface{}{"type": "number"},
										},
									},
								},
								"required":             []interface{}{"key"},
								"additionalProperties": true,
							},
						},
					},
					"required": []interface{}{"query"},
				},
			},
		},
	}

	for _, tool := range tools {
		decl := mapTool(tool)
		b, err := json.Marshal(decl)
		if err != nil {
			t.Fatalf("marshal tool %q: %v", tool.Function.Name, err)
		}

		// Verify no unsupported fields remain.
		var check map[string]interface{}
		if err := json.Unmarshal(b, &check); err != nil {
			t.Fatalf("re-unmarshal tool %q: %v", tool.Function.Name, err)
		}
		params, _ := check["parameters"].(map[string]interface{})
		if params == nil {
			t.Fatalf("tool %q: missing parameters", tool.Function.Name)
		}
		verifyNoUnsupportedFields(t, tool.Function.Name, params)

		// Verify name and description preserved.
		if check["name"] != tool.Function.Name {
			t.Errorf("tool %q: name = %v, want %q", tool.Function.Name, check["name"], tool.Function.Name)
		}
		if check["description"] != tool.Function.Description {
			t.Errorf("tool %q: description mismatch", tool.Function.Name)
		}

		// Verify required fields preserved where present.
		if req, ok := params["required"].([]interface{}); ok {
			if len(req) == 0 {
				t.Errorf("tool %q: expected required fields preserved", tool.Function.Name)
			}
		}

		t.Logf("Tool %q: %s", tool.Function.Name, string(b))
	}
}

func verifyNoUnsupportedFields(t *testing.T, toolName string, schema map[string]interface{}) {
	unsupported := map[string]bool{
		"exclusiveMinimum":     true,
		"exclusiveMaximum":     true,
		"additionalProperties": true,
		"anyOf":                true,
		"oneOf":                true,
		"allOf":                true,
		"$ref":                 true,
		"$defs":                true,
		"$schema":              true,
		"$id":                  true,
	}
	for k := range schema {
		if unsupported[k] {
			t.Errorf("tool %q: unsupported field %q in schema", toolName, k)
		}
	}
	// Check type is a string, not an array.
	if typ, ok := schema["type"]; ok {
		if _, isArray := typ.([]interface{}); isArray {
			t.Errorf("tool %q: type is array, must be string", toolName)
		}
	}
	// Recurse into properties.
	if props, ok := schema["properties"].(map[string]interface{}); ok {
		for propName, propSchema := range props {
			if m, ok := propSchema.(map[string]interface{}); ok {
				verifyNoUnsupportedFields(t, toolName+"."+propName, m)
			}
		}
	}
	// Recurse into items.
	if items, ok := schema["items"].(map[string]interface{}); ok {
		verifyNoUnsupportedFields(t, toolName+".items", items)
	}
}

// --- allOf test -------------------------------------------------------------

func TestNormalizeGeminiAllOf(t *testing.T) {
	input := map[string]interface{}{
		"allOf": []interface{}{
			map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{"a": map[string]interface{}{"type": "string"}},
			},
			map[string]interface{}{
				"properties": map[string]interface{}{"b": map[string]interface{}{"type": "number"}},
			},
		},
	}
	got := normalizeGeminiSchema(input)
	// Should keep only the first allOf clause.
	expected := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"a": map[string]interface{}{"type": "string"},
		},
	}
	if !deepEqualMap(got, expected) {
		gotB, _ := json.MarshalIndent(got, "", "  ")
		wantB, _ := json.MarshalIndent(expected, "", "  ")
		t.Errorf("allOf normalization mismatch:\ngot:  %s\nwant: %s", gotB, wantB)
	}
}

// --- response schema normalization ------------------------------------------

func TestNormalizeGeminiResponseSchema(t *testing.T) {
	// Simulates what mapResponseFormat receives and verifies the schema
	// leaving that path is also normalized (regression for the Gemini API
	// 422 errors on exclusiveMinimum/additionalProperties in response schemas).
	input := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"age": map[string]interface{}{
				"type":             "integer",
				"description":      "User age",
				"minimum":          float64(0),
				"exclusiveMinimum": float64(0),
				"exclusiveMaximum": float64(150),
			},
			"tags": map[string]interface{}{
				"type": []interface{}{"array", "null"},
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"label": map[string]interface{}{"type": "string"},
						"value": map[string]interface{}{
							"anyOf": []interface{}{
								map[string]interface{}{"type": "string"},
								map[string]interface{}{"type": "number"},
							},
						},
					},
					"required":             []interface{}{"label"},
					"additionalProperties": true,
				},
			},
			"notes": map[string]interface{}{
				"type":     []interface{}{"string", "null"},
				"nullable": false,
			},
		},
		"additionalProperties": false,
		"required":             []interface{}{"age"},
		"$schema":              "http://json-schema.org/draft-07/schema#",
	}

	got := normalizeGeminiSchema(input)

	// Verify unsupported fields are gone.
	for _, k := range []string{"exclusiveMinimum", "exclusiveMaximum", "additionalProperties", "anyOf", "oneOf", "allOf", "$schema"} {
		if _, ok := got[k]; ok {
			t.Errorf("top-level: unsupported field %q still present", k)
		}
	}

	// Verify type is a string, not an array.
	if typ, ok := got["type"]; ok {
		if _, isArray := typ.([]interface{}); isArray {
			t.Error("top-level: type should be string, not array")
		}
	}

	// Verify nested properties are normalized.
	props, _ := got["properties"].(map[string]interface{})
	age, _ := props["age"].(map[string]interface{})
	if _, ok := age["exclusiveMinimum"]; ok {
		t.Error("age: exclusiveMinimum should be stripped")
	}
	if _, ok := age["exclusiveMaximum"]; ok {
		t.Error("age: exclusiveMaximum should be stripped")
	}

	tags, _ := props["tags"].(map[string]interface{})
	items, _ := tags["items"].(map[string]interface{})
	if _, ok := items["additionalProperties"]; ok {
		t.Error("items: additionalProperties should be stripped")
	}
	val, _ := items["properties"].(map[string]interface{})["value"].(map[string]interface{})
	if _, ok := val["anyOf"]; ok {
		t.Error("value: anyOf should be flattened")
	}

	// Verify nullable is preserved on notes.
	notes, _ := props["notes"].(map[string]interface{})
	if notes["nullable"] != true {
		t.Error("notes: nullable should be preserved")
	}
}

// --- helper -----------------------------------------------------------------

func deepEqualMap(a, b map[string]interface{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		w, ok := b[k]
		if !ok {
			return false
		}
		if !deepEqualValue(v, w) {
			return false
		}
	}
	return true
}

func deepEqualValue(a, b interface{}) bool {
	switch av := a.(type) {
	case map[string]interface{}:
		bv, ok := b.(map[string]interface{})
		if !ok {
			return false
		}
		return deepEqualMap(av, bv)
	case []interface{}:
		bv, ok := b.([]interface{})
		if !ok {
			return false
		}
		if len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !deepEqualValue(av[i], bv[i]) {
				return false
			}
		}
		return true
	default:
		return a == b
	}
}
