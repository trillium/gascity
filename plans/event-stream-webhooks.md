# Event Stream & Webhook Ingestion

> **Updated:** 2026-07-06 (task-h5q architecture pass). The original stub's open questions are answered below from live probes and repo survey. Net finding: **most of this already exists** — gascity has a typed event bus with an SSE wire, and Tailscale Funnel is already on. The only missing piece is a webhook ingress connector.

## Problem

Agents need external events (GitHub comments, CI results, appointment confirmations) as context, but there's no path for external services to push events in. Everything today is pull-based or manual; no polling connector exists yet.

## What already exists (verified 2026-07-06)

- **Event bus + agent subscription: SOLVED.** `internal/events` is a typed event bus; payloads implementing `events.Payload` flow through the central registry and emerge on the typed **`/v0/events/stream`** wire with named schemas (see `internal/extmsg/events.go` for the pattern). Agents subscribe by event type. No Redis, no new queue — the answer to "what substrate" is *the bus that's already there*, persisted by `events.FileRecorder` as an append-only JSONL log with size/interval rotation and gzip archival (`internal/events/recorder.go`, `internal/events/rotation.go`, read back via `internal/events/reader.go`).
- **Conversational routing: SOLVED.** `internal/extmsg` is the external-message fabric (adapters, bindings, delivery, per-conversation session ownership) — it defines a `TransportAdapter` interface plus a registry (`adapter_registry.go`) and ships a generic HTTP adapter (`http_adapter.go`); concrete per-provider connectors are supplied by consumers. Reply-able external sources belong here; fire-and-forget events belong on the bus directly.
- **Inbound exposure: SOLVED.** Tailscale Funnel is **already enabled and serving** on this machine (`tailscale funnel status` → `https://macbook.hippo-tilapia.ts.net`). External webhooks can POST to a funneled port today. Outbound was never blocked — Tailscale nodes have ordinary egress.
- **No existing poller to retire.** A repo survey (2026-07-06) found no GitHub PR-polling connector under `internal/config/` or elsewhere in this repo today; the "retire the poller" framing in the original stub does not apply until such a connector exists.

## The gap: webhook ingress

External service → public HTTPS (Funnel) → **[missing: verify + normalize + emit]** → events bus → `/v0/events/stream` → agents.

Design:

- An ingress HTTP service exposing `POST /hooks/{provider}` (e.g. `/hooks/github`, `/hooks/vercel`), funneled.
- Per-provider verification at the edge: GitHub `X-Hub-Signature-256` HMAC, Vercel signature header; unknown provider or bad signature → 401, never reaches the bus. Secrets via the existing config layer, never in code.
- Normalization to a typed payload, e.g. `events.WebhookInbound` (`webhook.inbound`) carrying `{provider, event_kind, delivery_id, actor, subject, payload_digest}` — the bus event kept small (targeted context, not dumps). `raw_ref` (a pointer to the persisted full raw body) is aspirational: `InboundEventPayload` ships `payload_digest` only today; no raw-body store exists yet (task-h5q, step 2).
- Idempotency on provider delivery IDs (GitHub `X-GitHub-Delivery`) — webhooks redeliver; the ingress dedupes.
- Reply-able providers (a GitHub PR comment an agent might answer) can additionally bridge into extmsg as an adapter later; v1 emits bus events only.

## Next steps (builder-actionable, in order)

1. **Done (task-h5q).** `WebhookInbound` (`webhook.inbound`) is defined in `internal/events/events.go`; `internal/webhookd.InboundEventPayload` implements the schema and registers via `events.RegisterPayload` in `internal/webhookd/events.go`. It's deliberately absent from `events.KnownEventTypes` until step 3 wires the package into `internal/api` (see the comment at the constant's declaration).
2. **Done (task-h5q).** `internal/webhookd` exists: `POST /hooks/{provider}` handler (`handler.go`), `Verifier`/`GitHubVerifier` doing GitHub HMAC-SHA256 over the raw body via `X-Hub-Signature-256` (`verify.go`), and a bounded TTL `DeliveryDedup` cache keyed on `X-GitHub-Delivery` (`dedup.go`). Table-driven tests cover valid/invalid/missing signatures and duplicate-delivery no-ops (`handler_test.go`, `verify_test.go`, `dedup_test.go`). The package is self-contained and dependency-injected (`EmitEventFunc`) but not yet mounted on any live mux — that's step 3.
3. Wire exposure: mount `webhookd.Handler` on a live port, add its config to `internal/config.City`'s TOML schema, add `WebhookInbound` to `events.KnownEventTypes` plus the Huma/SSE typed-envelope union in `internal/api`, `tailscale funnel <port>` route, document the public URL + GitHub webhook setup (repo settings → webhook → secret) in the service doc.
4. Point one real GitHub repo's webhook at it; verify an issue comment lands on `/v0/events/stream` end-to-end.
5. If a GitHub PR-polling connector exists by then, retire it for repos covered by webhooks (same PR as step 4 or immediately after — gate-or-delete). None exists in this repo as of this writing.
6. Consumer subscription: have an overseer agent consume `webhook.inbound` filtered by provider/event_kind. This is pack configuration, not SDK code — per the ZERO-hardcoded-roles rule in `AGENTS.md`, no role name may appear in Go, so the subscription is expressed in a pack's agent definition (e.g. `examples/gastown/packs/gastown/agents/mayor/agent.toml` and its `prompt.template.md`) reading the typed `/v0/events/stream` wire. There is no SDK-level architecture doc for this and there should not be one.

## Answered questions (from the original stub)

- *Funnel available on current plan?* Yes — already on and serving.
- *Queue substrate?* The existing events bus + its rotating JSONL event log; no new infra.
- *How do agents get events?* The existing typed SSE stream `/v0/events/stream`; extmsg bindings for conversation-shaped sources.
- *Priority event types?* GitHub first (unblocks CI/PR-comment context); Vercel second; appointment webhooks once a provider is chosen.

## Related

- `internal/extmsg/` — fabric for reply-able external conversations
- `internal/extmsg/http_adapter.go` — reference `TransportAdapter` implementation shape
- `examples/gastown/packs/gastown/agents/mayor/` — pack-level agent definition; where an overseer's event subscription is configured
- `~/code/localmodels/Plans/distributed-overnight-inference.md` — local agents also need event context
