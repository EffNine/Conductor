# Conductor Decision Engine V2 — Architecture Design Document

## Executive Summary

Conductor has evolved beyond an AI gateway into a multi-dimensional **AI Decision Platform**.
The current "Auto Mode" (provider-centric, NVIDIA NIM-focused, a separate decision path) no longer
represents the system. This document designs the **Decision Engine V2**: a unified, policy-driven,
cross-provider intelligence layer that decides provider, key, model, inference parameters, caching,
streaming, retry, fallback, and (in the future) tool and MCP capability usage.

The user experience collapses to two fields:

```json
{ "provider": "conductor", "model": "auto" }
```

Conductor decides everything else.

---

## 1. Design Philosophy

### 1.1 First Principles

1. **The user must never make infrastructural decisions.** Provider, key, model, parameters, cache,
   stream, retry, and fallback are all Conductor's responsibility.
2. **Decision-making is separable from transport.** The Decision Engine is a pure function:
   `DecisionContext → Decision`. It does not care whether the upstream is OpenAI, Anthropic,
   Ollama, or a future provider.
3. **Policies are the public interface.** Operators express *intent* ("cheapest", "fastest",
   "coding", "privacy-first"), not weighted formulas. Policies are declarative; weights are internal.
4. **Runtime state is ephemeral but learnable.** Health, latency, quotas, and errors are transient.
   Patterns (cost trends, failure correlations) are durable. Both feed future decisions.
5. **Backward compatibility is non-negotiable.** `model = "auto"` continues to work. Existing
   routes, aliases, and fallbacks remain operational.

### 1.2 Guiding Metaphor

> Conductor is a **conductor** (orchestrator), not a **router** (switch).
> A router forwards; a conductor *decides*.

---

## 2. Architecture Overview

### 2.1 High-Level Component Diagram

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           Conductor Decision Engine V2                      │
│                                                                             │
│   ┌──────────┐    ┌──────────────┐    ┌──────────────┐    ┌─────────────┐  │
│   │  Auth     │    │  Request     │    │  Decision    │    │  Execution   │  │
│   │  Layer    │───→│  Context     │───→│  Engine      │───→│  Pipeline    │  │
│   │          │    │  Builder     │    │              │    │             │  │
│   └──────────┘    └──────────────┘    └──────────────┘    └─────────────┘  │
│                                              │              │               │
│                    ┌─────────────────────────┘              │               │
│                    │                                        │               │
│   ┌────────────────▼────────────────┐       ┌──────────────▼─────────────┐ │
│   │       Policy Engine              │       │     Provider Runtime       │ │
│   │  ┌────────────────────────────┐ │       │     State Manager          │ │
│   │  │ Preset Policies            │ │       │     ┌────────────────────┐ │ │
│   │  │  - Balanced                │ │       │     │ Key Vault          │ │ │
│   │  │  - Cheapest                │ │       │     │  - Multi-key       │ │ │
│   │  │  - Fastest                 │ │       │     │  - Rotation        │ │ │
│   │  │  - Highest Quality         │ │       │     │  - Quota tracking  │ │ │
│   │  │  - Prefer Local            │ │       │     └────────────────────┘ │ │
│   │  │  - Prefer Free Tier        │ │       │                            │ │
│   │  │  - Privacy First           │ │       │     ┌────────────────────┐ │ │
│   │  │  - Coding                  │ │       │     │ Provider Runtime   │ │ │
│   │  │  - Reasoning               │ │       │     │ State              │ │ │
│   │  │  - Vision                  │ │       │     │  - Health          │ │ │
│   │  │  - Custom / Enterprise     │ │       │     │  - Latency         │ │ │
│   │  │  - Offline First           │ │       │     │  - Rate limits     │ │ │
│   │  │                          │ │       │     │  - Quota / Credits   │ │ │
│   │  │ User-defined policies    │ │       │     │  - Failures          │ │ │
│   │  └────────────────────────────┘ │       │     │  - Load            │ │ │
│   └─────────────────────────────────┘       │     │  - Last success    │ │ │
│                                             │     │  - Last failure    │ │ │
│   ┌─────────────────────────────────────┐   │     │  - Availability    │ │ │
│   │    Decision Context                 │   │     └────────────────────┘ │ │
│   │  - Request intent                   │   │                            │ │
│   │  - Capability requirements          │   │   ┌────────────────────┐   │ │
│   │  - Cost constraints                 │   │   │  Learning          │   │ │
│   │  - Latency budget                   │   │   │  Feedback Loop     │   │ │
│   │  - Privacy / data residency         │   │   │  - Cost trends     │   │ │
│   │  - Historical signals               │   │   │  - Failure patterns│   │ │
│   │  - User overrides                   │   │   │  - Cache hit rates │   │ │
│   │  - Current rate limits              │   │   │  - User overrides  │   │ │
│   │                                     │   │   └────────────────────┘   │ │
│   └─────────────────────────────────────┘   │                            │ │
│                                              └────────────────────────────┘ │
│   ┌──────────────────────────────────────────────────────────────────────┐   │
│   │                    Learning & Observability Layer                     │   │
│   │  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐               │   │
│   │  │  Metrics     │  │  Cost        │  │  Usage       │               │   │
│   │  │  Collector   │  │  Estimator   │  │  Tracker     │               │   │
│   │  └──────────────┘  └──────────────┘  └──────────────┘               │   │
│   │  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐               │   │
│   │  │  Cache       │  │  Health      │  │  Circuit     │               │   │
│   │  │  Engine      │  │  Prober      │  │  Breaker     │               │   │
│   │  └──────────────┘  └──────────────┘  └──────────────┘               │   │
│   └──────────────────────────────────────────────────────────────────────┘   │
│                                                                             │
│   ┌──────────────────────────────────────────────────────────────────────┐   │
│   │                         Provider Layer                               │   │
│   │  ┌────────┐ ┌────────┐ ┌────────┐ ┌────────┐ ┌────────┐ ┌────────┐  │   │
│   │  │ OpenAI │ │ Anthropic│ │Gemini│ │DeepSeek│ │Ollama│ │LM Studio│  │   │
│   │  └────────┘ └────────┘ └────────┘ └────────┘ └────────┘ └────────┘  │   │
│   │  ┌────────┐ ┌────────┐ ┌────────┐ ┌────────┐ ┌────────┐             │   │
│   │  │OpenRouter│ │Groq  │ │xAI   │ │Generic │ │Plugin  │             │   │
│   │  └────────┘ └────────┘ └────────┘ └────────┘ └────────┘             │   │
│   └──────────────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 2.2 Responsibility Map

