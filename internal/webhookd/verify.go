package webhookd

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
)

// Sentinel errors returned by Verifier.Verify. Callers should treat any
// non-nil error as "reject with 401"; the distinct values exist for
// logging/testing, not for divergent handler behavior.
var (
	// ErrMissingSignature is returned when the provider's signature header
	// is absent from the request.
	ErrMissingSignature = errors.New("webhookd: missing signature header")
	// ErrInvalidSignature is returned when a signature header is present
	// but does not authenticate the body.
	ErrInvalidSignature = errors.New("webhookd: invalid signature")
	// ErrSecretUnset is returned when the verifier's configured secret
	// environment variable is unset or empty at verify time.
	ErrSecretUnset = errors.New("webhookd: webhook secret env var is unset")
)

// validSecretEnvName enforces that only an environment-variable NAME is
// ever configured for a webhook secret — never the secret VALUE itself.
var validSecretEnvName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// GitHub webhook delivery headers, per
// https://docs.github.com/en/webhooks/webhook-events-and-payloads.
const (
	githubSignatureHeader = "X-Hub-Signature-256"
	githubEventHeader     = "X-GitHub-Event"
	githubDeliveryHeader  = "X-GitHub-Delivery"
	githubSHA256Prefix    = "sha256="
)

// Verifier authenticates one provider's webhook deliveries. Implementations
// must not mutate header or body, and must run in time independent of how
// much of the signature matches (constant-time comparison).
type Verifier interface {
	// Verify returns nil if body is an authentic delivery per header's
	// signature, or a non-nil error otherwise.
	Verify(header http.Header, body []byte) error
}

// GitHubVerifier verifies GitHub webhook deliveries via the
// X-Hub-Signature-256 HMAC-SHA256 scheme. The secret value itself is never
// held on the struct; SecretEnv names the environment variable holding it,
// and the value is looked up fresh on every Verify call so secret rotation
// takes effect without a process restart.
type GitHubVerifier struct {
	// SecretEnv is the environment variable containing the HMAC secret.
	SecretEnv string

	// lookupEnv overrides os.LookupEnv in tests.
	lookupEnv func(string) (string, bool)
}

// NewGitHubVerifier constructs a GitHubVerifier for the given secret
// environment variable name. It returns an error if secretEnv is not a
// valid environment-variable identifier.
func NewGitHubVerifier(secretEnv string) (*GitHubVerifier, error) {
	secretEnv = strings.TrimSpace(secretEnv)
	if !validSecretEnvName.MatchString(secretEnv) {
		return nil, fmt.Errorf("webhookd: secret_env must be an environment variable name, got %q", secretEnv)
	}
	return &GitHubVerifier{SecretEnv: secretEnv}, nil
}

func (v *GitHubVerifier) lookup(name string) (string, bool) {
	if v.lookupEnv != nil {
		return v.lookupEnv(name)
	}
	return os.LookupEnv(name)
}

// Verify implements Verifier for GitHub's X-Hub-Signature-256 scheme.
func (v *GitHubVerifier) Verify(header http.Header, body []byte) error {
	secret, ok := v.lookup(v.SecretEnv)
	if !ok || strings.TrimSpace(secret) == "" {
		return ErrSecretUnset
	}

	got := strings.TrimSpace(header.Get(githubSignatureHeader))
	if got == "" {
		return ErrMissingSignature
	}
	if !strings.HasPrefix(got, githubSHA256Prefix) {
		return ErrInvalidSignature
	}
	gotSum, err := hex.DecodeString(strings.TrimPrefix(got, githubSHA256Prefix))
	if err != nil {
		return ErrInvalidSignature
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := mac.Sum(nil)

	if len(gotSum) != len(want) || subtle.ConstantTimeCompare(gotSum, want) != 1 {
		return ErrInvalidSignature
	}
	return nil
}
