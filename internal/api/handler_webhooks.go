package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/gastownhall/gascity/internal/events"
	"github.com/gastownhall/gascity/internal/webhookd"
)

// handleWebhook serves POST /v0/city/{cityName}/hooks/{provider} (reached
// via the supervisor as /hooks/{provider}). It is a raw http.Handler, not a
// Huma operation: provider HMAC signatures cover the exact request bytes.
// Providers without a configured secret reject every delivery with 401.
func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	deps := webhookd.HandlerDeps{
		Verifiers:    map[webhookd.Provider]webhookd.Verifier{},
		EventKind:    map[webhookd.Provider]func(http.Header) string{},
		DeliveryID:   map[webhookd.Provider]func(http.Header) string{},
		ActorSubject: map[webhookd.Provider]func([]byte) (string, string){},
		Dedup:        s.webhookDedup,
		EmitEvent:    s.webhookEmitEvent(),
	}
	if cfg := s.state.Config(); cfg != nil && cfg.Webhooks.GitHub.SecretEnv != "" {
		v, err := webhookd.NewGitHubVerifier(cfg.Webhooks.GitHub.SecretEnv)
		if err != nil {
			fmt.Fprintf(os.Stderr, "webhookd: invalid [webhooks.github] config: %v\n", err)
			http.Error(w, "webhook ingress misconfigured", http.StatusInternalServerError)
			return
		}
		gh := webhookd.NewGitHubHandlerDeps(v)
		deps.Verifiers = gh.Verifiers
		deps.EventKind = gh.EventKind
		deps.DeliveryID = gh.DeliveryID
		deps.ActorSubject = gh.ActorSubject
	}
	webhookd.NewHandler(deps).ServeHTTP(w, r)
}

// webhookEmitEvent records webhook.inbound events on the city event bus.
func (s *Server) webhookEmitEvent() webhookd.EmitEventFunc {
	ep := s.state.EventProvider()
	if ep == nil {
		return func(string, string, events.Payload) {}
	}
	return func(eventType, subject string, payload events.Payload) {
		b, err := json.Marshal(payload)
		if err != nil {
			fmt.Fprintf(os.Stderr, "webhookd: marshal event payload: %v\n", err)
			return
		}
		ep.Record(events.Event{Type: eventType, Subject: subject, Payload: b})
	}
}