| Component | Responsibility |
|-----------|---------------|
| **Auth Layer** | Gateway API key validation, per-client quotas (future) |
| **Request Context Builder** | Assembles `DecisionContext` from request, config, and runtime state |
| **Policy Engine** | Resolves user/operational policy intent to scoring weights and constraints |
| **Decision Context** | Immutable snapshot of all signals available at decision time |
| **Decision Engine** | Core algorithm: filter → score → select → parameterize |
| **Provider Runtime State** | Ephemeral, per-request health data (latency, errors, quota) |
| **Key Vault** | Multi-key per provider, selection and rotation logic |
| **Parameter Optimizer** | Determines temperature, top_p, max_tokens, reasoning_effort, etc. |
| **Learning Feedback Loop** | Ingests outcomes; updates cost models, failure correlations, preferences |
| **Execution Pipeline** | Sends request, handles streaming, retries, fallbacks |
| **Cache Engine** | Request → response mapping; cache-key generation; eviction |
| **Health Prober** | Background reachability probes; error tracking |
| **Circuit Breaker** | Per-provider failure isolation |
| **Metrics / Usage / Cost** | Observability; persistence to SQLite |

---

## 3. Component Deep Dive

### 3.1 Decision Context (`DecisionContext`)

An immutable runtime snapshot. Created once per request.

```
DecisionContext {
    // --- Request-derived ---
    RequestID          string
    CorrelationID      string
    ModelHint          string          // "auto" or explicit model ID
    Messages           []Message
    Tools              []Tool          // optional, for capability matching
    StreamRequested    bool
    UserOverrides      UserOverrides   // explicit per-request hints

    // --- Intent signals ---
    TaskType           TaskType        // elite/coding/reasoning/vision/fast/default
    CapabilityHints    CapabilityHints // streaming, vision, reasoning, tools, ...
    Language           string          // detected from messages
    IsMultimodal       bool

    // --- Constraints ---
    MaxLatencyMs       int64           // user or policy budget
    MaxCostUSD         float64         // per-request cost ceiling
    MinQualityScore    float64         // policy floor
    PrivacyConstraint  PrivacyLevel    // none / anonymize / data-residency

    // --- Runtime state (read-only view) ---
    ProviderStates     map[string]ProviderRuntimeState
    KeyVaultSnapshot   KeyVaultSnapshot
    CacheSnapshot      CacheSnapshot
    CurrentRateLimits  map[string]RateLimitState

    // --- Historical signals ---
    CostHistory        CostHistory     // per-provider-model rolling averages
    LatencyHistory     LatencyHistory  // rolling averages
    FailureHistory     FailureHistory  // recent error patterns
    CacheHitRatio      float64         // overall and per-model

    // --- Policy resolution ---
    ActivePolicy       Policy          // resolved from preset or custom
    PolicyWeights      PolicyWeights   // normalized scoring weights
}
```

**Key design decision:** The context is *immutable* once created. This allows safe concurrent
use across scoring factors without locks.

### 3.2 Provider Runtime State (`ProviderRuntimeState`)

Separate from static `Metadata`. This is ephemeral, frequently updated data.

```
ProviderRuntimeState {
    // Health
    HealthState        State          // healthy / degraded / unhealthy / recovering / unknown
    Reachable          bool
    LastProbeAt        time.Time
    LastProbeLatencyMs int64
    ConsecutiveFails   int
    NextProbeTime      time.Time

    // Performance
    RollingLatencyMs   int64           // 64-sample window
    ErrorRate          float64         // rolling window (e.g., 5 min)
    LastSuccessAt      time.Time
    LastFailureAt      time.Time

    // Quota / Limits
    RateLimitRemaining int             // requests remaining in window
    RateLimitResetAt   time.Time
    QuotaRemaining     float64         // credits / tokens remaining
    QuotaResetAt       time.Time

    // Load
    ActiveRequests     int
    QueueDepth         int

    // Key-specific state (see Key Vault)
    ActiveKeyID        string
    KeyRotationScore   float64
}
```

**Storage:** In-memory with SQLite persistence for crash recovery. Probes and live request
outcomes update state; the state store serializes on checkpoint.

### 3.3 Key Vault (`KeyVault`)

Multi-key per provider with intelligent selection.

