package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/mail"
	"github.com/stretchr/testify/require"
)

// fakeBootCheckSource is a test double for BootCheckSource.
type fakeBootCheckSource struct {
	hooked    []beads.Bead
	hookedErr error
	routed    []beads.Bead
	routedErr error
	ready     []beads.Bead
	readyErr  error
	inbox     []mail.Message
	inboxErr  error
	rigs      []BootCheckRig
	rigsErr   error

	// hookedDelay etc. simulate slow queries.
	hookedDelay time.Duration
	routedDelay time.Duration
	readyDelay  time.Duration
	inboxDelay  time.Duration
	rigsDelay   time.Duration
}

func (f *fakeBootCheckSource) Hooked(ctx context.Context, _ []string) ([]beads.Bead, error) {
	if f.hookedDelay > 0 {
		select {
		case <-time.After(f.hookedDelay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return f.hooked, f.hookedErr
}

func (f *fakeBootCheckSource) Routed(ctx context.Context, _ string) ([]beads.Bead, error) {
	if f.routedDelay > 0 {
		select {
		case <-time.After(f.routedDelay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return f.routed, f.routedErr
}

func (f *fakeBootCheckSource) Ready(ctx context.Context) ([]beads.Bead, error) {
	if f.readyDelay > 0 {
		select {
		case <-time.After(f.readyDelay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return f.ready, f.readyErr
}

func (f *fakeBootCheckSource) Inbox(ctx context.Context, _ []string) ([]mail.Message, error) {
	if f.inboxDelay > 0 {
		select {
		case <-time.After(f.inboxDelay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return f.inbox, f.inboxErr
}

func (f *fakeBootCheckSource) Rigs(ctx context.Context) ([]BootCheckRig, error) {
	if f.rigsDelay > 0 {
		select {
		case <-time.After(f.rigsDelay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return f.rigs, f.rigsErr
}

func TestBootCheckAllEmpty(t *testing.T) {
	src := &fakeBootCheckSource{}
	result := doBootCheck(src, []string{"mayor"}, "mayor")

	require.Empty(t, result.Hooked)
	require.Empty(t, result.Routed)
	require.Empty(t, result.Ready)
	require.Empty(t, result.Mail)
	require.Empty(t, result.Rigs)
}

func TestBootCheckAllPopulated(t *testing.T) {
	src := &fakeBootCheckSource{
		hooked: []beads.Bead{{ID: "gc-1", Title: "hooked work", Status: "in_progress"}},
		routed: []beads.Bead{{ID: "gc-2", Title: "routed work", Status: "open"}},
		ready:  []beads.Bead{{ID: "gc-3", Title: "ready work", Status: "open"}},
		inbox:  []mail.Message{{ID: "gc-4", From: "polecat", Subject: "done"}},
		rigs: []BootCheckRig{
			{Name: "frontend", Path: "/code/frontend"},
			{Name: "backend", Path: "/code/backend", Suspended: true},
		},
	}
	result := doBootCheck(src, []string{"mayor"}, "mayor")

	require.Len(t, result.Hooked, 1)
	require.Equal(t, "gc-1", result.Hooked[0].ID)
	require.Len(t, result.Routed, 1)
	require.Equal(t, "gc-2", result.Routed[0].ID)
	require.Len(t, result.Ready, 1)
	require.Equal(t, "gc-3", result.Ready[0].ID)
	require.Len(t, result.Mail, 1)
	require.Equal(t, "gc-4", result.Mail[0].ID)
	require.Len(t, result.Rigs, 2)
	require.Equal(t, "frontend", result.Rigs[0].Name)
	require.True(t, result.Rigs[1].Suspended)
}

func TestBootCheckJSONOutput(t *testing.T) {
	src := &fakeBootCheckSource{
		ready: []beads.Bead{{ID: "gc-1", Title: "task", Status: "open"}},
	}
	result := doBootCheck(src, []string{"mayor"}, "mayor")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := writeBootCheckResult(result, &stdout, &stderr)
	require.Equal(t, 0, code)

	var parsed BootCheckResult
	err := json.Unmarshal(stdout.Bytes(), &parsed)
	require.NoError(t, err)
	require.Len(t, parsed.Ready, 1)
	require.Equal(t, "gc-1", parsed.Ready[0].ID)
}

func TestBootCheckJSONEmptyArraysNotNull(t *testing.T) {
	src := &fakeBootCheckSource{}
	result := doBootCheck(src, []string{"mayor"}, "mayor")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := writeBootCheckResult(result, &stdout, &stderr)
	require.Equal(t, 0, code)

	// Verify arrays are [] not null.
	var raw map[string]json.RawMessage
	err := json.Unmarshal(stdout.Bytes(), &raw)
	require.NoError(t, err)

	for _, key := range []string{"hooked", "routed", "ready", "mail", "rigs"} {
		require.Equal(t, "[]", string(raw[key]), "field %q should be [] not null", key)
	}
}

func TestBootCheckPartialResultsOnError(t *testing.T) {
	src := &fakeBootCheckSource{
		hookedErr: errors.New("store timeout"),
		routedErr: errors.New("store timeout"),
		readyErr:  errors.New("store timeout"),
		inboxErr:  errors.New("mail down"),
		rigs: []BootCheckRig{
			{Name: "frontend", Path: "/code/frontend"},
		},
	}
	result := doBootCheck(src, []string{"mayor"}, "mayor")

	// Errors produce empty arrays, but rigs still returned.
	require.Empty(t, result.Hooked)
	require.Empty(t, result.Routed)
	require.Empty(t, result.Ready)
	require.Empty(t, result.Mail)
	require.Len(t, result.Rigs, 1)
}

func TestBootCheckMailTimeoutReturnsPartial(t *testing.T) {
	src := &fakeBootCheckSource{
		ready:      []beads.Bead{{ID: "gc-1", Title: "task", Status: "open"}},
		rigs:       []BootCheckRig{{Name: "frontend", Path: "/code/frontend"}},
		inboxDelay: 10 * time.Second, // longer than overall 5s timeout
	}
	result := doBootCheck(src, []string{"mayor"}, "mayor")

	// Mail should be empty due to timeout, but ready and rigs should succeed.
	require.Len(t, result.Ready, 1)
	require.Len(t, result.Rigs, 1)
	require.Empty(t, result.Mail)
}

func TestBootCheckNoIdentities(t *testing.T) {
	src := &fakeBootCheckSource{
		ready: []beads.Bead{{ID: "gc-1", Title: "task", Status: "open"}},
		rigs:  []BootCheckRig{{Name: "frontend", Path: "/code/frontend"}},
	}
	// No identities — hooked and mail queries should be skipped.
	result := doBootCheck(src, nil, "")

	require.Len(t, result.Ready, 1)
	require.Len(t, result.Rigs, 1)
	require.Empty(t, result.Hooked)
	require.Empty(t, result.Routed)
	require.Empty(t, result.Mail)
}

func TestBootCheckCompletesWithinTimeout(t *testing.T) {
	src := &fakeBootCheckSource{
		hookedDelay: 50 * time.Millisecond,
		routedDelay: 50 * time.Millisecond,
		readyDelay:  50 * time.Millisecond,
		inboxDelay:  50 * time.Millisecond,
		rigsDelay:   50 * time.Millisecond,
		hooked:      []beads.Bead{{ID: "gc-1", Title: "hooked", Status: "in_progress"}},
		routed:      []beads.Bead{{ID: "gc-2", Title: "routed", Status: "open"}},
		ready:       []beads.Bead{{ID: "gc-3", Title: "ready", Status: "open"}},
		inbox:       []mail.Message{{ID: "gc-4", From: "polecat", Subject: "done"}},
		rigs:        []BootCheckRig{{Name: "frontend", Path: "/code/frontend"}},
	}

	start := time.Now()
	result := doBootCheck(src, []string{"mayor"}, "mayor")
	elapsed := time.Since(start)

	// All queries should complete well under the 5s overall timeout.
	require.Less(t, elapsed, 2*time.Second)
	require.Len(t, result.Hooked, 1)
	require.Len(t, result.Routed, 1)
	require.Len(t, result.Ready, 1)
	require.Len(t, result.Mail, 1)
	require.Len(t, result.Rigs, 1)
}

func TestBootCheckIdentities(t *testing.T) {
	// Save and restore env vars.
	envVars := []string{"GC_SESSION_ID", "GC_SESSION_NAME", "GC_ALIAS", "GC_AGENT"}
	saved := make(map[string]string)
	for _, k := range envVars {
		saved[k] = os.Getenv(k)
	}
	defer func() {
		for _, k := range envVars {
			if saved[k] == "" {
				os.Unsetenv(k) //nolint:errcheck
			} else {
				os.Setenv(k, saved[k]) //nolint:errcheck
			}
		}
	}()

	os.Setenv("GC_SESSION_ID", "sess-123")   //nolint:errcheck
	os.Setenv("GC_SESSION_NAME", "gt-mayor") //nolint:errcheck
	os.Setenv("GC_ALIAS", "mayor")           //nolint:errcheck
	os.Setenv("GC_AGENT", "mayor")           //nolint:errcheck

	ids := bootCheckIdentities()
	// Should deduplicate: GC_ALIAS and GC_AGENT are both "mayor"
	require.Len(t, ids, 3)
	require.Contains(t, ids, "sess-123")
	require.Contains(t, ids, "gt-mayor")
	require.Contains(t, ids, "mayor")
}
