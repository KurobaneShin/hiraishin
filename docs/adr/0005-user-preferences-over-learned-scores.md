# 0005. User preferences always override learned scores

Date: 2026-11-21
Status: Accepted

## Context

Opaque auto-routers erode trust when they quietly send critical architecture work to a weaker model or burn a scarce subscription against the user's intent.

## Decision

Separate scheduling into hard user-defined policy gates (allowed list, deny list, minimum quality tier, pins, concurrency caps, metered opt-in) and weighted scoring, where optional per-(provider, role) learning is clamped to a small bounded delta (`max_delta`), can never violate a hard gate, and can be frozen or reset at any time.

## Consequences

The scheduler can adapt to empirical reliability differences inside a user's approved provider pool without ever surprising the operator. Every choice produces a deterministic, inspectable `why` breakdown showing exact weights and learned deltas.
