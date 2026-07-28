package webhookd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/gastownhall/gascity/internal/events"
)

// maxBodyBytes bounds ingress memory per delivery. gascity only extracts a
// compact summary from the body (InboundEventPayload does not carry the raw
// body), so a delivery larger than this is rejected rather than buffered.
const maxBodyBytes = 10 << 20 // 10MiB

// Provider is a webhook source identifier: the {provider} path segment of
// POST /hooks/{provider} (e.g. "github").
type Provider = string

// ProviderGitHub is the first (and currently only) supported provider.
const ProviderGitHub Provider = "github"

// EmitEventFunc records a typed event onto the bus. Mirrors the
// deps.EmitEvent shape internal/extmsg's handlers take (see
// internal/extmsg/inbound.go) so the API layer can wire the same
// bus-backed closure into both.
type EmitEventFunc func(eventType, subject string, payload events.Payload)

// HandlerDeps configures a Handler.
type HandlerDeps struct {
	// Verifiers maps a provider name to the Verifier that authenticates its
	// deliveries. A provider not present here is rejected with 401, the
	// same status a bad signature gets — unknown-provider and bad-signature
	// are both "this delivery does not pass edge verification"
	// (plans/event-stream-webhooks.md).
	Verifiers map[Provider]Verifier

	// EventKind extracts a provider's own event-taxonomy label from a
	// delivery's headers (e.g. GitHub's X-GitHub-Event). A provider absent
	// from this map yields an empty EventKind.
	EventKind map[Provider]func(http.Header) string

	// DeliveryID extracts a provider's idempotency key from a delivery's
	// headers (e.g. GitHub's X-GitHub-Delivery). A provider whose
	// extractor is absent or returns "" fails the delivery with 400:
	// idempotency cannot be enforced without a key.
	DeliveryID map[Provider]func(http.Header) string

	// ActorSubject extracts the optional Actor/Subject summary fields from
	// a delivery's raw body. Best-effort: providers absent from this map,
	// or an extractor that cannot parse the body, simply leave both fields
	// empty rather than failing the delivery.
	ActorSubject map[Provider]func(body []byte) (actor, subject string)

	// Dedup deduplicates deliveries by (provider, delivery ID). Required.
	Dedup *DeliveryDedup

	// EmitEvent records the normalized webhook.inbound event. Required.
	EmitEvent EmitEventFunc

	// Logf logs handler-level failures that cannot be surfaced in the HTTP
	// response. Defaults to log.Printf when nil.
	Logf func(format string, args ...any)
}

// Handler serves POST /hooks/{provider}: verify, dedup, normalize, emit.
// It implements http.Handler directly (not a Huma-typed endpoint) because
// GitHub's HMAC scheme is computed over the exact raw request bytes, which
// must reach the verifier undecoded — the opposite of the typed control
// plane's decode-then-validate contract. v1 emits bus events only; no
// provider gets a reply written back (plans/event-stream-webhooks.md).
type Handler struct {
	deps HandlerDeps
}

// NewHandler constructs a Handler. It panics if Dedup or EmitEvent is nil:
// a Handler that silently drops dedup or emission is a worse failure mode
// than a boot-time crash on misconfiguration.
func NewHandler(deps HandlerDeps) *Handler {
	if deps.Dedup == nil {
		panic("webhookd: HandlerDeps.Dedup is required")
	}
	if deps.EmitEvent == nil {
		panic("webhookd: HandlerDeps.EmitEvent is required")
	}
	if deps.Logf == nil {
		deps.Logf = log.Printf
	}
	return &Handler{deps: deps}
}

// NewGitHubHandlerDeps builds the "github" provider-table entries for a
// Handler: the HMAC verifier plus its header/body extractors. Callers still
// need to set Dedup and EmitEvent before calling NewHandler.
func NewGitHubHandlerDeps(verifier *GitHubVerifier) HandlerDeps {
	return HandlerDeps{
		Verifiers: map[Provider]Verifier{
			ProviderGitHub: verifier,
		},
		EventKind: map[Provider]func(http.Header) string{
			ProviderGitHub: githubEventKind,
		},
		DeliveryID: map[Provider]func(http.Header) string{
			ProviderGitHub: githubDeliveryID,
		},
		ActorSubject: map[Provider]func([]byte) (string, string){
			ProviderGitHub: extractGitHubActorSubject,
		},
	}
}

func githubEventKind(h http.Header) string {
	return strings.TrimSpace(h.Get(githubEventHeader))
}

