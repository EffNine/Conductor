# Novexa Gateway — Domain Glossary

This file defines the canonical domain language for Novexa Gateway. It contains no implementation details.

## Scope

Novexa Gateway is a single-operator, self-hosted AI API gateway. One operator owns one gateway API key. The gateway is configured with multiple upstream provider API keys/plans. Client tools (VS Code, Claude Code, Continue, Aider, Open WebUI, etc.) talk to the gateway using that one key and see a merged model picker drawn from all configured provider subscriptions.

## Dashboard Scope (MVP)

The management dashboard exposes:

- **Models** — the merged Model Catalog from all configured providers, optionally filtered by per-model online status.
- **Usage** — estimated cost and token/resource consumption, totals and per-provider/per-model breakdowns.
- **Health** — per-provider liveness and latency, plus optional per-model reachability (especially for providers like NVIDIA NIM where the catalog includes unreachable endpoints).
- **Logs** — recent request log entries for debugging.

## Canonical Terms

### Gateway
The single binary/server that exposes an OpenAI-compatible API to client tools and routes requests to upstream AI providers.

### Provider
An upstream AI service reachable by the gateway (e.g., OpenAI, Anthropic, Gemini, DeepSeek, OpenRouter, Groq, Ollama, LM Studio, a generic OpenAI-compatible endpoint).

### Provider Key
The API credential used by the gateway to authenticate with a provider. Stored in configuration, never exposed to clients.

### Model ID
The user-facing identifier that appears in client requests, aliases, and routes. Examples: `gpt-4o`, `claude-sonnet`, `fast`, `cheap`.

### Provider Model ID
The exact model identifier that must be sent to a provider's API. Examples: `gpt-4o-2024-08-06`, `claude-3-5-sonnet-20241022`.

A Model ID may resolve to a Provider Model ID that differs from it.

### Alias
A shortcut name that resolves to a Model ID. Example: `fast` → `gpt-4o-mini`. Aliases are for operator convenience in configuration and CLI usage; they are not advertised in the model list returned by `/v1/models`.

### Route
A configuration mapping a Model ID to a provider and an optional Provider Model ID override.

### Fallback / Fallback Chain
An ordered list of backup Route configurations tried when the primary route fails.

### Routing Resolution Rule
A requested Model ID or Alias is resolved by exact match against configured Aliases, then Routes. If no match exists, the request fails. The gateway does not automatically select among providers for an unmatched Model ID; explicit Routes or Fallbacks are required.

When a Model ID returned by `/v1/models` carries a provider prefix (e.g. `groq/llama3-8b`), the gateway strips the prefix before route lookup. The base Model ID (`llama3-8b`) must map to a Route for that provider. Provider prefixes are a serialization convention for the model list, not part of route configuration.

### Model Catalog
The set of models a provider reports as available for a given provider key. The gateway queries each configured provider for its catalog, merges the results, and exposes them through `/v1/models`.

When the same base model identifier is available from multiple providers, each offering is advertised as a distinct Model ID with the provider identifier as a prefix, e.g. `openai/llama3-8b` and `groq/llama3-8b`. The gateway recognizes the prefix during routing.

When model reachability probing is enabled, unreachable catalog entries may be omitted from `/v1/models` while remaining inspectable via the dashboard API.

### Model Reachability / Online Status
Whether a catalog Model ID currently accepts inference (typically a minimal chat completion). Provider-level health only proves the upstream API is up; it does not prove each listed model is callable. Probing is especially relevant for providers like NVIDIA NIM whose catalog mixes free hosted endpoints with unreachable or retired ones.

### Static Model List
A manually configured list of Model IDs advertised for a provider when the provider does not support a dynamic model catalog query (e.g., generic OpenAI-compatible endpoints or some local providers).

### Resolved Route
The concrete result of routing: a chosen provider plus the effective Provider Model ID to send upstream.

### Usage
A record of resources consumed, latency observed, provider used, and cost estimated for a single request.

The primary shape is OpenAI-compatible token usage: `prompt_tokens`, `completion_tokens`, and `total_tokens`. Providers that do not bill by tokens report zero or null for these fields and may populate additional counters such as `requests`, `duration_ms`, `input_chars`, or `output_chars`.

### Cost Rate
The price for a model/provider combination. The gateway estimates cost by applying the rate to the relevant usage counter (e.g., per token, per request, per minute, per character). All cost estimates are stored in USD; the gateway does not perform currency conversion.

Cost rates are primarily fetched from each provider's pricing API or published pricing data. When a provider returns exact usage cost in a response, that value is recorded and can override the estimate. Providers that expose neither pricing nor per-request cost fall back to manually configured rates or report cost as unknown.

### Plan / Subscription
The set of models, rate limits, and pricing associated with a provider key. The gateway does not manage provider subscriptions; it reflects them in configuration.

## Deprecated / Overloaded Terms

- **model** — do not use alone; use `Model ID` or `Provider Model ID`.
- **provider name** — prefer `provider` as an identifier string when unambiguous; use `provider_id` for keys in serialized formats.