```
KeyVault {
    Keys map[string]ProviderKeyRing    // provider → key ring

    ProviderKeyRing {
        Entries []APIKeyEntry
        Strategy KeySelectionStrategy  // round-robin / quota-aware / cost-optimized / manual
    }

    APIKeyEntry {
        ID           string            // user-assigned label: "office", "personal", "backup"
        Key          EncryptedKey      // never logged
        Provider     string
        BaseURL      string            // optional per-key endpoint override
        Quota        QuotaConfig       // monthly/ daily / per-minute limits
        Priority     int               // lower = preferred
        Tags         []string          // "free", "paid", "office", "backup"
        Enabled      bool
        LastUsedAt   time.Time
        UsageCount   int64
        FailureCount int64
    }
}
```

**Selection Strategies:**
- `round_robin` — even distribution
- `quota_aware` — prefer keys with more remaining quota
- `cost_optimized` — prefer keys with lower effective cost
- `priority` — honor user-assigned priority
- `manual` — fixed key (admin-specified)

**Per-key metadata tracking:**
- Usage count and failure count per key
- Last success / failure timestamps
- Quota remaining (inferred from rate-limit headers or provider APIs)

### 3.4 Policy Engine (`PolicyEngine`)

Policies are declarative intent specifications. Each policy resolves to a set of scoring weights,
constraints, and behavioral flags.

```
Policy {
    ID            string             // "balanced", "cheapest", "coding", ...
    Name          string
    Description   string
    Category      PolicyCategory     // general / cost / performance / capability / privacy / custom

    // Scoring weights (internal; user sees preset names)
    Weights       PolicyWeights

    // Constraints
    Constraints   PolicyConstraints

    // Behavioral flags
    Flags         PolicyFlags
}

PolicyWeights {
    Health     float64
    Latency    float64
    Cost       float64
    Capability float64
    Quota      float64    // prefer providers with headroom
    Locality   float64    // prefer local / self-hosted
    Privacy    float64    // prefer data-resident providers
}

PolicyConstraints {
    MaxLatencyMs       int64
    MaxCostPerToken    float64
    MaxCostPerRequest  float64
    MinHealthScore     float64
    RequireStreaming   bool
    RequireVision      bool
    RequireTools       bool
    RequireReasoning   bool
    ExcludeProviders   []string
    RequireProviders   []string
    RequireCapability  []string
    DataResidency      []string  // ISO country codes
}

PolicyFlags {
    PreferLocal           bool   // offline-first / local providers first
    PreferFreeTier        bool   // zero-cost providers first
    CacheAggressive       bool   // higher cache TTL, broader cache keys
    StreamAlways          bool   // force streaming even when client didn't ask
    RetryOnRateLimit      bool   // exponential backoff on 429
    FallbackOnDegraded    bool   // failover when provider is degraded (not just unhealthy)
    TrackCostPerDecision  bool   // log cost as a decision signal
    LearningEnabled       bool   // feed outcomes back into future decisions
}
```

**Preset Policies:**

| Policy ID | Weights (H/L/C/Cap) | Flags | Use Case |
|-----------|---------------------|-------|----------|
| `balanced` | 40/25/15/20 | — | Default; good all-rounder |
| `cheapest` | 10/10/70/10 | PreferFreeTier | Minimize spend |
| `fastest` | 20/70/5/5 | CacheAggressive | Lowest latency |
| `highest_quality` | 30/15/10/45 | — | Best output quality |
| `prefer_local` | 20/30/20/10 | PreferLocal, OfflineFirst | Self-hosted first |
| `prefer_free` | 10/20/60/10 | PreferFreeTier | Zero-cost inference |
| `privacy_first` | 20/10/20/10 | Data residency, no third-party | GDPR / compliance |
| `coding` | 25/20/15/40 | — | Agentic coding tasks |
| `reasoning` | 30/15/10/45 | — | Complex reasoning |
| `vision` | 20/15/10/55 | RequireVision | Image understanding |
| `offline_first` | 10/40/20/10 | PreferLocal | No internet required |
| `custom` | user-defined | user-defined | Enterprise policies |

**Custom / Enterprise Policies:**
- Defined in `config.yaml` under `policies:`
- Can reference external policy definitions (future: JSON Schema served from a URL)
- Support conditional rules (e.g., "if request > 10K tokens, prefer local")

### 3.5 Decision Pipeline

The complete request flow, from HTTP to upstream response:

