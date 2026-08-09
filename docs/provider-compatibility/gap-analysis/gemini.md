# Gap Analysis: Google Gemini (Provider Sprint #003)

- **Provider**: `gemini`
- **Adapter package**: `internal/provider/gemini`
- **Canonical contract**: unchanged (stable)
- **Status**: Native adapter implemented; OpenAI-compatible passthrough retained only as the pre-sprint baseline in history.

---

## 1. Verified API Shape

Verified against Google's authoritative documentation (`ai.google.dev/gemini-api/docs/...`, the REST reference on `docs.cloud.google.com`, and Google's own `@google/genai` SDK type definitions for `ThinkingConfig`).

### Native endpoints

| Operation              | Endpoint                                                                    |
|------------------------|-----------------------------------------------------------------------------|
| Chat (non-stream)      | `POST /v1beta/models/{model}:generateContent`                              |
| Chat (stream)          | `POST /v1beta/models/{model}:streamGenerateContent?alt=sse`                |
| Embeddings (single)    | `POST /v1beta/models/{model}:embedContent`                                 |
| Model catalog          | `GET /v1beta/models`                                                       |

The Gemini provider also exposes an **OpenAI-compatible** surface at `/v1beta/openai/...` (`/chat/completions`, `/embeddings`, `/models`). It was this surface (via `openaibase.Base`) that Conductor used before this sprint. It is a strict subset of the native API and — notably — the pre-sprint wiring appended `/chat/completions` to a base URL of `https://generativelanguage.googleapis.com/v1beta`, which does not even land on the `/v1beta/openai/` prefix. This adapter therefore switches to the native API.

### Authentication

- API key via `x-goog-api-key: <key>` header **or** `?key=<key>` query parameter; also accepts OAuth `Authorization: Bearer`.
- This adapter uses the `x-goog-api-key` header (keyed to `GEMINI_API_KEY`).

### Request schema (`generateContent`)

| Field                 | Wire name               | Notes |
|-----------------------|-------------------------|-------|
| `contents`            | `contents[]`            | Conversation; roles are `user` and `model` (not `assistant`). Consecutive same-role contents are **not** allowed and are merged. |
| `systemInstruction`   | `systemInstruction`     | `{ "parts": [ { "text": "..." } ] }` |
| `tools`               | `tools[]`               | `{ "functionDeclarations": [...] }` |
| `toolConfig`          | `toolConfig.functionCallingConfig` | `mode: AUTO | ANY | NONE`, `allowedFunctionNames[]` |
| `generationConfig`    | `generationConfig`      | `temperature`, `topP`, `topK`, `maxOutputTokens`, `stopSequences`, `responseMimeType`, `responseSchema`, `seed`, `presencePenalty`, `frequencyPenalty`, `candidateCount`, `thinkingConfig` |
| `thinkingConfig`      | `thinkingConfig`        | `includeThoughts` (bool, thought **summaries**), `thinkingBudget` (int tokens; Gemini 2.5), `thinkingLevel` (`LOW/MEDIUM/HIGH`, Gemini 3). `thinkingLevel` and `thinkingBudget` must not both be set. |

**Parts** (`Part` is a union): `text`, `inlineData` (`Blob{ mimeType, data(base64) }`), `fileData`, `functionCall { name, args }`, `functionResponse { name, response }`. Thought parts are marked `"thought": true` alongside text.

### Response schema (`GenerateContentResponse`)

```
candidates[] {
  content { parts[], role: "model" },
  finishReason: STOP | MAX_TOKENS | SAFETY | RECITATION | LANGUAGE | OTHER |
                BLOCKLIST | PROHIBITED_CONTENT | SPII | MALFORMED_FUNCTION_CALL,
  index, safetyRatings[], citationMetadata, tokenCount
}
usageMetadata {
  promptTokenCount, candidatesTokenCount, totalTokenCount,
  thoughtsTokenCount, cachedContentTokenCount
}
modelVersion
```

There is **no** response `id` field in `generateContent`; the adapter synthesizes one.

### Streaming protocol

