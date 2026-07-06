// Package webhookd is the external webhook ingress: it exposes
// funnel-facing HTTP endpoints (POST /hooks/{provider}), verifies each
// delivery with the provider's signature scheme, deduplicates on the
// provider delivery ID, and emits a compact typed event onto the bus.
// Design and sequencing: plans/event-stream-webhooks.md.
package webhookd

import "github.com/gastownhall/gascity/internal/events"

// InboundEventPayload is emitted on events.WebhookInbound
// ("webhook.inbound") after a delivery passes signature verification.
// It deliberately carries a summary, not the raw body — subscribers that
// need full payloads fetch them from the ingress store by DeliveryID
// (targeted context, not dumps).
type InboundEventPayload struct {
	// Provider is the ingress route that accepted the delivery ("github",
	// "vercel", ...).
	Provider string `json:"provider"`
	// EventKind is the provider's own event taxonomy (GitHub
	// X-GitHub-Event: "issue_comment", "push", ...).
	EventKind string `json:"event_kind"`
	// DeliveryID is the provider's unique delivery identifier (GitHub
	// X-GitHub-Delivery). It is the idempotency key: redeliveries with a
	// seen ID are acknowledged but not re-emitted.
	DeliveryID string `json:"delivery_id"`
	// Actor is the external principal that triggered the event, when the
	// provider exposes one (GitHub sender login).
	Actor string `json:"actor,omitempty"`
	// Subject names the thing the event is about in provider terms
	// (e.g. "owner/repo#123").
	Subject string `json:"subject,omitempty"`
	// PayloadDigest is the hex SHA-256 of the raw request body, tying the
	// bus event to the stored raw delivery without carrying it.
	PayloadDigest string `json:"payload_digest"`
}

// IsEventPayload marks InboundEventPayload as an events.Payload variant.
func (InboundEventPayload) IsEventPayload() {}

func init() {
	events.RegisterPayload(events.WebhookInbound, InboundEventPayload{})
}