```
┌──────────────────────────────────────────────────────────────────────────────┐
│                           DECISION PIPELINE V2                               │
│                                                                              │
│  1. HTTP REQUEST                                                             │
│     POST /v1/chat/completions                                                │
│     { "model": "auto", "messages": [...] }                                   │
│                                                                              │
│  2. AUTH & VALIDATION                                                        │
│     - Gateway API key check                                                  │
│     - Payload validation                                                     │
│     - Rate limit pre-check (global)                                          │
│                                                                              │
│  3. DECISION CONTEXT BUILDING                                                │
│     ┌──────────────────────────────────────────────────────────────────┐     │
│     │ • Parse request fields                                           │     │
│     │ • Classify task intent (keyword heuristics + future ML)          │     │
│     │ • Detect capability hints (vision, tools, reasoning, streaming)  │     │
│     │ • Resolve user overrides (per-request model hints, etc.)         │     │
│     │ • Snapshot current provider states from runtime store            │     │
│     │ • Snapshot key vault state                                       │     │
│     │ • Load cost / latency / failure history from SQLite              │     │
│     │ • Compute cache key if applicable                              │     │
│     └──────────────────────────────────────────────────────────────────┘     │
│                                                                              │
│  4. CACHE CHECK (non-streaming only)                                         │
│     - Build deterministic cache key from (model, messages, params)           │
│     - If hit: return cached response, record metrics                         │
│     - If miss: continue to provider discovery                                │
│                                                                              │
│  5. PROVIDER DISCOVERY                                                       │
│     ┌──────────────────────────────────────────────────────────────────┐     │
│     │ • Find all providers supporting the requested model capability   │     │
│     │ • Filter by policy constraints (excluded providers, etc.)        │     │
│     │ • Filter by data residency requirements                          │     │
│     │ • Include local providers if policy allows                       │     │
│     │ • Build candidate list with full runtime state snapshot          │     │
│     └──────────────────────────────────────────────────────────────────┘     │
│                                                                              │
│  6. RUNTIME FILTER                                                           │
│     ┌──────────────────────────────────────────────────────────────────┐     │
│     │ • Remove providers with open circuit breakers                    │     │
│     │ • Remove providers below health floor                            │     │
│     │ • Remove providers exceeding cost ceiling                        │     │
│     │ • Remove providers at rate limit                                 │     │
│     │ • Remove providers with exhausted quota                          │     │
│     │ • Apply policy-specific exclusions                               │     │
│     └──────────────────────────────────────────────────────────────────┘     │
│                                                                              │
│  7. KEY SELECTION                                                            │
│     ┌──────────────────────────────────────────────────────────────────┐     │
│     │ • For each surviving provider, select the best API key           │     │
│     │ • Strategy: quota-aware > cost-optimized > round-robin           │     │
│     │ • Log key selection for audit                                    │     │
│     └──────────────────────────────────────────────────────────────────┘     │
│                                                                              │
│  8. POLICY EVALUATION & SCORING                                              │
│     ┌──────────────────────────────────────────────────────────────────┐     │
│     │ • Load active policy weights                                     │     │
│     │ • For each candidate, compute:                                   │     │
│     │     - Health score (from runtime state)                          │     │
│     │     - Latency score (from rolling average)                       │     │
│     │     - Cost score (from pricing + historical cost)                │     │
│     │     - Capability score (from request intent vs provider caps)    │     │
│     │     - Quota score (from remaining quota)                         │     │
│     │     - Locality score (local > cloud)                             │     │
│     │     - Privacy score (data residency match)                       │     │
│     │ • Apply policy constraints as hard filters or soft penalties     │     │
│     │ • Compute weighted composite score                               │     │
│     └──────────────────────────────────────────────────────────────────┘     │
│                                                                              │
│  9. MODEL SELECTION                                                          │
│     ┌──────────────────────────────────────────────────────────────────┐     │
│     │ • If model is "auto", select best model from provider catalog    │     │
│     │ • Score models by: health, cost, latency, capability match       │     │
│     │ • Apply task-specific model allowlists from policy profiles      │     │
│     │ • Return (provider, key, model) triple                           │     │
│     └──────────────────────────────────────────────────────────────────┘     │
│                                                                              │
│  10. PARAMETER OPTIMIZATION                                                  │
│     ┌──────────────────────────────────────────────────────────────────┐     │
│     │ • Determine optimal inference parameters based on policy:        │     │
│     │     - temperature (lower for coding, higher for creative)        │     │
│     │     - top_p (tighter for reasoning, looser for casual)           │     │
│     │     - max_tokens (budget-aware; respect context window)          │     │
│     │     - reasoning_effort (high for reasoning, low for fast)        │     │
│     │     - thinking_budget (for models supporting it)                 │     │
│     │     - stream (policy flag or client preference)                  │     │
│     │ • Apply client overrides (user-specified values take precedence) │     │
│     └──────────────────────────────────────────────────────────────────┘     │
│                                                                              │
│  11. EXECUTION                                                               │
│     ┌──────────────────────────────────────────────────────────────────┐     │
│     │ • Send request to selected provider with selected key            │     │
│     │ • Stream or collect response                                     │
│     │ • Record success/failure, latency, tokens, cost                  │
│     │ • Update runtime state (latency, health, quota)                  │
│     │ • Update key vault usage counters                                │
│     │ • On failure: trigger retry / fallback logic                     │
│     └──────────────────────────────────────────────────────────────────┘     │
│                                                                              │
│  12. LEARNING FEEDBACK                                                       │
│     ┌──────────────────────────────────────────────────────────────────┐     │
│     │ • Persist usage record to SQLite                                 │     │
│     │ • Update rolling cost / latency averages                         │     │
│     │ • Record failure patterns                                        │
│     │ • Update cache statistics                                        │
│     │ • (Future) Retrain policy weights from historical outcomes       │     │
│     └──────────────────────────────────────────────────────────────────┘     │
│                                                                              │
│  13. RESPONSE                                                                │
│     ┌──────────────────────────────────────────────────────────────────┐     │
│     │ • Return OpenAI-compatible response                              │     │
│     │ • Include Conductor decision metadata (optional X-Conductor-*)    │     │
│     └──────────────────────────────────────────────────────────────────┘     │
└──────────────────────────────────────────────────────────────────────────────┘
```

### 3.6 Parameter Optimizer (`ParameterOptimizer`)

Decides inference parameters based on policy, task type, and request characteristics.

