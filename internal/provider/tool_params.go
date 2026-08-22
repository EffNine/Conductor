package provider

import "strings"

// StripSchemaMetaFields removes JSON Schema meta-fields that some providers
// reject as unknown properties. These include $schema, $id, $ref, $defs, and
// any other top-level keys starting with $.
func StripSchemaMetaFields(params map[string]interface{}) map[string]interface{} {
	if params == nil {
		return nil
	}
	out := make(map[string]interface{}, len(params))
	for k, v := range params {
		if strings.HasPrefix(k, "$") {
			continue
		}
		out[k] = v
	}
	// Recursively strip nested objects and arrays.
	for k, v := range out {
		switch val := v.(type) {
		case map[string]interface{}:
			out[k] = StripSchemaMetaFields(val)
		case []interface{}:
			stripped := make([]interface{}, 0, len(val))
			for _, item := range val {
				if m, ok := item.(map[string]interface{}); ok {
					stripped = append(stripped, StripSchemaMetaFields(m))
				} else {
					stripped = append(stripped, item)
				}
			}
			out[k] = stripped
		}
	}
	return out
}

// SchemaNormalization describes which non-standard JSON Schema fields to
// strip or rewrite before sending to a provider that rejects them.
type SchemaNormalization int

const (
	// NoNormalization passes schemas through unchanged.
	NoNormalization SchemaNormalization = iota
	// StripMetaFields removes $schema, $id, $ref, $defs and all
	// other $-prefixed keys. Safe for any provider.
	StripMetaFields
	// NormalizeForGemini strips meta-fields plus rewrites fields that
	// Gemini's protobuf Schema type does not accept: exclusiveMinimum,
	// exclusiveMaximum, additionalProperties, anyOf/oneOf/allOf, and
	// type arrays. Also protects nullable:false from overwriting a
	// type-derived nullable:true via map-iteration-order randomness.
	NormalizeForGemini
	// NormalizeForAnthropic strips meta-fields plus rewrites the same
	// unsupported fields as NormalizeForGemini. Anthropic validates tool
	// schemas strictly and returns 400 on unsupported keywords.
	NormalizeForAnthropic
)

// NormalizeSchema applies the given normalization to schema and returns a new
// map (the input is never mutated). Returns nil if schema is nil.
func NormalizeSchema(schema map[string]interface{}, norm SchemaNormalization) map[string]interface{} {
	if schema == nil || norm == NoNormalization {
		return schema
	}
	return normalizeSchemaRecursive(schema, norm)
}

func normalizeSchemaRecursive(schema map[string]interface{}, norm SchemaNormalization) map[string]interface{} {
	out := make(map[string]interface{}, len(schema))
	typeDerivedNullable := false
	for k, v := range schema {
		switch k {
		case "$schema", "$id", "$ref", "$defs":
			continue
		case "exclusiveMinimum", "exclusiveMaximum", "additionalProperties":
			if norm >= NormalizeForGemini {
				continue
			}
			out[k] = normalizeSchemaValue(v, norm)
		case "anyOf", "oneOf":
			merged := normalizeUnion(v, norm)
			if mergedMap, ok := merged.(map[string]interface{}); ok {
				for mk, mv := range mergedMap {
					out[mk] = mv
				}
			}
			continue
		case "allOf":
			merged := normalizeAllOf(v, norm)
			if mergedMap, ok := merged.(map[string]interface{}); ok {
				for mk, mv := range mergedMap {
					out[mk] = mv
				}
			}
			continue
		case "type":
			typ := normalizeType(v)
			out[k] = typ
			if arr, ok := v.([]interface{}); ok {
				for _, item := range arr {
					if s, ok := item.(string); ok && s == "null" {
						out["nullable"] = true
						typeDerivedNullable = true
						break
					}
				}
			}
			continue
		case "nullable":
			if norm >= NormalizeForGemini && typeDerivedNullable {
				continue // preserve type-derived nullable; don't let explicit false win
			}
			out[k] = v
			continue
		default:
			// Skip unknown top-level schema keywords for strict providers,
			// but recurse into properties/items so their child schemas are
			// normalized while property names pass through untouched.
			if norm >= NormalizeForAnthropic && !isValidJsonSchemaField(k) {
				continue
			}
			switch k {
			case "properties":
				if m, ok := v.(map[string]interface{}); ok {
					out[k] = normalizePropertiesMap(m, norm)
				} else {
					out[k] = v
				}
			case "items":
				out[k] = normalizeSchemaValue(v, norm)
			default:
				out[k] = normalizeSchemaValue(v, norm)
			}
		}
	}
	return out
}