- SSE over `?alt=sse`. Each `data:` event body is a full `GenerateContentResponse` — the same object as non-streaming, per chunk. There is **no `[DONE]` sentinel**; the stream ends when the HTTP body closes.
- Text arrives incrementally across chunk `parts`. Function-call `args` **can** arrive as fragments that only become valid JSON across multiple events → a stateful accumulator is required (same requirement as Anthropic's `input_json_delta`).
- `usageMetadata` is populated on the final chunk(s).

### Structured output

`responseMimeType: "application/json"` **and** `responseSchema` (JSON Schema) together give strict JSON. `responseMimeType` alone gives lenient JSON mode.

### Vision / multimodal

`inlineData` (base64) is the native image input. `fileData` accepts Google-owned URIs (`gs://…`). Arbitrary `http(s)` image URLs are **not** accepted by `generateContent` (they work only via the OpenAI-compatible shim / Files API), so they are a documented gap.

### Thinking / reasoning

- Gemini 2.5: `thinkingConfig.thinkingBudget` (tokens; `0` disables, `-1` dynamic).
- Gemini 3: `thinkingConfig.thinkingLevel`.
- Reasoning text is returned as `thought`-flagged parts; token counts in `usageMetadata.thoughtsTokenCount`.

### Usage metadata

`promptTokenCount` / `candidatesTokenCount` / `totalTokenCount`, plus `thoughtsTokenCount` (map to `completion_tokens_details.reasoning_tokens`) and `cachedContentTokenCount`.

### Error format

Google standard `Status` object:

```json
{ "error": { "code": 400, "message": "…", "status": "INVALID_ARGUMENT" } }
```

| HTTP | status                    | Conductor mapping                          |
|------|---------------------------|--------------------------------------------|
| 400  | `INVALID_ARGUMENT`/`FAILED_PRECONDITION` | invalid_request_error (not retryable) |
| 401  | `UNAUTHENTICATED`         | authentication_error |
| 403  | `PERMISSION_DENIED`       | authentication_error |
| 404  | `NOT_FOUND`               | invalid_request_error |
| 429  | `RESOURCE_EXHAUSTED`      | rate_limit_error (retryable) |
| 500  | `INTERNAL`                | server_error (retryable) |
| 503  | `UNAVAILABLE`             | provider_unavailable (retryable) |
| 504  | `DEADLINE_EXCEEDED`       | provider_unavailable (retryable) |

Context-length errors are 400 `INVALID_ARGUMENT` with message keywords ("token limit", "context length", "too many tokens"); the mapper detects these and marks `context_length_exceeded`, non-retryable.

---

## 2. Implementation Decision

Build a **native `generateContent` adapter** (not the OpenAI-compatible shim):

1. Native API is the superset — structured output and reasoning/thinking only exist there (the old provider doc even listed thinking as "not supported").
2. The pre-sprint path was effectively broken (wrong path relative to the configured base URL).
3. The sprint's contract is to validate Canonical Contract against a third genuinely different provider API.

`NewProvider(apiKey, baseURL string, timeout time.Duration)` keeps the same signature used by `cmd/conductor/main.go` and `config.yaml` (`providers.gemini.base_url`, default `https://generativelanguage.googleapis.com/v1beta`) so no router/handler/config changes are required.

---

## 3. Adapter Architecture

| File            | Responsibility |
|-----------------|---------------|
| `adapter.go`    | `Provider` struct; `Provider` interface methods (chat, stream, embeddings, models, pricing, health, `SupportsModel`); `GetMetadata`; synthetic response ID. |
| `request_mapper.go` | `MapRequest(*apitypes.ChatCompletionRequest)` → Gemini `generateContentRequest` (messages, system, multimodal, tools, tool choice, structured output, thinking). All Gemini wire types live here. |
| `response_mapper.go` | `MapResponse(model, *generateContentResponse)` → canonical `ChatCompletionResponse`. |
| `stream_mapper.go` | `MapStreamEvent(event, accum)` + `*streamAccumulator` → canonical `StreamChunk`; incremental function-call accumulation; malformed-event handling. |
| `tool_mapper.go` | Schema / tool-choice mapping; arguments marshalling; synthetic tool-call IDs and `ToolCallID` ↔ function-name correlation. |
| `error_mapper.go` | Google status → `ProviderError`; retryability; context-length detection. |
| `auth.go`        | `AuthConfig`, API-key validation. |
| `transport.go`   | `newRequest` (`x-goog-api-key`), URL building for `:generateContent` / `:streamGenerateContent` / `:embedContent` / `/models`, error-response handling. |

No Gemini wire type escapes the package (unexported). The canonical contract in `internal/apitypes` is untouched.

---

## 4. Request Mapping (`request_mapper.go`)

| Canonical                     | Gemini                                      |
|-------------------------------|---------------------------------------------|
| `messages[].role == "system"` | `systemInstruction.parts[].text`           |
| `messages[].role == "user"`   | `contents[]` role `user` (merged with adjacent `user`/`tool`) |
| `messages[].role == "assistant"` | `contents[]` role `model`               |
| `messages[].ToolCalls`        | `functionCall` parts on the `model` content |
| `messages[].ToolCallID`       | `functionResponse` part on a `user` content (name correlated from the preceding assistant `ToolCall`) |
| multimodal `image_url` data-URI | `inlineData { mimeType, data }`            |
| `temperature`                 | `generationConfig.temperature`             |
| `top_p`                       | `generationConfig.topP`                    |
| `max_tokens`                  | `generationConfig.maxOutputTokens`         |
| `stop`                        | `generationConfig.stopSequences`           |
| `n`                           | `generationConfig.candidateCount`          |
| `response_format`             | `generationConfig.responseMimeType` (`application/json`) + `responseSchema` |
| `reasoning` / `reasoning_effort` / `thinking_budget` | `thinkingConfig` (`thinkingBudget`, `thinkingLevel`, `includeThoughts`) |
| `tools`                       | `tools[].functionDeclarations`             |
| `tool_choice`                 | `toolConfig.functionCallingConfig` (`AUTO`/`ANY`/`NONE`, `allowedFunctionNames`) |

Unsupported canonical fields (`frequency_penalty`, `presence_penalty`, `seed`, `logit_bias`) — Gemini has equivalents (`frequencyPenalty`, `presencePenalty`, `seed`), so forward them; `logit_bias`/`logprobs` have no native equivalent and are dropped (documented below). `user` is dropped.

---

## 5. Response Mapping (`response_mapper.go`)

| Gemini                             | Canonical                                   |
|------------------------------------|---------------------------------------------|
| `candidates[i].content.parts[]`    | one `Choice` per candidate (`index = i`)     |
| `part.text` (non-thought)          | `message.content` (concatenated)             |
| `part.text` with `thought:true`      | `message.reasoning` (concatenated)           |
| `part.functionCall {name,args}`    | `message.tool_calls[]` (`id` synthesized per candidate, `type:"function"`, `arguments` = JSON string) |
| `candidates[i].finishReason`       | `finish_reason` (see table below)            |
| `usageMetadata`                    | `usage{ promptTokenCount, candidatesTokenCount, totalTokenCount, cached→prompt_tokens_details.cached_tokens, thoughts→completion_tokens_details.reasoning_tokens }` |
| `modelVersion`                     | `model` echo (request model preserved)      |
| —                                 | `id`: synthesized `gemini-<nano>` (no native id) |

Finish reasons: `STOP`→`stop`, `MAX_TOKENS`→`length`, `SAFETY`/`RECITATION`/`PROHIBITED_CONTENT`→`content_filter`, `MALFORMED_FUNCTION_CALL`→`tool_calls`, others→`stop`.

---

## 6. Streaming Mapping (`stream_mapper.go`)

`streamGenerateContent?alt=sse` is consumed with a `bufio.Scanner`; each `data:` payload is decoded as a `generateContentResponse` and fed to a stateful `streamAccumulator`:

- **Text deltas**: `part.text` (non-thought) → `delta.content`.
- **Reasoning/thinking deltas**: `part.text` with `thought:true` → `delta.reasoning`.
- **Function-call deltas**: `part.functionCall` args are accumulated per (`candidateIndex`, `function.name`). A tool-call delta is emitted only once the accumulated args form balanced, valid JSON (fragmented delivery) or immediately (single-shot delivery). On stream finish, incomplete buffers are flushed best-effort.
- **Usage**: last-chunk `usageMetadata` forwarded on the final content chunk.
- **Finish reason**: forwarded when `candidates[].finishReason` set.
- **Termination**: no `[DONE]` sentinel; the emitter closes the channel and sends one `StreamChunk{Done:true}` after the body closes.
- **Malformed/empty**: empty `data:` events skipped; unparseable payloads emit `StreamChunk{Error}`.

---

## 7. Tool Calling (`tool_mapper.go`)

- **Single + multiple function calls**: supported. Non-streaming responses can carry several `functionCall` parts → several canonical `tool_calls`. Streaming accumulates fragments per call index.
- **Ordering**: preserved — canonical `tool_calls` follow part order; `contents` preserve message order.
- **Tool IDs**: native `generateContent` has **no tool-call IDs**. The adapter synthesizes stable IDs (`call_generateContent...`) and correlates tool results by function name (via the preceding assistant message) instead of IDs. Documented limitation, no canonical change required (canonical `id` is provider-supplied).
- **Tool results / multi-turn**: canonical `tool` messages are forwarded as `functionResponse` parts on a `user` content; adjacent `tool`/`user` messages are merged to satisfy Gemini's alternating-role requirement. The tool-to-call correlation uses the function name remembered from the closest preceding assistant `ToolCall`.
- **Tool choice**: `tool_choice == "none"`→`NONE`; `"auto"`→`AUTO`; `"required"`→`ANY`; `{"type":"tool"|"function","function":{"name":…}}`→`ANY` + `allowedFunctionNames`.

Full multi-turn tool execution is covered by a dedicated test.

---

## 8. Error Mapping (`error_mapper.go`)

See the table in §1. `MapError` reads the Google `error{code,status,message}` envelope, maps status*code/system to the canonical error, and exposes `Retryable()` helpers. Retryable = `429 RESOURCE_EXHAUSTED`, `500 INTERNAL`, `503 UNAVAILABLE`, `504 DEADLINE_EXCEEDED`; everything else non-retryable. Context-length is detected via message keywords and reported as `context_length_exceeded` (non-retryable).

---

## 9. Compatibility Gaps (no canonical changes)

Gaps documented rather than fabricated:

1. **No native tool-call IDs.** Synthetic `call_<…>` IDs are generated. Multi-call same-function streaming/parallel ambiguity is resolved by part index, not an ID.
2. **Arbitrary `http(s)` image URLs unsupported** by `generateContent`. Only base64 `data:` URIs (inline) and `gs://` file URIs (fileData) are handled; remote URLs are dropped (a text part is retained so message text survives). File API upload is out of scope.
3. **`logit_bias`, `logprobs`** have no native equivalent → dropped (documented; no fabrication).
4. **`ThinkingConfig` budget vs level**: both cannot be set together. The mapper prefers `thinkingBudget` when `thinking_budget`/`reasoning.max_tokens` is present (Gemini 2.5), else `thinkingLevel` from `reasoning_effort` (Gemini 3). `includeThoughts` is mapped from `include_reasoning` / `reasoning` config (`exclude`→false).
5. **`candidateCount > 1`** produces multiple canonical choices; `n` is honored only where the model supports it (same as OpenAI).
6. **Reasoning surfacing**: only Gemini's thought *summaries* (`includeThoughts`) are returned; un-budget raw chain-of-thought is not streamed by `generateContent` — `thoughtsTokenCount` still appears in usage.

Per the Canonical Contract rule, none of these block basic adapter correctness, so **no canonical change is made**.

---

## 10. Test Results

See `internal/provider/gemini/provider_test.go` (all mocked HTTP + SSE; no live credentials).

Run with:

```
go test ./internal/provider/gemini/...
```

Running within sprint to be pasted here.

---

## 11. Coverage

Coverage report generated in-sprint (see results below).

---

## 12. Build / Race / Vet

Commands from the sprint regression block:

```
go build ./...
go test ./...
go test -race ./internal/provider/gemini/... ./internal/provider/openai/... ./internal/provider/anthropic/...
go vet ./internal/provider/gemini/...
```

Results pasted after execution.

---

## 13. Commit Hash

Gemini-only commit: `<pasted>`.

---

## 14. Remaining Provider Compatibility Risks

- **Model-level thinking support varies** (`thinkingBudget` vs `thinkingLevel` vs none for e.g. `gemini-1.5`). The adapter sends a config; Gemini returns 400 `INVALID_ARGUMENT` for unsupported models — surfaced verbatim.
- **`allowedFunctionNames` in `ANY` mode** requires the function name to exist in the declared tools; mismatched names → 400.
- **Streaming tool-call fragmentation** is tolerated but relies on JSON-balance heuristics; adversarial fragments (unbalanced strings with braces) may defer emission until final flush.
- **OpenAI-compatible consumers** receive canonical shapes, but Gemini-native model set is different (e.g. no `o1`/`claude`); routing relies on `gemini` prefix matching.
- **`http(s)` image URLs** (common from OpenAI clients) will silently lose the image part in native mode — this is the single most likely user-visible regression vs the previous OpenAI-compatible passthrough.