```
ParameterOptimizer {
    // Input: DecisionContext
    // Output: resolved ChatCompletionRequest with optimized params

    ResolveParameters(ctx DecisionContext) OptimizedParameters {
        return {
            Temperature:       resolveTemperature(ctx),
            TopP:              resolveTopP(ctx),
            MaxTokens:         resolveMaxTokens(ctx),
            ReasoningEffort:   resolveReasoningEffort(ctx),
            ThinkingBudget:    resolveThinkingBudget(ctx),
            Stream:            resolveStreaming(ctx),
            PresencePenalty:   resolvePresencePenalty(ctx),
            FrequencyPenalty:  resolveFrequencyPenalty(ctx),
        }
    }
}
```

**Parameter Resolution Rules:**

| Parameter | Default | Coding | Reasoning | Vision | Fast | Creative |
|-----------|---------|--------|-----------|--------|------|----------|
| `temperature` | 0.7 | 0.2 | 0.3 | 0.1 | 0.8 | 0.9 |
| `top_p` | 0.9 | 0.95 | 0.9 | 1.0 | 1.0 | 0.95 |
| `max_tokens` | dynamic | context-aware | 4096 | 2048 | 256 | 4096 |
| `reasoning_effort` | medium | low | high | — | low | medium |
| `thinking_budget` | auto | none | full | none | none | medium |
| `stream` | client pref | true | true | true | false | true |

**Dynamic `max_tokens`:**
- Calculated from context window minus prompt tokens minus safety margin
- Capped by policy `MaxTokens` ceiling
- Respects provider-specific limits from metadata

### 3.7 Provider Discovery

**How providers participate:**
- Each provider implements the `Provider` interface (already exists)
- Providers register with the `Registry` at startup
- The Decision Engine queries the registry for capability-matching providers
- Providers self-report capabilities via `GetMetadata()` → `Capabilities` struct

**Plugin providers:**
- Any type implementing `ProviderPlugin` can be loaded at runtime
- Plugin factory receives config (API key, base URL, timeout) and returns a `Plugin`
- Plugins are discovered via:
  - Static registration (compiled-in providers: OpenAI, Anthropic, Gemini, etc.)
  - Dynamic loading (future: plugin DLLs / WASM modules)
  - Generic adapter (`generic` provider) for any OpenAI-compatible endpoint

**Local providers:**
- Ollama and LM Studio are treated as first-class providers
- They participate in scoring with a `Locality` bonus
- Local providers are skipped during health probes when the gateway runs remotely (existing behavior)
- Policy `PreferLocal` elevates local providers in scoring

**Provider-specific Auto modes (OpenRouter Auto, etc.):**
- Existing per-provider auto-modes (e.g., `nvidia_nim.auto`) are **deprecated** in V2
- The unified Decision Engine replaces all per-provider auto logic
- Migration path: existing auto configs are imported as policy constraints during V1→V2 transition

### 3.8 Learning Signals

Signals that feed back into future decisions:

| Signal | Source | Storage | Usage |
|--------|--------|---------|-------|
| Latency (per provider-model) | Request completion | Rolling window (64 samples) + SQLite | Latency scoring, SLA monitoring |
| Failures (per provider-model-key) | Request error | SQLite + in-memory counter | Circuit breaker, health score |
| Retries (per provider) | Fallback trigger count | SQLite | Retry policy tuning |
| Costs (per provider-model-key) | Actual + estimated | SQLite | Cost scoring, budget alerts |
| User overrides | Explicit per-request hints | SQLite | Preference learning |
| Success rate (rolling) | Request outcome | In-memory rate counter | Health score |
| Rate limit hits | 429 responses | SQLite | Quota-aware key selection |
| Cache hit ratio | Cache engine metrics | In-memory + SQLite | Cache policy tuning |
| Context length utilization | Token counts | SQLite | Max token optimization |
| Task classification accuracy | Keyword classifier results | In-memory | Classifier tuning (future: ML) |

**Learning mechanisms:**
1. **Short-term (in-memory):** Rolling averages, recent error rates, live quota tracking
2. **Medium-term (SQLite):** Daily / weekly cost trends, failure correlation patterns
3. **Long-term (future):** Model-driven weight adjustment based on historical success

---

## 4. Data Flow

### 4.1 Request-to-Response Flow

```
Client                          Conductor                          Upstream Provider
  │                                │                                      │
  │  POST /v1/chat/completions     │                                      │
  │  { model: "auto", ... }        │                                      │
  │───────────────────────────────→│                                      │
  │                                │  1. Auth check                       │
  │                                │  2. Build DecisionContext            │
  │                                │  3. Policy resolution                │
  │                                │  4. Provider discovery               │
  │                                │  5. Scoring & selection              │
  │                                │  6. Parameter optimization           │
  │                                │  7. Key selection                    │
  │                                │                                      │
  │                                │  POST /chat/completions              │
  │                                │  { model: "gpt-4o", ... }            │
  │                                │─────────────────────────────────────→│
  │                                │                                      │
  │                                │  ←── response ───────────────────────│
  │                                │  8. Normalize response               │
  │                                │  9. Update runtime state             │
  │                                │ 10. Record usage / cost              │
  │                                │ 11. Cache result (if applicable)     │
  │                                │                                      │
  │  ←── SSE stream / JSON ──────────────────────────────────────────────│
  │                                │                                      │
```

### 4.2 Decision Pipeline Data Structures

