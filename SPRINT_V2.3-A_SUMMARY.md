# Sprint V2.3-A: Provider Compatibility Layer — Summary

## Sprint Overview

| Field | Value |
|-------|-------|
| **Sprint** | V2.3-A |
| **Objective** | Transform Conductor from a provider router into a Provider Compatibility Layer |
| **Scope** | Documentation, contracts, metadata, certification planning only |
| **Out of Scope** | Provider adapters, routing changes, policy logic, learning engine |

This sprint established the foundational documentation and design artifacts for Conductor's transition into a full provider compatibility layer. The canonical contract, compatibility matrix, provider scorecard, and certification suite design are now in place to guide future adapter implementation work.

---

## Goals Completed

| # | Goal | Status |
|---|------|--------|
| 1 | Define the canonical contract — Conductor's provider-agnostic request/response types | ✅ Done |
| 2 | Document per-provider compatibility specs for all 17 providers | ✅ Done |
| 3 | Create a compatibility matrix showing feature coverage across providers | ✅ Done |
| 4 | Define a provider scorecard framework for measuring adapter quality | ✅ Done |
| 5 | Design a provider capabilities metadata schema | ✅ Done |
| 6 | Design a provider certification test suite (17 scenarios, 5 levels) | ✅ Done |
| 7 | Document the provider onboarding workflow | ✅ Done |
| 8 | Establish the future adapter implementation roadmap | ✅ Done |

---

## Documentation Produced

| File | Description |
|------|-------------|
| `docs/provider-compatibility/README.md` | Index and navigation for the compatibility layer docs |
| `docs/provider-compatibility/canonical-contract.md` | Full specification of Conductor's internal canonical types: `CanonicalRequest`, `CanonicalResponse`, `CanonicalMessage`, `CanonicalContentBlock`, `CanonicalTool`, `CanonicalToolCall`, `CanonicalUsage`, `CanonicalStreamChunk`, `CanonicalThinking`, `CanonicalFinishReason`, `CanonicalError`, `CanonicalStructuredOutput`, and mapping notes for OpenAI, Anthropic, Ollama, and NVIDIA NIM |
| `docs/provider-compatibility/compatibility-matrix.md` | Feature coverage matrix across all 17 supported providers, covering chat, streaming, vision, tool calling, reasoning, structured output, long context, embeddings, and more |
| `docs/provider-compatibility/provider-scorecard.md` | Scoring framework with 6 categories: contract compliance, feature coverage, error handling, performance, reliability, and documentation — each weighted and scored 0–100 |
| `docs/provider-compatibility/provider-capabilities.schema.yaml` | JSON Schema for the `Capabilities` metadata struct, defining the shape of provider capability declarations |
| `docs/provider-compatibility/providers/*.md` | Per-provider specification documents for 17 providers: OpenAI, Anthropic, Ollama, NVIDIA NIM, Google Gemini, Mistral, Cerebras, Groq, Azure OpenAI, Amazon Bedrock, Vertex AI, Cohere, AI21 Labs, Perplexity, DeepSeek, Qwen, and xAI Grok |
| `tests/provider-certification/README.md` | Provider certification suite design: 17 test scenarios, 5 certification levels (Platinum/Gold/Silver/Bronze/Untested), runner configuration, test matrix, result reporting, and CI integration |

---

## Canonical Contract Summary

The canonical contract defines Conductor's internal, provider-agnostic types that every adapter must produce and consume. It is **not** OpenAI's shape and **not** Anthropic's shape — it is Conductor's own normalized model.

**Canonical Model Inventory:**

| Type | Purpose |
|------|---------|
| `CanonicalRequest` | Normalized inbound request with model, messages, tools, stream flag, and parameters |
| `CanonicalResponse` | Normalized full response with choices, usage, and metadata |
| `CanonicalMessage` | Unified message with role, content blocks, tool calls, and thinking |
| `CanonicalContentBlock` | Individual content piece: text, image, tool_use, tool_result, or thinking |
| `CanonicalTool` / `CanonicalFunction` | Tool definition with name, description, and JSON Schema parameters |
| `CanonicalToolCall` / `CanonicalFunctionCall` | Tool invocation with id, name, and JSON arguments |
| `CanonicalToolResult` | Tool response linked by `tool_call_id` |
| `CanonicalUsage` | Token counts including cache tokens and reasoning token breakdowns |
| `CanonicalStreamChunk` | Individual SSE chunk with delta, finish_reason, and optional usage |
| `CanonicalThinking` | Chain-of-thought / reasoning content, separate from main content |
| `CanonicalFinishReason` | Why generation stopped: stop, length, tool_calls, content_filter, interrupt, unknown |
| `CanonicalStructuredOutput` | Constrained output request: `json_object` or `json_schema` |
| `CanonicalError` | Provider-agnostic error with code, status, retryable flag, and provider details |
| `CanonicalProviderCapabilities` | Feature flags: streaming, vision, reasoning, tool_calling, structured_output, etc. |

