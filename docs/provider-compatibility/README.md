# Provider Compatibility

**Sprint V2.3-A** — Canonical contract and compatibility layer for upstream provider normalization.

## Overview

Conductor routes requests across multiple upstream AI providers (OpenAI, Anthropic, Ollama, NVIDIA NIM, etc.) through a unified gateway. This directory documents the **canonical contract** — Conductor's internal, provider-agnostic types that the compatibility layer normalizes to and from — and the evolving per-provider compatibility specs.

The canonical model is Conductor-native. It is **not** OpenAI's shape and **not** Anthropic's shape. Provider adapters translate their native formats into the canonical contract so routing, cost tracking, and the dashboard API work uniformly regardless of upstream.

## Directory Structure

```
docs/provider-compatibility/
├── README.md                   ← you are here
├── canonical-contract.md       ← Conductor's internal canonical types
├── openai.md                   ← OpenAI provider adapter spec
├── anthropic.md                ← Anthropic provider adapter spec
├── ollama.md                   ← Ollama provider adapter spec
├── nvidia-nim.md               ← NVIDIA NIM provider adapter spec
└── provider-interface.md       ← Go Provider interface reference
```

## How to Use These Docs

| Role | What to read |
|------|-------------|
| **Provider adapter implementer** | `canonical-contract.md` first, then your provider's spec |
| **Gateway consumer / API dev** | `canonical-contract.md` for the normalized types the gateway emits internally |
| **Dashboard developer** | `canonical-contract.md` — usage, cost, and error shapes are canonical |
| **Contributor adding a new provider** | Start with `provider-interface.md`, then `canonical-contract.md`, then an existing provider spec as a template |

## Status Indicators

Used throughout this directory and in per-provider specs:

| Symbol | Meaning |
|--------|---------|
| ✅ | Implemented |
| 🟡 | Planned |
| 🔲 | Stub (interface defined, no logic yet) |
| ❌ | Not Supported |
| ❓ | Unknown / needs verification |

## Cross-References

| Doc | Location |
|-----|----------|
| Provider interface (Go) | `internal/provider/interface.go` |
| Capabilities metadata | `internal/provider/metadata.go` |
| Configuration reference | `docs/configuration.md` |
| Health / probe behavior | `docs/configuration.md` (section `health.models`) |
| Making a request through Conductor | `README.md` — Quickstart |
| Usage & cost API | `README.md` — API reference (`/api/usage`, `/api/usage/costs`) |

## Current Status

| Provider | Chat | Stream | Embed | Vision | Tools | Reasoning | Status |
|----------|------|--------|-------|--------|-------|-----------|--------|
| OpenAI | ✅ | ✅ | ✅ | ✅ | 🟡 | 🟡 | Adapter done; tool/reasoning in progress |
| Anthropic | ✅ | ✅ | ❌ | ✅ | ✅ | ✅ | Adapter done |
| Ollama | ✅ | ✅ | 🟡 | ❌ | ❌ | ❌ | Basic chat/stream done |
| NVIDIA NIM | ✅ | 🟡 | ❌ | ❓ | ❌ | ❌ | Chat probe passing; stream planned |

*See each provider's spec for field-level detail.*
