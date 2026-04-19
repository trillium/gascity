package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/session"
	"github.com/spf13/cobra"
)

// newAttachCmd creates the top-level "gc attach" command.
// With no args, it presents an interactive picker over active sessions.
// With a session ID arg, it delegates to gc session attach logic.
func newAttachCmd(stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "attach [session-id-or-alias]",
		Short: "Attach to a session (interactive picker or direct)",
		Long: `Attach to a running or suspended chat session.

With no arguments, shows an interactive picker (fzf if available, else
a numbered list) over all active and suspended sessions.

With a session ID or alias, delegates directly to "gc session attach".`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if cmdAttach(args, stdout, stderr) != 0 {
				return errExit
			}
			return nil
		},
	}
}

// cmdAttach is the CLI entry point for "gc attach".
func cmdAttach(args []string, stdout, stderr io.Writer) int {
	// With an explicit session ID/alias, delegate directly.
	if len(args) == 1 {
		return cmdSessionAttach(args, stdout, stderr)
	}

	// No args: interactive picker.
	return cmdAttachInteractive(stdout, stderr)
}

// cmdAttachInteractive lists sessions and prompts the user to pick one,
// then attaches to it.
func cmdAttachInteractive(stdout, stderr io.Writer) int {
	store, code := openCityStore(stderr, "gc attach")
	if store == nil {
		return code
	}

	allSessionBeads, err := store.List(beads.ListQuery{
		Label: session.LabelSession,
		Sort:  beads.SortCreatedDesc,
	})
	if err != nil {
		fmt.Fprintf(stderr, "gc attach: listing sessions: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}

	sessionBeads := newSessionBeadSnapshot(allSessionBeads)
	sp := newSessionProviderFromContext(loadSessionProviderContext(), sessionBeads)
	mgr := newSessionManager(store, sp)
	listResult := mgr.ListFullFromBeads(allSessionBeads, "", "")
	sessions := listResult.Sessions

	// Filter to active/suspended sessions only (exclude closed).
	var pickable []session.Info
	for _, s := range sessions {
		if string(s.State) != "closed" && !s.Closed {
			pickable = append(pickable, s)
		}
	}

	if len(pickable) == 0 {
		fmt.Fprintln(stdout, "No active or suspended sessions found.") //nolint:errcheck // best-effort stdout
		return 0
	}

	// Build display lines: "id  template  state  title  age"
	lines := make([]string, len(pickable))
	for i, s := range pickable {
		state := string(s.State)
		if state == "" {
			state = "unknown"
		}
		title := s.Title
		if title == "" {
			title = "-"
		}
		if len(title) > 30 {
			title = title[:27] + "..."
		}
		age := formatDuration(time.Since(s.CreatedAt))
		lines[i] = fmt.Sprintf("%-12s  %-20s  %-10s  %-30s  %s",
			s.ID, s.Template, state, title, age)
	}

	// Try fzf first; fall back to numbered list.
	chosen, ok := pickSessionWithFzf(lines, pickable, stderr)
	if !ok {
		chosen, ok = pickSessionNumbered(lines, pickable, stderr)
		if !ok {
			return 1
		}
	}

	return cmdSessionAttach([]string{chosen.ID}, stdout, stderr)
}

// pickSessionWithFzf presents lines via fzf and returns the chosen session.
// Returns (session, true) on success, (zero, false) if fzf is unavailable or
// the user cancelled.
func pickSessionWithFzf(lines []string, sessions []session.Info, stderr io.Writer) (session.Info, bool) {
	fzfPath, err := exec.LookPath("fzf")
	if err != nil {
		return session.Info{}, false
	}

	cmd := exec.Command(fzfPath, "--no-sort", "--prompt=session> ", "--height=40%")
	cmd.Stdin = strings.NewReader(strings.Join(lines, "\n"))
	cmd.Stderr = os.Stderr // pass through fzf UI to the real stderr (terminal)

	out, err := cmd.Output()
	if err != nil {
		// User pressed Escape or fzf exited non-zero.
		return session.Info{}, false
	}

	chosen := strings.TrimSpace(string(out))
	if chosen == "" {
		return session.Info{}, false
	}

	// Match chosen line back to a session by ID prefix (first field).
	chosenID := strings.Fields(chosen)[0]
	for _, s := range sessions {
		if s.ID == chosenID {
			return s, true
		}
	}

	fmt.Fprintf(stderr, "gc attach: could not match selection %q to a session\n", chosenID) //nolint:errcheck // best-effort stderr
	return session.Info{}, false
}

// pickSessionNumbered prints a numbered list and reads a choice from stdin.
func pickSessionNumbered(lines []string, sessions []session.Info, stderr io.Writer) (session.Info, bool) {
	for i, l := range lines {
		fmt.Fprintf(stderr, "  %d) %s\n", i+1, l) //nolint:errcheck // best-effort stderr
	}
	fmt.Fprint(stderr, "Select session [1-", len(lines), "]: ") //nolint:errcheck // best-effort stderr

	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return session.Info{}, false
	}
	input := strings.TrimSpace(scanner.Text())
	if input == "" {
		return session.Info{}, false
	}

	var n int
	if _, err := fmt.Sscanf(input, "%d", &n); err != nil || n < 1 || n > len(sessions) {
		fmt.Fprintf(stderr, "gc attach: invalid selection %q\n", input) //nolint:errcheck // best-effort stderr
		return session.Info{}, false
	}
	return sessions[n-1], true
}

// cityTmuxSocketName returns the tmux socket name for the city. It matches the
// logic in providers.go: use session.socket if set, else city name.
func cityTmuxSocketName(cityPath string) string {
	cfg, err := loadCityConfig(cityPath)
	if err != nil {
		return filepath.Base(cityPath)
	}
	if cfg.Session.Socket != "" {
		return cfg.Session.Socket
	}
	if cfg.Workspace.Name != "" {
		return cfg.Workspace.Name
	}
	return filepath.Base(cityPath)
}
