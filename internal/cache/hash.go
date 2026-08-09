package cache

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
	"time"
)

// HashBuilder creates a deterministic hash for a chat completion request.
type HashBuilder struct {
	// IncludeMessages determines whether request messages are included in the hash.
	// When false, only structural fields (model, params) are hashed — useful when
	// message content is sensitive or varies between cache-missed requests.
	IncludeMessages bool
	// NormalizeMessages determines whether message content is normalized (trimmed,
	// whitespace collapsed) before hashing.
	NormalizeMessages bool
}

// DefaultHashBuilder returns a hash builder with sensible defaults.
func DefaultHashBuilder() *HashBuilder {
	return &HashBuilder{
		IncludeMessages:   true,
		NormalizeMessages: true,
	}
}

// BuildHash creates a hash key from a chat completion request.
func (hb *HashBuilder) BuildHash(model string, messages []interface{}, params map[string]interface{}) string {
	var b strings.Builder
	b.WriteString(model)
	b.WriteByte('|')

	// Hash structural params first for stability.
	paramKeys := make([]string, 0, len(params))
	for k := range params {
		paramKeys = append(paramKeys, k)
	}
	sortStrings(paramKeys)
	for _, k := range paramKeys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(fmt.Sprintf("%v", params[k]))
		b.WriteByte('|')
	}

	if hb.IncludeMessages && len(messages) > 0 {
		b.WriteByte('M')
		for i, msg := range messages {
			b.WriteString(strconv.Itoa(i))
			b.WriteByte(':')
			b.WriteString(formatMessage(msg))
			if i < len(messages)-1 {
				b.WriteByte(',')
			}
		}
	}

	h := fnvHash(b.String())
	return fmt.Sprintf("%016x", h)
}

// BuildContentHash creates a content hash for a value (e.g., response body).
func BuildContentHash(data []byte) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h[:8]) // Use first 8 bytes for a concise hash.
}

// BuildCompositeHash creates a hash from multiple components.
func BuildCompositeHash(components ...string) string {
	var b strings.Builder
	for i, c := range components {
		if i > 0 {
			b.WriteByte('|')
		}
		b.WriteString(c)
	}
	h := fnvHash(b.String())
	return fmt.Sprintf("%016x", h)
}

func fnvHash(s string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return h.Sum32()
}

func formatMessage(msg interface{}) string {
	switch v := msg.(type) {
	case map[string]interface{}:
		var b strings.Builder
		if role, ok := v["role"]; ok {
			b.WriteString(fmt.Sprintf("role=%v", role))
		}
		if content, ok := v["content"]; ok {
			b.WriteString(fmt.Sprintf("|content=%v", formatContent(content)))
		}
		return b.String()
	case string:
		return v
	default:
		data, _ := json.Marshal(v)
		return string(data)
	}
}

func formatContent(content interface{}) string {
	switch v := content.(type) {
	case string:
		if v == "" {
			return ""
		}
		if len(v) > 200 {
			return v[:200] + "...(truncated)"
		}
		return v
	case []interface{}:
		var parts []string
		for _, p := range v {
			parts = append(parts, fmt.Sprintf("%v", p))
		}
		return strings.Join(parts, ",")
	default:
		data, _ := json.Marshal(v)
		return string(data)
	}
}

// CacheKey builds a cache key from request components.
func CacheKey(model string, messages []interface{}, params map[string]interface{}) string {
	hb := DefaultHashBuilder()
	return hb.BuildHash(model, messages, params)
}

// ResponseCacheKey builds a cache key specifically for response caching.
func ResponseCacheKey(model string, messages []interface{}, params map[string]interface{}) string {
	return "resp:" + CacheKey(model, messages, params)
}

// EmbeddingCacheKey builds a cache key for embedding requests.
func EmbeddingCacheKey(model string, input interface{}) string {
	var b strings.Builder
	b.WriteString("emb:")
	b.WriteString(model)
	b.WriteByte('|')
	switch v := input.(type) {
	case string:
		b.WriteString(v)
	case []string:
		for i, s := range v {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(s)
		}
	default:
		data, _ := json.Marshal(input)
		b.Write(data)
	}
	return b.String()
}

// sortStrings sorts a string slice in place (standard library sort not imported).
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// ParseHashKey decodes a hex hash key back to its uint32 representation.
func ParseHashKey(key string) (uint32, error) {
	var h uint32
	_, err := fmt.Sscanf(key, "%08x", &h)
	return h, err
}

// EncodeUint32 encodes a uint32 as a string.
func EncodeUint32(v uint32) string {
	return strconv.FormatUint(uint64(v), 10)
}

// DecodeUint32 decodes a string to uint32.
func DecodeUint32(s string) (uint32, error) {
	v, err := strconv.ParseUint(s, 10, 32)
	return uint32(v), err
}

// EncodeUint64 encodes a uint64 as a string.
func EncodeUint64(v uint64) string {
	return strconv.FormatUint(v, 10)
}

// DecodeUint64 decodes a string to uint64.
func DecodeUint64(s string) (uint64, error) {
	return strconv.ParseUint(s, 10, 64)
}

// EncodeInt64 encodes an int64 as a string.
func EncodeInt64(v int64) string {
	return strconv.FormatInt(v, 10)
}

// DecodeInt64 decodes a string to int64.
func DecodeInt64(s string) (int64, error) {
	return strconv.ParseInt(s, 10, 64)
}

// EncodeTime encodes a time.Time as unix nanoseconds.
func EncodeTime(t time.Time) int64 {
	return t.UnixNano()
}

// DecodeTime decodes unix nanoseconds to time.Time.
func DecodeTime(nanos int64) time.Time {
	return time.Unix(0, nanos)
}

// EncodeDuration encodes a time.Duration as nanoseconds.
func EncodeDuration(d time.Duration) int64 {
	return d.Nanoseconds()
}

// DecodeDuration decodes nanoseconds to time.Duration.
func DecodeDuration(nanos int64) time.Duration {
	return time.Duration(nanos)
}
