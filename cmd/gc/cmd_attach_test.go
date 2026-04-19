package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/session"
)

// setupAttachTestCity creates a temporary city with a bead store and session
// beads for testing gc attach. Returns cleanup function.
func setupAttachTestCity(t *testing.T, sessions []session.Info) (cleanup func()) {
	t.Helper()

	dir := t.TempDir()
	cityToml := filepath.Join(dir, "city.toml")
	if err := os.WriteFile(cityToml, []byte("[workspace]\nname = \"test-city\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gcDir := filepath.Join(dir, ".gc")
	if err := os.MkdirAll(gcDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Set city flag so resolveCity finds our test city.
	oldCity := cityFlag
	cityFlag = dir
	// Force file-based beads for test.
	oldBeads := os.Getenv("GC_BEADS")
	os.Setenv("GC_BEADS", "file")

	store, code := openCityStore(os.Stderr, "test")
	if store == nil {
		t.Fatalf("failed to open city store, code=%d", code)
	}

	// Create session beads.
	for _, s := range sessions {
		meta := map[string]string{
			"state":    string(s.State),
			"template": s.Template,
		}
		if s.Alias != "" {
			meta["alias"] = s.Alias
		}
		if s.Title != "" {
			meta["title"] = s.Title
		}
		b := beads.Bead{
			ID:       s.ID,
			Type:     session.BeadType,
			Title:    s.Title,
			Status:   "open",
			Labels:   []string{session.LabelSession},
			Metadata: meta,
		}
		if _, err := store.Create(b); err != nil {
			t.Fatalf("creating session bead %s: %v", s.ID, err)
		}
	}

	return func() {
		cityFlag = oldCity
		os.Setenv("GC_BEADS", oldBeads)
	}
}

func TestCmdAttach_WithArg(t *testing.T) {
	// With an arg, cmdAttach delegates to cmdSessionAttach which needs
	// a full city + session. We test that it passes through the arg by
	// verifying the error message contains the session ID we passed.
	dir := t.TempDir()
	cityToml := filepath.Join(dir, "city.toml")
	if err := os.WriteFile(cityToml, []byte("[workspace]\nname = \"test-city\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gcDir := filepath.Join(dir, ".gc")
	if err := os.MkdirAll(gcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldCity := cityFlag
	cityFlag = dir
	oldBeads := os.Getenv("GC_BEADS")
	os.Setenv("GC_BEADS", "file")
	defer func() {
		cityFlag = oldCity
		os.Setenv("GC_BEADS", oldBeads)
	}()

	var stdout, stderr bytes.Buffer
	code := cmdAttach([]string{"nonexistent-session"}, &stdout, &stderr, strings.NewReader(""))
	if code == 0 {
		t.Fatal("expected non-zero exit for nonexistent session")
	}
	// The error should mention the session we tried to attach to.
	if !strings.Contains(stderr.String(), "nonexistent-session") {
		t.Errorf("stderr should mention session ID, got: %s", stderr.String())
	}
}

func TestCmdAttach_NoSessions(t *testing.T) {
	cleanup := setupAttachTestCity(t, nil)
	defer cleanup()

	var stdout, stderr bytes.Buffer
	code := cmdAttach(nil, &stdout, &stderr, strings.NewReader(""))
	if code == 0 {
		t.Fatal("expected non-zero exit when no sessions exist")
	}
	if !strings.Contains(stderr.String(), "no active or suspended sessions") {
		t.Errorf("stderr should say no sessions, got: %s", stderr.String())
	}
}

func TestCmdAttach_ListsSessions(t *testing.T) {
	sessions := []session.Info{
		{ID: "gc-1", Template: "helper", State: session.StateActive, Title: "debugging auth"},
		{ID: "gc-2", Template: "worker", State: session.StateActive, Alias: "sky"},
	}
	cleanup := setupAttachTestCity(t, sessions)
	defer cleanup()

	var stdout, stderr bytes.Buffer
	// Simulate choosing nothing (empty input).
	code := cmdAttach(nil, &stdout, &stderr, strings.NewReader(""))
	// Should fail because no input.
	if code == 0 {
		t.Fatal("expected non-zero exit with no input")
	}

	// But stdout should show the session list.
	out := stdout.String()
	if !strings.Contains(out, "gc-1") {
		t.Errorf("output should contain gc-1, got: %s", out)
	}
	if !strings.Contains(out, "gc-2") {
		t.Errorf("output should contain gc-2, got: %s", out)
	}
	if !strings.Contains(out, "helper") {
		t.Errorf("output should contain template name, got: %s", out)
	}
	if !strings.Contains(out, "(sky)") {
		t.Errorf("output should contain alias in parens, got: %s", out)
	}
	if !strings.Contains(out, "debugging auth") {
		t.Errorf("output should contain title, got: %s", out)
	}
}

func TestCmdAttach_InvalidSelection(t *testing.T) {
	sessions := []session.Info{
		{ID: "gc-1", Template: "helper", State: session.StateActive},
	}
	cleanup := setupAttachTestCity(t, sessions)
	defer cleanup()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"zero", "0\n", "invalid selection"},
		{"too high", "5\n", "invalid selection"},
		{"non-numeric", "abc\n", "invalid selection"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := cmdAttach(nil, &stdout, &stderr, strings.NewReader(tt.input))
			if code == 0 {
				t.Fatal("expected non-zero exit")
			}
			if !strings.Contains(stderr.String(), tt.want) {
				t.Errorf("stderr should contain %q, got: %s", tt.want, stderr.String())
			}
		})
	}
}

func TestCmdAttach_ClosedSessionsExcluded(t *testing.T) {
	// Only active+suspended should appear, not closed.
	sessions := []session.Info{
		{ID: "gc-1", Template: "helper", State: session.StateActive},
	}
	cleanup := setupAttachTestCity(t, sessions)
	defer cleanup()

	// Also create a closed session bead directly.
	store, _ := openCityStore(os.Stderr, "test")
	closedBead := beads.Bead{
		ID:     "gc-closed",
		Type:   session.BeadType,
		Status: "closed",
		Labels: []string{session.LabelSession},
		Metadata: map[string]string{
			"state":    "closed",
			"template": "worker",
		},
	}
	if _, err := store.Create(closedBead); err != nil {
		t.Fatalf("creating closed bead: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := cmdAttach(nil, &stdout, &stderr, strings.NewReader(""))
	// Will fail (no input), but check output only shows gc-1.
	_ = code
	out := stdout.String()
	if !strings.Contains(out, "gc-1") {
		t.Errorf("should show active session gc-1, got: %s", out)
	}
	if strings.Contains(out, "gc-closed") {
		t.Errorf("should NOT show closed session, got: %s", out)
	}
}
