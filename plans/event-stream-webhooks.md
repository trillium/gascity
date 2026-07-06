# Event Stream & Webhook Ingestion

> **Updated:** 2026-07-06 (task-h5q architecture pass). The original stub's open questions are answered below from live probes and repo survey. Net finding: **most of this already exists** — gascity has a typed event bus with an SSE wire, and Tailscale Funnel is already on. The only missing piece is a webhook ingress connector.

## Problem

Agents need external events (GitHub comments, CI results, appointment confirmations) as context, but there's no path for external services to push events in. Everything is pull-based (`internal/config/github_pr_monitor.go`) or manual.

## What already exists (verified 2026-07-06)

- **Event bus + agent subscription: SOLVED.** `internal/events` is a typed event bus; payloads implementing `events.Payload` flow through the central registry and emerge on the typed **`/v0/events/stream`** wire with named schemas (see `internal/extmsg/events.go` for the pattern). Agents subscribe by event type. No Redis, no JSONL, no new queue — the answer to "what substrate" is *the bus that's already there*, persisted via the Dolt-backed `events`/`wisp_events` tables (`internal/beads/native_dolt_store.go`).
- **Conversational routing: SOLVED.** `internal/extmsg` is the external-message fabric (adapters, bindings, delivery, per-conversation session ownership) — openclaw-bridge (#3435) ships iMessage + Telegram connectors on it. Reply-able external sources belong here; fire-and-forget events belong on the bus directly.
- **Inbound exposure: SOLVED.** Tailscale Funnel is **already enabled and serving** on this machine (`tailscale funnel status` → `https://macbook.hippo-tilapia.ts.net`). External webhooks can POST to a funneled port today. Outbound was never blocked — Tailscale nodes have ordinary egress.
- **Prior art to retire:** `internal/config/github_pr_monitor.go` polls GitHub. Once GitHub webhooks flow, retire the poller (retirement rule: new surface names what it replaces).

## The gap: webhook ingress

External service → public HTTPS (Funnel) → **[missing: verify + normalize + emit]** → events bus → `/v0/events/stream` → agents.

Design:

- An ingress HTTP service exposing `POST /hooks/{provider}` (e.g. `/hooks/github`, `/hooks/vercel`), funneled.
- Per-provider verification at the edge: GitHub `X-Hub-Signature-256` HMAC, Vercel signature header; unknown provider or bad signature → 401, never reaches the bus. Secrets via the existing config layer, never in code.
- Normalization to a typed payload, e.g. `events.WebhookInbound` (`webhook.inbound`) carrying `{provider, event_kind, delivery_id, actor, subject, payload_digest, raw_ref}` — the full raw body stored/persisted, the bus event kept small (targeted context, not dumps).
- Idempotency on provider delivery IDs (GitHub `X-GitHub-Delivery`) — webhooks redeliver; the ingress dedupes.
- Reply-able providers (a GitHub PR comment an agent might answer) can additionally bridge into extmsg as an adapter later; v1 emits bus events only.

## Next steps (builder-actionable, in order)

1. Define the `WebhookInbound` payload type in `internal/events` following the `extmsg/events.go` pattern (typed struct, `IsEventPayload()`, named schema constant). Unit test schema registration.
2. Build the ingress service (new `internal/webhookd`, patterned on `internal/workspacesvc` service shape): `POST /hooks/{provider}`, provider verifier interface + GitHub HMAC implementation first, idempotency cache keyed on delivery ID, emit to bus. Table-driven tests with recorded GitHub payloads.
3. Wire exposure: serve on a local port, `tailscale funnel <port>` route, document the public URL + GitHub webhook setup (repo settings → webhook → secret) in the service doc.
4. Point one real GitHub repo's webhook at it; verify an issue comment lands on `/v0/events/stream` end-to-end.
5. Retire `github_pr_monitor.go` polling for repos covered by webhooks (same PR as step 4 or immediately after — gate-or-delete).
6. Mayor subscription: per `Plans/mayor-agent-architecture.md`, mayor consumes `webhook.inbound` filtered by provider/event_kind.

## Answered questions (from the original stub)

- *Funnel available on current plan?* Yes — already on and serving.
- *Queue substrate?* The existing events bus + Dolt persistence; no new infra.
- *How do agents get events?* The existing typed SSE stream `/v0/events/stream`; extmsg bindings for conversation-shaped sources.
- *Priority event types?* GitHub first (replaces an existing poller and unblocks CI/PR-comment context); Vercel second; appointment webhooks once a provider is chosen.

## Related

- `Plans/mayor-agent-architecture.md` — mayor is the primary event consumer
- `internal/extmsg/` — fabric for reply-able external conversations
- `contrib/openclaw-bridge/` — reference connector implementation shape
- `~/code/localmodels/Plans/distributed-overnight-inference.md` — local agents also need event context
