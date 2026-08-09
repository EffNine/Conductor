# ADR-0005: Explainability

## Status
Accepted

## Context
Routing decisions are currently opaque. Operators need to understand:
- Why a provider was selected
- Why alternatives were rejected
- What signals influenced the decision
- What penalties were applied

## Decision
Introduce explainability contracts:
- `DecisionRationale` - Complete decision justification
- `CandidateRationale` - Per-candidate reasoning
- `SignalEntry` - Positive signals that influenced decision
- `PenaltyEntry` - Negative signals that reduced score

Also define:
- `Reason` enum for decision factors (health, latency, cost, capability, policy, etc.)
- `ExplainableDecision` interface for components that can explain themselves

## Alternatives Considered
1. Log all decisions - rejected as insufficient for structured analysis
2. Store in database - rejected as premature optimization
3. Real-time explanation API - rejected as out of scope

## Trade-offs
- **Pros**: Transparent decisions, debuggability, audit capability
- **Cons**: Additional data structures, requires explanation generation logic

## Future Implications
- Enables explainable routing (Sprint V2.2+)
- Supports audit compliance
- Facilitates debugging
- Allows decision analysis and optimization