```
DecisionPipeline {
    Context     DecisionContext     // input: all signals
    Policy      Policy              // resolved policy
    Candidates  []Candidate         // filtered provider-model-key triples
    Decision    Decision            // final selection
    Outcome     Outcome              // execution result
}

Candidate {
    ProviderName    string
    ProviderModelID string
    KeyID           string          // selected key from vault
    RuntimeState    ProviderRuntimeState
    Score           float64          // composite score
    Breakdown       ScoreBreakdown   // per-factor scores
}

ScoreBreakdown {
    Health     float64
    Latency    float64
    Cost       float64
    Capability float64
    Quota      float64
    Locality   float64
    Privacy    float64
}

Decision {
    ProviderName      string
    ProviderModelID   string
    KeyID             string
    Parameters        OptimizedParameters
    CacheKey          string          // if caching is applicable
    Rationale         string          // human-readable explanation
    Scores            map[string]float64  // all candidate scores
}

Outcome {
    Success       bool
    LatencyMs     int64
    Tokens        TokenUsage
    CostUSD       *float64
    CacheHit      bool
    FallbackUsed  bool
    FallbackDepth int               // how many fallbacks were tried
    Error         *ProviderError
}
```

---

## 5. Interfaces

### 5.1 Decision Engine Interface

```go
//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -generate

// DecisionEngine is the core decision-making interface.
type DecisionEngine interface {
    // Decide makes a provider/model/key/parameter decision for a request.
    Decide(ctx context.Context, req *ChatCompletionRequest) (*Decision, error)

    // DecideEmbeddings makes a decision for an embeddings request.
    DecideEmbeddings(ctx context.Context, req *EmbeddingRequest) (*Decision, error)

    // ResolvePolicy resolves a policy name to a Policy object.
    ResolvePolicy(policyName string) (*Policy, error)

    // ListPolicies returns all available policies.
    ListPolicies() []PolicySummary
}
```

### 5.2 Provider Interface (extended)

The existing `Provider` interface remains. New optional interfaces for V2:

```go
// QuotaProvider is an optional interface for providers that expose quota info.
type QuotaProvider interface {
    GetQuota(ctx context.Context) (QuotaInfo, error)
}

type QuotaInfo struct {
    Limit          int64
    Remaining      int64
    ResetAt        time.Time
    UsagePercentage float64
}

// KeyManager is an optional interface for providers with multiple keys.
type KeyManager interface {
    ListKeys() []KeyInfo
    RotateKey(newKey string) error
}

type KeyInfo struct {
    ID       string
    Label    string
    Priority int
    Enabled  bool
}
```

### 5.3 Key Vault Interface

```go
type KeyVault interface {
    // SelectKey chooses the best key for a provider given the context.
    SelectKey(ctx context.Context, providerName string, ctxHint DecisionContext) (*APIKeyEntry, error)

    // RecordUsage records a successful or failed use of a key.
    RecordUsage(providerName string, keyID string, success bool, latencyMs int64)

    // GetSnapshot returns a read-only snapshot of all keys.
    GetSnapshot() map[string][]*APIKeyEntry
}
```

### 5.4 Policy Engine Interface

```go
type PolicyEngine interface {
    // Resolve returns the resolved policy for a name.
    Resolve(name string) (*Policy, error)

    // ResolveForTask returns the best policy for a task type.
    ResolveForTask(taskType TaskType) (*Policy, error)

    // Evaluate returns the scoring weights for a policy.
    Evaluate(policyName string) (PolicyWeights, error)

    // List returns all available policies.
    List() []PolicySummary
}

type PolicySummary struct {
    ID          string
    Name        string
    Category    string
    Description string
}
```

### 5.5 Parameter Optimizer Interface

```go
type ParameterOptimizer interface {
    // Optimize determines inference parameters from context.
    Optimize(ctx DecisionContext) (OptimizedParameters, error)
}

type OptimizedParameters struct {
    Temperature      float32
    TopP             float32
    MaxTokens        int
    ReasoningEffort  string   // "low", "medium", "high"
    ThinkingBudget   int      // 0 = auto
    Stream           bool
    PresencePenalty  float32
    FrequencyPenalty float32
}
```

---

## 6. Component Diagram — Module Layout

```
internal/
├── decision/
│   ├── engine.go              # Core DecisionEngine implementation
│   ├── context.go             # DecisionContext builder
│   ├── policy.go              # Policy resolution and evaluation
│   ├── scorer.go              # Composite scoring (extends router/scorer.go)
│   ├── parameter_optimizer.go # Inference parameter optimization
│   └── decision.go            # Decision and Candidate types
│
├── keyvault/
│   ├── vault.go               # KeyVault implementation
│   ├── key_ring.go            # Per-provider key ring
│   └── selection.go           # Key selection strategies
│
├── runtime/
│   ├── state.go               # ProviderRuntimeState
│   ├── store.go               # In-memory state store
│   └── checkpoint.go          # SQLite persistence for state
│
├── learning/
│   ├── feedback.go            # Learning feedback loop
│   ├── cost_model.go          # Cost trend analysis
│   └── preference.go          # User preference learning
│
├── router/                    # Existing (enhanced)
│   ├── router.go
│   ├── scorer.go
│   ├── selection.go
│   └── capability.go
│
├── automode/                  # Deprecated; kept for backward compat
│   ├── selector.go
│   ├── classifier.go
│   └── defaults.go
│
├── provider/                  # Existing (extended with optional interfaces)
│   ├── interface.go
│   ├── registry.go
│   ├── metadata.go
│   └── plugin.go
│
├── health/                    # Existing
│   ├── state.go
│   └── ...
│
├── cache/                     # Existing (enhanced cache-key logic)
│   ├── engine.go
│   └── hash.go
│
├── breaker/                   # Existing
│   └── breaker.go
│
├── usage/                     # Existing (extended with learning signals)
│   ├── tracker.go
│   └── estimator.go
│
└── handler/                   # Existing (wire DecisionEngine)
    └── handler.go
```

