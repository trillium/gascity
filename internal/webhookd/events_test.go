package webhookd

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/gastownhall/gascity/internal/events"
)

// TestWebhookInboundPayloadRegistered pins the init-time registration: the
// SSE projection decodes webhook.inbound emissions via this registry entry,
// so a missing or wrongly-typed registration is a schema bug.
func TestWebhookInboundPayloadRegistered(t *testing.T) {
	sample, ok := events.LookupPayload(events.WebhookInbound)
	if !ok {
		t.Fatalf("no payload registered for %q", events.WebhookInbound)
	}
	if got, want := reflect.TypeOf(sample), reflect.TypeOf(InboundEventPayload{}); got != want {
		t.Fatalf("payload for %q registered as %v, want %v", events.WebhookInbound, got, want)
	}
}

// TestInboundEventPayloadRoundTrip pins the wire field names — subscribers
// filter on provider/event_kind, and delivery_id is the idempotency key.
func TestInboundEventPayloadRoundTrip(t *testing.T) {
	in := InboundEventPayload{
		Provider:      "github",
		EventKind:     "issue_comment",
		DeliveryID:    "72d3162e-cc78-11e3-81ab-4c9367dc0958",
		Actor:         "trillium",
		Subject:       "gastownhall/gascity#3435",
		PayloadDigest: "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"provider", "event_kind", "delivery_id", "actor", "subject", "payload_digest"} {
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		if _, ok := m[key]; !ok {
			t.Fatalf("wire JSON missing %q: %s", key, raw)
		}
	}
	var out InboundEventPayload
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Fatalf("round-trip mismatch: got %+v want %+v", out, in)
	}
}
