package provider

import (
	"reflect"
	"testing"
)

func TestStripSchemaMetaFields(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]interface{}
		expected map[string]interface{}
	}{
		{
			name:     "nil params",
			input:    nil,
			expected: nil,
		},
		{
			name:     "empty input",
			input:    map[string]interface{}{},
			expected: map[string]interface{}{},
		},
		{
			name: "strip $schema",
			input: map[string]interface{}{
				"$schema": "http://json-schema.org/draft-07/schema#",
				"type":    "object",
			},
			expected: map[string]interface{}{
				"type": "object",
			},
		},
		{
			name: "strip $ref and $defs",
			input: map[string]interface{}{
				"$ref": "#/$defs/Foo",
				"$defs": map[string]interface{}{
					"Foo": map[string]interface{}{"type": "string"},
				},
				"type": "object",
			},
			expected: map[string]interface{}{
				"type": "object",
			},
		},
		{
			name: "strip all meta-fields preserves properties",
			input: map[string]interface{}{
				"$schema":              "http://json-schema.org/draft-07/schema#",
				"type":                 "object",
				"properties":           map[string]interface{}{"city": map[string]interface{}{"type": "string"}},
				"additionalProperties": false,
				"required":             []interface{}{"city"},
			},
			expected: map[string]interface{}{
				"type":                 "object",
				"properties":           map[string]interface{}{"city": map[string]interface{}{"type": "string"}},
				"additionalProperties": false,
				"required":             []interface{}{"city"},
			},
		},
		{
			name: "recursively strip nested $schema",
			input: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"city": map[string]interface{}{
						"$schema": "http://json-schema.org/draft-07/schema#",
						"type":    "string",
					},
				},
			},
			expected: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"city": map[string]interface{}{
						"type": "string",
					},
				},
			},
		},
		{
			name: "strip in array items",
			input: map[string]interface{}{
				"type": "array",
				"items": map[string]interface{}{
					"$schema": "http://json-schema.org/draft-07/schema#",
					"type":    "string",
				},
			},
			expected: map[string]interface{}{
				"type": "array",
				"items": map[string]interface{}{
					"type": "string",
				},
			},
		},
		{
			name: "strip nested $ref in properties",
			input: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"address": map[string]interface{}{
						"$ref": "#/$defs/Address",
					},
				},
				"$defs": map[string]interface{}{
					"Address": map[string]interface{}{
						"type":       "object",
						"properties": map[string]interface{}{"street": map[string]interface{}{"type": "string"}},
						"$schema":    "http://json-schema.org/draft-07/schema#",
						"$id":        "https://example.com/address.json",
					},
				},
			},
			expected: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"address": map[string]interface{}{},
				},
			},
		},
		{
			name: "does not mutate original map",
			input: map[string]interface{}{
				"$schema": "http://json-schema.org/draft-07/schema#",
				"type":    "object",
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Snapshot original keys before call.
			originalKeys := make(map[string]bool, len(tt.input))
			if tt.input != nil {
				for k := range tt.input {
					originalKeys[k] = true
				}
			}

			got := StripSchemaMetaFields(tt.input)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("StripSchemaMetaFields() = %v, want %v", got, tt.expected)
			}

			// Verify the original map was not mutated: all original keys must
			// still be present in the input map after the call.
			if tt.input != nil {
				for k := range originalKeys {
					if _, ok := tt.input[k]; !ok {
						t.Errorf("original input was mutated: key %q missing", k)
					}
				}
			}
		})
	}
}

// --- NormalizeSchema tests --------------------------------------------------

func TestNormalizeSchema(t *testing.T) {
	tests := []struct {
		name     string
		norm     SchemaNormalization
		input    map[string]interface{}
		expected map[string]interface{}
	}{
		{
			name:     "nil input returns nil",
			norm:     NormalizeForGemini,
			input:    nil,
			expected: nil,
		},
		{
			name:     "NoNormalization passes through",
			norm:     NoNormalization,
			input:    map[string]interface{}{"type": "object", "exclusiveMinimum": float64(0)},
			expected: map[string]interface{}{"type": "object", "exclusiveMinimum": float64(0)},
		},
		{
			name: "strip meta-fields only",
			norm: StripMetaFields,
			input: map[string]interface{}{
				"$schema":              "http://json-schema.org/draft-07/schema#",
				"$ref":                 "#/$defs/Foo",
				"type":                 "object",
				"properties":           map[string]interface{}{"city": map[string]interface{}{"type": "string"}},
				"additionalProperties": false,
				"exclusiveMinimum":     float64(0),
			},
			expected: map[string]interface{}{
				"type":                 "object",
				"properties":           map[string]interface{}{"city": map[string]interface{}{"type": "string"}},
				"additionalProperties": false,
				"exclusiveMinimum":     float64(0),
			},
		},
		{
			name: "gemini: strip meta-fields and unsupported keywords",
			norm: NormalizeForGemini,
			input: map[string]interface{}{
				"$schema":              "http://json-schema.org/draft-07/schema#",
				"type":                 "object",
				"properties":           map[string]interface{}{"city": map[string]interface{}{"type": "string"}},
				"additionalProperties": false,
				"exclusiveMinimum":     float64(0),
				"exclusiveMaximum":     float64(100),
				"required":             []interface{}{"city"},
			},
			expected: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{"city": map[string]interface{}{"type": "string"}},
				"required":   []interface{}{"city"},
			},
		},
		{
			name: "gemini: type array becomes nullable",
			norm: NormalizeForGemini,
			input: map[string]interface{}{
				"type":     []interface{}{"string", "null"},
				"nullable": false,
			},
			expected: map[string]interface{}{
				"type":     "string",
				"nullable": true,
			},
		},
		{
			name: "gemini: anyOf nullable union",
			norm: NormalizeForGemini,
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
			name: "gemini: oneOf single option",
			norm: NormalizeForGemini,
			input: map[string]interface{}{
				"oneOf": []interface{}{
					map[string]interface{}{"type": "integer", "description": "age"},
				},
			},
			expected: map[string]interface{}{
				"type":        "integer",
				"description": "age",
			},
		},
		{
			name: "gemini: allOf takes first clause",
			norm: NormalizeForGemini,
			input: map[string]interface{}{
				"allOf": []interface{}{
					map[string]interface{}{"type": "object", "properties": map[string]interface{}{"a": map[string]interface{}{"type": "string"}}},
					map[string]interface{}{"properties": map[string]interface{}{"b": map[string]interface{}{"type": "number"}}},
				},
			},
			expected: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"a": map[string]interface{}{"type": "string"},
				},
			},
		},
		{
			name: "anthropic: same as gemini plus unknown fields stripped",
			norm: NormalizeForAnthropic,
			input: map[string]interface{}{
				"type":         "object",
				"unknownField": "should be removed",
				"properties": map[string]interface{}{
					"age": map[string]interface{}{
						"type":             "integer",
						"description":      "User age",
						"minimum":          float64(0),
						"exclusiveMinimum": float64(0),
						"customValidator":  true,
					},
				},
			},
			expected: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"age": map[string]interface{}{
						"type":        "integer",
						"description": "User age",
						"minimum":     float64(0),
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeSchema(tt.input, tt.norm)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("NormalizeSchema() mismatch:\ngot:  %v\nwant: %v", got, tt.expected)
			}
		})
	}
}