// normalizePropertiesMap normalizes each property's schema while keeping the
// property name itself untouched (it is a user-chosen identifier, not a schema
// keyword).
func normalizePropertiesMap(props map[string]interface{}, norm SchemaNormalization) map[string]interface{} {
	out := make(map[string]interface{}, len(props))
	for name, schema := range props {
		out[name] = normalizeSchemaValue(schema, norm)
	}
	return out
}

// isValidJsonSchemaField reports whether k is a standard JSON Schema keyword
// that providers like Anthropic and Gemini accept in tool schemas.
func isValidJsonSchemaField(k string) bool {
	switch k {
	case "type", "properties", "items", "required", "enum", "default",
		"description", "format", "nullable",
		"minimum", "maximum", "minLength", "maxLength",
		"pattern", "minItems", "maxItems",
		"anyOf", "oneOf", "allOf",
		"exclusiveMinimum", "exclusiveMaximum", "additionalProperties",
		"$schema", "$id", "$ref", "$defs":
		return true
	}
	return false
}

func normalizeSchemaValue(v interface{}, norm SchemaNormalization) interface{} {
	switch val := v.(type) {
	case map[string]interface{}:
		return normalizeSchemaRecursive(val, norm)
	case []interface{}:
		stripped := make([]interface{}, 0, len(val))
		for _, item := range val {
			if m, ok := item.(map[string]interface{}); ok {
				stripped = append(stripped, normalizeSchemaRecursive(m, norm))
			} else {
				stripped = append(stripped, item)
			}
		}
		return stripped
	}
	return v
}

func normalizeUnion(v interface{}, norm SchemaNormalization) interface{} {
	arr, ok := toInterfaceSlice(v)
	if !ok || len(arr) == 0 {
		return v
	}
	nonNull := make([]map[string]interface{}, 0, len(arr))
	hasNull := false
	for _, item := range arr {
		m, ok := toInterfaceMap(item)
		if !ok {
			continue
		}
		t, _ := m["type"].(string)
		if t == "null" {
			hasNull = true
			continue
		}
		nonNull = append(nonNull, m)
	}
	if hasNull && len(nonNull) == 1 {
		schema := normalizeSchemaRecursive(nonNull[0], norm)
		schema["nullable"] = true
		return schema
	}
	if len(nonNull) == 1 {
		return normalizeSchemaRecursive(nonNull[0], norm)
	}
	if len(arr) > 0 {
		if m, ok := toInterfaceMap(arr[0]); ok {
			return normalizeSchemaRecursive(m, norm)
		}
	}
	return v
}

func normalizeAllOf(v interface{}, norm SchemaNormalization) interface{} {
	arr, ok := toInterfaceSlice(v)
	if !ok || len(arr) == 0 {
		return v
	}
	if m, ok := toInterfaceMap(arr[0]); ok {
		return normalizeSchemaRecursive(m, norm)
	}
	return v
}

func normalizeType(v interface{}) string {
	switch tv := v.(type) {
	case string:
		return tv
	case []interface{}:
		for _, item := range tv {
			if s, ok := item.(string); ok && s != "null" {
				return s
			}
		}
		if len(tv) > 0 {
			if s, ok := tv[0].(string); ok {
				return s
			}
		}
		return ""
	}
	return ""
}

func toInterfaceSlice(v interface{}) ([]interface{}, bool) {
	s, ok := v.([]interface{})
	return s, ok
}

func toInterfaceMap(v interface{}) (map[string]interface{}, bool) {
	m, ok := v.(map[string]interface{})
	return m, ok
}