---

## 7. Migration Strategy

### 7.1 Compatibility Guarantees

1. `model = "auto"` continues to work exactly as before (routes to NVIDIA NIM auto mode)
2. All existing `routes`, `aliases`, and `fallbacks` configurations continue to function
3. `GET /v1/models` returns the same catalog (provider-prefixed IDs unchanged)
4. Dashboard API endpoints remain at the same paths
5. `config.yaml` schema is backward compatible

### 7.2 Phased Rollout

**Phase 1 — Coexistence (no breaking changes):**
- Decision Engine runs alongside existing Auto Mode
- `model = "auto"` still uses the old Auto Mode path
- New endpoint `POST /v1/chat/completions` with `model = "auto-v2"` uses the new engine
- Policy engine is registered but not yet active

**Phase 2 — opt-in V2:**
- Users add `routing.engine: "v2"` to config to enable the new Decision Engine
- `model = "auto-v2"` activates the engine
- All existing behavior preserved for `model = "auto"`

**Phase 3 — V2 as default:**
- `model = "auto"` routes through the Decision Engine
- Old Auto Mode is deprecated (logs a warning)
- `model = "auto-legacy"` preserves old behavior for migration testing

**Phase 4 — V1 removal:**
- Old Auto Mode code removed
- `model = "auto"` exclusively uses V2 engine

### 7.3 Configuration Migration

Existing config keys map to V2 concepts:

```yaml
# OLD (V1)
providers:
  nvidia_nim:
    auto:
      enabled: true
      provider: nvidia_nim
      weights:
        reachability: 10
        cost: 3
        latency: 1

# NEW (V2)
routing:
  engine: v2
  policy: balanced           # or "fastest", "cheapest", "coding", etc.
  weights:                   # override defaults per policy
    health: 40
    latency: 25
    cost: 15
    capability: 20

providers:
  nvidia_nim:
    # auto mode removed; handled by unified engine
```

### 7.4 API Key Migration

Existing single-key-per-provider config continues to work. New multi-key support is opt-in:

```yaml
# V2 multi-key example
providers:
  anthropic:
    keys:
      - id: office
        api_key: ${ANTHROPIC_OFFICE_KEY}
        tags: [paid, office]
      - id: personal
        api_key: ${ANTHROPIC_PERSONAL_KEY}
        tags: [paid, personal]
      - id: backup
        api_key: ${ANTHROPIC_BACKUP_KEY}
        tags: [backup]
    key_selection: quota_aware
```

---

## 8. Risks & Mitigations

| Risk | Severity | Mitigation |
|------|----------|------------|
| Decision latency adds overhead | Medium | Scoring is O(n) over providers; cached state avoids DB hits; target < 5ms decision time |
| Incorrect task classification | Medium | Keyword heuristics are transparent; user overrides available; future ML model |
| Policy misconfiguration breaks routing | High | Default policies are safe; config validation catches invalid references |
| Key vault rotation fails silently | Medium | Logging every key selection; health checks per key |
| Learning signals degrade performance | Low | Learning is opt-in; feedback loop is decoupled from request path |
| Multi-key quota tracking is inaccurate | Medium | Quota is inferred from rate-limit headers; best-effort, not authoritative |
| Backward compatibility regression | High | Comprehensive test suite; migration phase with dual-path support |
| SQLite state persistence failure | Low | In-memory state is authoritative; SQLite is for crash recovery only |
| Plugin loading breaks sandbox | Medium | Plugins are Go interfaces, not dynamic code; no sandbox needed |
| Cost estimation errors | Low | Actual cost from provider takes precedence; estimates are clearly labeled |

---

## 9. Deployment Architecture

### 9.1 Production Deployment Model

```
                    ┌─────────────────────┐
                    │   GitHub Repository  │
                    │   (Conductor V2)     │
                    └──────────┬──────────┘
                               │
                    ┌──────────▼──────────┐
                    │     CI Pipeline      │
                    │  - go test ./...    │
                    │  - golangci-lint    │
                    │  - docker build     │
                    │  - push to GHCR     │
                    └──────────┬──────────┘
                               │
                    ┌──────────▼──────────┐
                    │    GHCR Image        │
                    │  effnine/conductor   │
                    │  :v2.x.x            │
                    └──────────┬──────────┘
                               │
              ┌────────────────┼────────────────┐
              │                │                │
    ┌─────────▼──────┐ ┌──────▼───────┐ ┌─────▼───────┐
    │   Development  │ │   Staging    │ │  Production  │
    │   (local /    │ │   (Fly.io    │ │  (Fly.io     │
    │    Docker     │ │    prod-1x)  │ │    prod-2x)  │
    │    Compose)   │ │              │ │              │
    └────────────────┘ └──────────────┘ └──────────────┘
```

### 9.2 Environment Details

