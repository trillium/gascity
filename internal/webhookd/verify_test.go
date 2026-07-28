package webhookd

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"testing"
)

func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return githubSHA256Prefix + hex.EncodeToString(mac.Sum(nil))
}

func envLookup(values map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		v, ok := values[name]
		return v, ok
	}
}

func TestNewGitHubVerifierValidatesSecretEnvName(t *testing.T) {
	if _, err := NewGitHubVerifier("GITHUB_WEBHOOK_SECRET"); err != nil {
		t.Fatalf("valid identifier rejected: %v", err)
	}
	for _, bad := range []string{"", "  ", "1LEADING_DIGIT", "has spaces", "has-dash", "has.dot"} {
		if _, err := NewGitHubVerifier(bad); err == nil {
			t.Fatalf("secret env %q: expected error, got nil", bad)
		}
	}
}

func TestGitHubVerifierValidSignatureAccepted(t *testing.T) {
	body := []byte(`{"action":"opened"}`)
	secret := "s3cr3t"

	v, err := NewGitHubVerifier("WEBHOOK_SECRET")
	if err != nil {
		t.Fatal(err)
	}
	v.lookupEnv = envLookup(map[string]string{"WEBHOOK_SECRET": secret})

	h := http.Header{}
	h.Set(githubSignatureHeader, sign(secret, body))

	if err := v.Verify(h, body); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
}

func TestGitHubVerifierInvalidSignatureRejected(t *testing.T) {
	body := []byte(`{"action":"opened"}`)
	v, err := NewGitHubVerifier("WEBHOOK_SECRET")
	if err != nil {
		t.Fatal(err)
	}
	v.lookupEnv = envLookup(map[string]string{"WEBHOOK_SECRET": "s3cr3t"})

	cases := map[string]string{
		"wrong secret":    sign("wrong-secret", body),
		"tampered body":   sign("s3cr3t", []byte(`{"action":"closed"}`)),
		"malformed hex":   githubSHA256Prefix + "not-hex!!",
		"missing prefix":  hex.EncodeToString([]byte("deadbeef")),
		"truncated bytes": githubSHA256Prefix + "ab",
	}
	for name, sig := range cases {
		t.Run(name, func(t *testing.T) {
			h := http.Header{}
			h.Set(githubSignatureHeader, sig)
			err := v.Verify(h, body)
			if !errors.Is(err, ErrInvalidSignature) {
				t.Fatalf("got err %v, want ErrInvalidSignature", err)
			}
		})
	}
}

func TestGitHubVerifierMissingSignatureRejected(t *testing.T) {
	v, err := NewGitHubVerifier("WEBHOOK_SECRET")
	if err != nil {
		t.Fatal(err)
	}
	v.lookupEnv = envLookup(map[string]string{"WEBHOOK_SECRET": "s3cr3t"})

	err = v.Verify(http.Header{}, []byte(`{}`))
	if !errors.Is(err, ErrMissingSignature) {
		t.Fatalf("got err %v, want ErrMissingSignature", err)
	}
}

func TestGitHubVerifierUnsetSecretRejected(t *testing.T) {
	v, err := NewGitHubVerifier("WEBHOOK_SECRET")
	if err != nil {
		t.Fatal(err)
	}
	v.lookupEnv = envLookup(map[string]string{}) // not present

	body := []byte(`{}`)
	h := http.Header{}
	h.Set(githubSignatureHeader, sign("anything", body))

	err = v.Verify(h, body)
	if !errors.Is(err, ErrSecretUnset) {
		t.Fatalf("got err %v, want ErrSecretUnset", err)
	}
}

func TestGitHubVerifierEmptySecretRejected(t *testing.T) {
	v, err := NewGitHubVerifier("WEBHOOK_SECRET")
	if err != nil {
		t.Fatal(err)
	}
	v.lookupEnv = envLookup(map[string]string{"WEBHOOK_SECRET": "   "})

	body := []byte(`{}`)
	h := http.Header{}
	h.Set(githubSignatureHeader, sign("anything", body))

	err = v.Verify(h, body)
	if !errors.Is(err, ErrSecretUnset) {
		t.Fatalf("got err %v, want ErrSecretUnset", err)
	}
}
