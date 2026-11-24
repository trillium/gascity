package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/events"
)

const testWebhookSecretEnv = "GC_TEST_GITHUB_WEBHOOK_SECRET"

func signGitHub(secret, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func postWebhook(t *testing.T, srv *Server, provider, delivery, body, sig string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, cityURL(srv.state, "/hooks/"+provider), strings.NewReader(body))
	req.Header.Set("X-GitHub-Event", "issue_comment")
	if delivery != "" {
		req.Header.Set("X-GitHub-Delivery", delivery)
	}
	if sig != "" {
		req.Header.Set("X-Hub-Signature-256", sig)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func TestWebhookIngressEmitsTypedEvent(t *testing.T) {
	t.Setenv(testWebhookSecretEnv, "s3cret")
	fs := newFakeState(t)
	fs.cfg.Webhooks.GitHub.SecretEnv = testWebhookSecretEnv
	srv := New(fs)

	body := `{"sender":{"login":"octocat"},"repository":{"full_name":"o/r"},"issue":{"number":7}}`
	rec := postWebhook(t, srv, "github", "d-1", body, signGitHub("s3cret", body))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}

	evs, err := fs.eventProv.List(events.Filter{Type: events.WebhookInbound})
	if err != nil || len(evs) != 1 {
		t.Fatalf("webhook.inbound events = %d (err %v), want 1", len(evs), err)
	}
	if evs[0].Subject != "o/r#7" {
		t.Errorf("subject = %q, want o/r#7", evs[0].Subject)
	}

	// Redelivery of the same delivery ID is acknowledged but not re-emitted.
	rec = postWebhook(t, srv, "github", "d-1", body, signGitHub("s3cret", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("redelivery status = %d, want 200", rec.Code)
	}
	evs, _ = fs.eventProv.List(events.Filter{Type: events.WebhookInbound})
	if len(evs) != 1 {
		t.Fatalf("events after redelivery = %d, want 1", len(evs))
	}
}

func TestWebhookIngressRejectsBadSignature(t *testing.T) {
	t.Setenv(testWebhookSecretEnv, "s3cret")
	fs := newFakeState(t)
	fs.cfg.Webhooks.GitHub.SecretEnv = testWebhookSecretEnv
	srv := New(fs)

	rec := postWebhook(t, srv, "github", "d-2", `{}`, signGitHub("wrong", `{}`))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if evs, _ := fs.eventProv.List(events.Filter{Type: events.WebhookInbound}); len(evs) != 0 {
		t.Fatalf("events = %d, want 0", len(evs))
	}
}

func TestWebhookIngressUnconfiguredProviderIs401(t *testing.T) {
	fs := newFakeState(t)
	srv := New(fs)
	rec := postWebhook(t, srv, "github", "d-3", `{}`, signGitHub("x", `{}`))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestWebhookIngressInvalidSecretEnvConfigIs500(t *testing.T) {
	fs := newFakeState(t)
	fs.cfg.Webhooks.GitHub.SecretEnv = "not a valid name"
	srv := New(fs)
	rec := postWebhook(t, srv, "github", "d-4", `{}`, "sha256=00")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}