| Environment | Platform | Instance | Data | Purpose |
|-------------|----------|----------|------|---------|
| Development | Docker Compose | 1× shared-cpu | Local SQLite | Feature development |
| Staging | Fly.io | 1× shared-cpu-1x | Fly volume | Pre-production validation |
| Production | Fly.io | 2× performance-2x | Fly volume (replicated) | Live traffic |

### 9.3 Fly.io Configuration (V2)

```toml
app = 'conductor-v2'
primary_region = 'sin'

[build]
  dockerfile = 'deployments/Dockerfile'

[env]
  CONDUCTOR_ROUTING_ENGINE = 'v2'
  CONDUCTOR_ROUTING_POLICY = 'balanced'
  # Existing keys continue to work
  OPENAI_API_KEY = '${OPENAI_API_KEY}'
  ANTHROPIC_API_KEY = '${ANTHROPIC_API_KEY}'
  # New multi-key support
  ANTHROPIC_KEYS_OFFICE = '${ANTHROPIC_OFFICE_KEY}'
  ANTHROPIC_KEYS_BACKUP = '${ANTHROPIC_BACKUP_KEY}'

[[mounts]]
  source = 'conductor_data'
  destination = '/app/data'

[http_service]
  internal_port = 8080
  force_https = true
  auto_stop_machines = 'stop'
  auto_start_machines = true
  min_machines_running = 2    # increased for production HA
  processes = ['app']

  [[http_service.checks]]
    interval = '15s'
    timeout = '5s'
    grace_period = '30s'
    method = 'GET'
    path = '/health'

[vm]
  size = 'performance-2x'
  memory = '2048mb'
```

### 9.4 Docker Image

```dockerfile
# Identical structure to current; only binary changes
FROM golang:1.21-alpine AS builder
RUN apk add --no-cache git ca-certificates gcc musl-dev
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 CGO_CFLAGS="-D_LARGEFILE64_SOURCE" GOOS=linux \
    go build -ldflags="-s -w" -o /app/conductor ./cmd/conductor

FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata sqlite su-exec wget libgcc
RUN adduser -D -g '' conductor
WORKDIR /app
COPY --from=builder /app/conductor .
COPY deployments/docker-entrypoint.sh /docker-entrypoint.sh
RUN chmod +x /docker-entrypoint.sh \
 && mkdir -p /app/data \
 && chown -R conductor:conductor /app/data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=10s --start-period=10s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://127.0.0.1:8080/health || exit 1
ENTRYPOINT ["/docker-entrypoint.sh"]
```

---

## 10. Future Extensions

### 10.1 Tool Usage (Phase 2)

- Decision Engine will select which provider's tool-calling capability to use
- Tool definitions passed through to the selected provider
- Multi-tool orchestration across providers (future)

### 10.2 MCP Capability (Phase 2)

- Model Context Protocol support as a provider-agnostic interface
- Decision Engine routes MCP requests to capable providers
- MCP server discovery and selection

### 10.3 ML-Driven Classification (Phase 3)

- Replace keyword heuristics with a small on-device ML model
- Fine-tuned on historical request patterns
- Optional: cloud-based classification for higher accuracy

### 10.4 Multi-Tenancy (Phase 3)

- Per-client API keys with independent policies
- Quota enforcement per client
- Usage billing integration

### 10.5 A/B Policy Testing (Phase 3)

- Run two policies in parallel on shadow traffic
- Compare outcomes (latency, cost, quality)
- Automated policy selection based on experiment results

### 10.6 Cross-Region Deployment (Phase 4)

- Geo-distributed Decision Engines
- Region-aware provider selection (data residency)
- Latency-optimized routing across regions

---

## 11. Trade-offs

| Decision | Trade-off | Rationale |
|----------|-----------|-----------|
| Immutable DecisionContext | Slightly more memory per request | Thread-safe, no locks, easier to debug |
| In-memory runtime state + SQLite persistence | State lost on crash (minor) | Fast reads; SQLite for recovery, not primary store |
| Keyword-based task classification | Lower accuracy than ML | Zero dependencies, instant, explainable |
| Policy presets over raw weights | Less flexibility for experts | Most users want intent, not formulas |
| Multi-key per provider | More config complexity | Operators already manage multiple keys; V2 makes it explicit |
| Decision Engine as separate module | Additional indirection | Clean separation; testable in isolation; swappable |
| Backward-compatible migration (dual path) | Extra code during transition | Zero downtime migration; users opt in |
| SQLite for all persistence | Single-point-of-failure | Simple deployment; no external dependencies; adequate for single-operator use case |

---

## 12. Summary

Conductor Decision Engine V2 transforms Conductor from an AI gateway into an **AI decision platform**.
The core insight is simple: *every infrastructural decision should be Conductor's responsibility*.
The user writes `model = "auto"` and gets the best possible inference experience — provider, key,
model, parameters, cache, stream, retry, and fallback all decided automatically based on
declarative policy intent.

The architecture is:
- **Modular**: Decision Engine, Key Vault, Policy Engine, Parameter Optimizer are separate, testable components
- **Backward-compatible**: Existing `model = "auto"` continues to work; V2 is opt-in then default
- **Extensible**: Plugin providers, custom policies, and future ML classification are first-class
- **Observable**: Every decision is logged with rationale; learning signals feed back continuously
- **Deployable**: Same Docker/Fly.io model; no new infrastructure required

---

*Document version: 1.0*
*Scope: Architecture design only — no implementation*