func githubDeliveryID(h http.Header) string {
	return strings.TrimSpace(h.Get(githubDeliveryHeader))
}

// githubInboundBody is the minimal subset of GitHub's webhook payload
// shapes needed to populate InboundEventPayload's optional Actor/Subject
// summary. Best-effort only.
type githubInboundBody struct {
	Sender struct {
		Login string `json:"login"`
	} `json:"sender"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Issue struct {
		Number int `json:"number"`
	} `json:"issue"`
	PullRequest struct {
		Number int `json:"number"`
	} `json:"pull_request"`
}

// extractGitHubActorSubject best-effort parses body for a sender login and
// an "owner/repo#N" (or bare "owner/repo") subject. An unparseable body
// yields two empty strings rather than an error — Actor/Subject are
// omitempty summary fields, not load-bearing for delivery acceptance.
func extractGitHubActorSubject(body []byte) (actor, subject string) {
	var b githubInboundBody
	if err := json.Unmarshal(body, &b); err != nil {
		return "", ""
	}
	actor = strings.TrimSpace(b.Sender.Login)
	repo := strings.TrimSpace(b.Repository.FullName)
	switch {
	case repo == "":
		return actor, ""
	case b.Issue.Number != 0:
		subject = fmt.Sprintf("%s#%d", repo, b.Issue.Number)
	case b.PullRequest.Number != 0:
		subject = fmt.Sprintf("%s#%d", repo, b.PullRequest.Number)
	default:
		subject = repo
	}
	return actor, subject
}

// ServeHTTP implements http.Handler. Routing is intentionally hand-rolled
// (not http.ServeMux's {provider} pattern) so a Handler value can be
// mounted at any prefix by its caller without baking in a mount path.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	provider := strings.Trim(r.URL.Path, "/")
	if idx := strings.LastIndex(provider, "/"); idx >= 0 {
		provider = provider[idx+1:]
	}
	if provider == "" {
		http.Error(w, "missing provider", http.StatusBadRequest)
		return
	}

	verifier, ok := h.deps.Verifiers[provider]
	if !ok {
		// Unknown provider is authentication-shaped, not routing-shaped:
		// the plan treats "no verifier for this provider" the same as
		// "verifier rejected this delivery" (both 401), so probing for
		// configured providers gets no distinguishing signal.
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	body, err := readLimitedBody(r)
	if err != nil {
		if errors.Is(err, errBodyTooLarge) {
			h.deps.Logf("webhookd: rejecting %s delivery: %v", provider, err)
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		h.deps.Logf("webhookd: reading %s delivery body: %v", provider, err)
		http.Error(w, "internal error reading request body", http.StatusInternalServerError)
		return
	}

	if err := verifier.Verify(r.Header, body); err != nil {
		h.deps.Logf("webhookd: rejecting %s delivery: %v", provider, err)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	deliveryID := ""
	if extract := h.deps.DeliveryID[provider]; extract != nil {
		deliveryID = extract(r.Header)
	}
	if deliveryID == "" {
		http.Error(w, "missing delivery id", http.StatusBadRequest)
		return
	}

	if h.deps.Dedup.Seen(provider, deliveryID) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("duplicate delivery, already processed\n"))
		return
	}

	eventKind := ""
	if extract := h.deps.EventKind[provider]; extract != nil {
		eventKind = extract(r.Header)
	}
	var actor, subject string
	if extract := h.deps.ActorSubject[provider]; extract != nil {
		actor, subject = extract(body)
	}
	digest := sha256.Sum256(body)

	payload := InboundEventPayload{
		Provider:      provider,
		EventKind:     eventKind,
		DeliveryID:    deliveryID,
		Actor:         actor,
		Subject:       subject,
		PayloadDigest: hex.EncodeToString(digest[:]),
	}
	h.deps.EmitEvent(events.WebhookInbound, subject, payload)

	w.WriteHeader(http.StatusAccepted)
}

// errBodyTooLarge is returned by readLimitedBody when the delivery exceeds
// maxBodyBytes. Distinguished from a genuine io.ReadAll failure so ServeHTTP
// can return 413 (client-shaped, no redelivery expected) instead of 500
// (server-shaped, provider should retry).
var errBodyTooLarge = fmt.Errorf("webhookd: request body exceeds %d byte limit", maxBodyBytes)

func readLimitedBody(r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("webhookd: reading request body: %w", err)
	}
	if len(body) > maxBodyBytes {
		return nil, errBodyTooLarge
	}
	return body, nil
}
