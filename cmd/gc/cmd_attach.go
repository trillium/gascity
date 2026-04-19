package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// newAttachCmd creates the top-level "gc attach" command, a convenience
// alias for "gc session attach". With an argument it delegates directly;
// without one it presents an interactive picker of active sessions.
func newAttachCmd(stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "attach [session-id-or-alias]",
		Short: "Attach to a chat session (shortcut for gc session attach)",
		Long: `Attach to a running or suspended chat session.

With an argument, delegates directly to "gc session attach <id>".

Without arguments, lists active and suspended sessions and lets you
pick one interactively.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if cmdAttach(args, stdout, stderr, os.Stdin) != 0 {
				return errExit
			}
			return nil
		},
	}
}

// cmdAttach is the CLI entry point for "gc attach".
func cmdAttach(args []string, stdout, stderr io.Writer, stdin io.Reader) int {
	// With an argument, delegate directly to session attach.
	if len(args) > 0 {
		return cmdSessionAttach(args, stdout, stderr)
	}

	// No argument — interactive picker.
	store, code := openCityStore(stderr, "gc attach")
	if store == nil {
		return code
	}

	sp := newSessionProvider()
	mgr := newSessionManager(store, sp)

	// List active and suspended sessions (not closed).
	sessions, err := mgr.List("active,suspended", "")
	if err != nil {
		fmt.Fprintf(stderr, "gc attach: listing sessions: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}

	if len(sessions) == 0 {
		fmt.Fprintln(stderr, "gc attach: no active or suspended sessions") //nolint:errcheck // best-effort stderr
		return 1
	}

	// Display numbered list.
	for i, s := range sessions {
		label := s.ID
		if s.Alias != "" {
			label = fmt.Sprintf("%s (%s)", s.ID, s.Alias)
		}
		title := s.Title
		if title == "" {
			title = "-"
		}
		if len(title) > 30 {
			title = title[:27] + "..."
		}
		fmt.Fprintf(stdout, "  %d) %s  %s  [%s]  %s\n", i+1, label, s.Template, s.State, title) //nolint:errcheck // best-effort stdout
	}

	fmt.Fprintf(stdout, "Select session [1-%d]: ", len(sessions)) //nolint:errcheck // best-effort stdout

	scanner := bufio.NewScanner(stdin)
	if !scanner.Scan() {
		fmt.Fprintln(stderr, "gc attach: no input") //nolint:errcheck // best-effort stderr
		return 1
	}
	input := strings.TrimSpace(scanner.Text())
	if input == "" {
		fmt.Fprintln(stderr, "gc attach: no selection") //nolint:errcheck // best-effort stderr
		return 1
	}

	choice, err := strconv.Atoi(input)
	if err != nil || choice < 1 || choice > len(sessions) {
		fmt.Fprintf(stderr, "gc attach: invalid selection %q (expected 1-%d)\n", input, len(sessions)) //nolint:errcheck // best-effort stderr
		return 1
	}

	selected := sessions[choice-1]
	return cmdSessionAttach([]string{selected.ID}, stdout, stderr)
}