Provider adapters translate their native formats into these shapes so routing, cost tracking, and the dashboard API work uniformly.

---

## Compatibility Matrix Summary

The compatibility matrix (`docs/provider-compatibility/compatibility-matrix.md`) covers feature support across all 17 providers:

**Features Tracked:**
- `chat` — Basic text completion
- `stream` — SSE streaming support
- `vision` — Image input
- `tool_calling` — Function/tool calling
- `parallel_tools` — Multiple simultaneous tool calls
- `reasoning` — Chain-of-thought / thinking output
- `structured_output` — JSON Schema constrained output
- `json_output` — Simple `json_object` output
- `long_context` — Context window > 128k tokens
- `embeddings` — Embedding endpoint
- `audio` — Audio input/output (future)
- `images` — Image generation (future)

**Status Symbols:** ✅ Implemented · 🟡 Planned · 🔲 Stub · ❌ Not Supported · ❓ Unknown

The matrix reveals capability gaps (e.g., no provider currently supports both `reasoning` and `parallel_tools` at Platinum level) and guides implementation prioritization.

---

## Provider Scorecard Framework

The scorecard (`docs/provider-compatibility/provider-scorecard.md`) provides a quantitative measure of adapter quality across six weighted categories:

| Category | Weight | What It Measures |
|----------|--------|-----------------|
| **Contract Compliance** | 30% | Adherence to canonical types, correct field mapping, no data loss |
| **Feature Coverage** | 25% | Percentage of declared capabilities that are actually implemented |
| **Error Handling** | 15% | Correct error code mapping, retryable flag accuracy, provider error preservation |
| **Performance** | 10% | Latency overhead, memory efficiency, streaming throughput |
| **Reliability** | 10% | Pass rate on certification scenarios, timeout handling, cancellation safety |
| **Documentation** | 10% | completeness of per-provider docs, mapping notes, and known limitations |

**Overall Score → Certification Level:**
- 90–100: Platinum
- 75–89: Gold
- 60–74: Silver
- 40–59: Bronze
- <40: Untested

---

## Certification Suite Overview

The certification suite (`tests/provider-certification/README.md`) defines 17 contract tests that all certified providers must pass:

| # | Scenario | Level Requirement |
|---|----------|-------------------|
| 1 | Basic Chat | All levels |
| 2 | Streaming | Gold+ |
| 3 | Tool Calling (Single) | Silver+ |
| 4 | Parallel Tool Calls | Gold+ |
| 5 | JSON Output | Silver+ |
| 6 | Structured Output (JSON Schema) | Platinum |
| 7 | Thinking / Reasoning | Platinum |
| 8 | Vision | Platinum |
| 9 | Long Context | Platinum |
| 10 | Error Handling | All levels |
| 11 | Rate Limits | Gold+ |
| 12 | Timeout Recovery | Gold+ |
| 13 | Retry | Silver+ |
| 14 | Cancellation | Platinum |
| 15 | Failover | Platinum |
| 16 | Multi-turn Tool Loop | Gold |
| 17 | Agent Workflow | Platinum |

**Certification Levels:**
- **Platinum** (17/17): Full contract compliance, all features certified
- **Gold** (14/17): Core features certified, up to 3 non-critical gaps
- **Silver** (10/17): Basic compliance, streaming + tools + errors pass
- **Bronze** (5/17): Minimal compliance, chat + errors pass
- **Untested** (<5): Not production-ready

---

## Provider Onboarding Workflow

Onboarding a new provider adapter follows this workflow:

### Step 1: Implement Provider Interface

```go
// internal/provider/interface.go
type Provider interface {
    Name() string
    SupportsModel(modelID string) bool
    Supports(capability Capability) bool
    Chat(ctx context.Context, req *CanonicalRequest) (*CanonicalResponse, error)
    ChatStream(ctx context.Context, req *CanonicalRequest) (<-chan *CanonicalStreamChunk, error)
    GetModels(ctx context.Context) ([]ModelInfo, error)
    HealthCheck(ctx context.Context) error
}
```

Create `internal/provider/<name>/adapter.go` implementing this interface. Map native request/response formats to/from canonical types per `docs/provider-compatibility/<name>.md`.

### Step 2: Register with Registry

Add the provider to `internal/provider/registry.go`:

```go
import _ "github.com/.../internal/provider/newprovider"

func init() {
    registry.Register(&NewProviderAdapter{})
}
```

### Step 3: Configure in config.yaml

```yaml
providers:
  newprovider:
    enabled: true
    base_url: "https://api.newprovider.com/v1"
    api_key_env: "NEWPROVIDER_API_KEY"
    models:
      - "newmodel-1"
    capabilities:
      streaming: true
      vision: false
      tool_calling: true
```

### Step 4: Run Certification Suite

```bash
# Run the full certification suite for the new provider
go test ./tests/provider-certification/... -run Certification/NewProvider -v

# Review results
cat tests/provider-certification/results/latest/summary.json
```

### Step 5: Achieve Certification Level

