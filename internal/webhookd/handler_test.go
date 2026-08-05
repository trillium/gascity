package webhookd

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/events"
)

const githubIssueCommentBody = `{
  "action": "created",
  "sender": {"login": "trillium"},
  "repository": {"full_name": "gastownhall/gascity"},
  "issue": {"number": 3435}
}`

// testSecret is the fixed GitHub webhook secret used by every test in this
// file. No test currently needs a distinct correct secret across handlers,
// so this is a constant rather than a parameter threaded through the
// helpers below.
const testSecret = "s3cr3t"

type recordedEmission struct {
	eventType string
	subject   string
	payload   events.Payload
}

func newTestHandler(t *testing.T) (*Handler, *[]recordedEmission) {
	t.Helper()
	v, err := NewGitHubVerifier("WEBHOOK_SECRET")
	if err != nil {
		t.Fatal(err)
	}
	v.lookupEnv = envLookup(map[string]string{"WEBHOOK_SECRET": testSecret})

	var emitted []recordedEmission
	deps := NewGitHubHandlerDeps(v)
	deps.Dedup = NewDeliveryDedup(time.Minute, 10)
	deps.EmitEvent = func(eventType, subject string, payload events.Payload) {
		emitted = append(emitted, recordedEmission{eventType, subject, payload})
	}
	return NewHandler(deps), &emitted
}

func doGitHubRequest(t *testing.T, h *Handler, deliveryID, eventKind string, body []byte, corruptSig bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/hooks/github", strings.NewReader(string(body)))
	sig := sign(testSecret, body)
	if corruptSig {
		sig = sign("wrong-secret-entirely", body)
	}
	req.Header.Set(githubSignatureHeader, sig)
	if deliveryID != "" {
		req.Header.Set(githubDeliveryHeader, deliveryID)
	}
	if eventKind != "" {
		req.Header.Set(githubEventHeader, eventKind)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHandlerValidDeliveryAccepted(t *testing.T) {
	h, emitted := newTestHandler(t)
	body := []byte(githubIssueCommentBody)

	rec := doGitHubRequest(t, h, "delivery-1", "issue_comment", body, false)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if len(*emitted) != 1 {
		t.Fatalf("EmitEvent called %d times, want 1", len(*emitted))
	}
	got := (*emitted)[0]
	if got.eventType != events.WebhookInbound {
		t.Fatalf("eventType = %q, want %q", got.eventType, events.WebhookInbound)
	}
	payload, ok := got.payload.(InboundEventPayload)
	if !ok {
		t.Fatalf("payload type = %T, want InboundEventPayload", got.payload)
	}
	digest := sha256.Sum256(body)
	want := InboundEventPayload{
		Provider:      "github",
		EventKind:     "issue_comment",
		DeliveryID:    "delivery-1",
		Actor:         "trillium",
		Subject:       "gastownhall/gascity#3435",
		PayloadDigest: hex.EncodeToString(digest[:]),
	}
	if payload != want {
		t.Fatalf("payload = %+v, want %+v", payload, want)
	}
	if got.subject != want.Subject {
		t.Fatalf("emit subject = %q, want %q", got.subject, want.Subject)
	}
}

func TestHandlerInvalidSignatureRejected(t *testing.T) {
	h, emitted := newTestHandler(t)
	body := []byte(githubIssueCommentBody)

	rec := doGitHubRequest(t, h, "delivery-1", "issue_comment", body, true)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if len(*emitted) != 0 {
		t.Fatalf("EmitEvent called %d times, want 0", len(*emitted))
	}
}

func TestHandlerMissingSignatureRejected(t *testing.T) {
	h, emitted := newTestHandler(t)
	body := []byte(githubIssueCommentBody)

	req := httptest.NewRequest(http.MethodPost, "/hooks/github", strings.NewReader(string(body)))
	req.Header.Set(githubDeliveryHeader, "delivery-1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if len(*emitted) != 0 {
		t.Fatalf("EmitEvent called %d times, want 0", len(*emitted))
	}
}

func TestHandlerDuplicateDeliveryIsNoOp(t *testing.T) {
	h, emitted := newTestHandler(t)
	body := []byte(githubIssueCommentBody)

	first := doGitHubRequest(t, h, "delivery-1", "issue_comment", body, false)
	if first.Code != http.StatusAccepted {
		t.Fatalf("first delivery status = %d, want %d", first.Code, http.StatusAccepted)
	}

	second := doGitHubRequest(t, h, "delivery-1", "issue_comment", body, false)
	if second.Code != http.StatusOK {
		t.Fatalf("duplicate delivery status = %d, want %d", second.Code, http.StatusOK)
	}

	if len(*emitted) != 1 {
		t.Fatalf("EmitEvent called %d times across both requests, want 1 (duplicate must be a no-op)", len(*emitted))
	}
}

func TestHandlerUnknownProviderRejected(t *testing.T) {
	h, emitted := newTestHandler(t)
	body := []byte(githubIssueCommentBody)

	req := httptest.NewRequest(http.MethodPost, "/hooks/vercel", strings.NewReader(string(body)))
	req.Header.Set(githubSignatureHeader, sign(testSecret, body))
	req.Header.Set(githubDeliveryHeader, "delivery-1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if len(*emitted) != 0 {
		t.Fatalf("EmitEvent called %d times, want 0", len(*emitted))
	}
}

func TestHandlerWrongMethodRejected(t *testing.T) {
	h, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/hooks/github", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandlerMissingDeliveryIDRejected(t *testing.T) {
	h, emitted := newTestHandler(t)
	body := []byte(githubIssueCommentBody)

	rec := doGitHubRequest(t, h, "", "issue_comment", body, false)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if len(*emitted) != 0 {
		t.Fatalf("EmitEvent called %d times, want 0", len(*emitted))
	}
}

func TestHandlerUnparseableBodyStillAccepted(t *testing.T) {
	// Actor/Subject extraction is best-effort; a body GitHub's HMAC still
	// authenticates but that isn't valid JSON must not block acceptance.
	h, emitted := newTestHandler(t)
	body := []byte("not json")

	rec := doGitHubRequest(t, h, "delivery-1", "ping", body, false)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if len(*emitted) != 1 {
		t.Fatalf("EmitEvent called %d times, want 1", len(*emitted))
	}
	payload := (*emitted)[0].payload.(InboundEventPayload)
	if payload.Actor != "" || payload.Subject != "" {
		t.Fatalf("Actor/Subject = %q/%q, want empty for unparseable body", payload.Actor, payload.Subject)
	}
}

func TestNewHandlerPanicsWithoutRequiredDeps(t *testing.T) {
	v, err := NewGitHubVerifier("WEBHOOK_SECRET")
	if err != nil {
		t.Fatal(err)
	}

	mustPanic := func(name string, fn func()) {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("expected panic, got none")
				}
			}()
			fn()
		})
	}

	mustPanic("missing dedup", func() {
		deps := NewGitHubHandlerDeps(v)
		deps.EmitEvent = func(string, string, events.Payload) {}
		NewHandler(deps)
	})
	mustPanic("missing emit", func() {
		deps := NewGitHubHandlerDeps(v)
		deps.Dedup = NewDeliveryDedup(time.Minute, 10)
		NewHandler(deps)
	})
}