| Result | Action |
|--------|--------|
| Platinum (17/17) | Provider is production-ready. Add to `docs/provider-compatibility/compatibility-matrix.md`. |
| Gold (14+/17) | Approve for production with documented gaps. |
| Silver (10+/17) | Approve for limited use. Fix gaps before general availability. |
| Bronze (5+/17) | Not production-ready. Prioritize remaining scenarios. |
| < 5 | Do not ship. Return to Step 1. |

---

## Future Adapter Roadmap

### Phase 1: Core OpenAI-Compatible Providers (Complete)

| Provider | Status | Certification |
|----------|--------|---------------|
| OpenAI | ✅ Adapter implemented | Pending certification run |
| Ollama | ✅ Adapter implemented | Pending certification run |
| NVIDIA NIM | ✅ Adapter implemented | Pending certification run |
| Azure OpenAI | 🟡 Stub | Planned |

### Phase 2: Native API Providers

| Provider | Status | Notes |
|----------|--------|-------|
| Anthropic | 🟡 Planned | Requires native Messages API mapping |
| Google Gemini | 🟡 Planned | Requires Gemini API format translation |
| Mistral | 🟡 Planned | OpenAI-compatible; mostly mapping work |
| Cerebras | 🟡 Planned | OpenAI-compatible; mostly mapping work |
| Groq | 🟡 Planned | OpenAI-compatible; mostly mapping work |

### Phase 3: Emerging Providers

| Provider | Status | Notes |
|----------|--------|-------|
| Amazon Bedrock | 🟡 Planned | Requires Bedrock runtime API mapping |
| Vertex AI | 🟡 Planned | Requires Google Cloud AI API mapping |
| Cohere | 🟡 Planned | Requires Cohere Chat API mapping |
| AI21 Labs | 🟡 Planned | Requires AI21 Jamba API mapping |
| Perplexity | 🟡 Planned | OpenAI-compatible endpoint |
| DeepSeek | 🟡 Planned | OpenAI-compatible endpoint |
| Qwen | 🟡 Planned | OpenAI-compatible endpoint |
| xAI Grok | 🟡 Planned | OpenAI-compatible endpoint |

### Phase 4: Regional & Gateway Providers

| Provider | Status | Notes |
|----------|--------|-------|
| Aliyun (Tongyi) | 🔲 Roadmap | Requires Alibaba Cloud API mapping |
| Cloudflare Gateway | 🔲 Roadmap | Proxy/gateway pattern, not direct API |
| Together AI | 🔲 Roadmap | OpenAI-compatible |
| Replicate | 🔲 Roadmap | Different streaming model (HTTP polling) |

---

## Files Created This Sprint

| File | Lines |
|------|-------|
| `docs/provider-compatibility/canonical-contract.md` | ~440 |
| `docs/provider-compatibility/compatibility-matrix.md` | ~200 |
| `docs/provider-compatibility/provider-scorecard.md` | ~180 |
| `docs/provider-compatibility/provider-capabilities.schema.yaml` | ~120 |
| `docs/provider-compatibility/providers/openai.md` | ~80 |
| `docs/provider-compatibility/providers/anthropic.md` | ~80 |
| `docs/provider-compatibility/providers/ollama.md` | ~80 |
| `docs/provider-compatibility/providers/nvidia-nim.md` | ~80 |
| `docs/provider-compatibility/providers/gemini.md` | ~80 |
| `docs/provider-compatibility/providers/mistral.md` | ~80 |
| `docs/provider-compatibility/providers/cerebras.md` | ~80 |
| `docs/provider-compatibility/providers/groq.md` | ~80 |
| `docs/provider-compatibility/providers/azure-openai.md` | ~80 |
| `docs/provider-compatibility/providers/bedrock.md` | ~80 |
| `docs/provider-compatibility/providers/vertex-ai.md` | ~80 |
| `docs/provider-compatibility/providers/cohere.md` | ~80 |
| `docs/provider-compatibility/providers/ai21.md` | ~80 |
| `docs/provider-compatibility/providers/perplexity.md` | ~80 |
| `docs/provider-compatibility/providers/deepseek.md` | ~80 |
| `docs/provider-compatibility/providers/qwen.md` | ~80 |
| `docs/provider-compatibility/providers/xai-grok.md` | ~80 |
| `tests/provider-certification/README.md` | ~350 |

---

## Conclusion

Sprint V2.3-A established the documentation and design foundation for Conductor's transformation into a full Provider Compatibility Layer:

- ✅ Canonical contract defined and documented
- ✅ Compatibility matrix created for 17 providers
- ✅ Provider scorecard framework established
- ✅ Certification suite design completed (17 scenarios, 5 levels)
- ✅ Provider onboarding workflow documented
- ✅ Future adapter roadmap outlined (4 phases)

The next sprint (V2.3-B) should focus on implementing Phase 2 native API adapters (Anthropic, Gemini, Mistral) and running the certification suite against all Phase 1 adapters.
