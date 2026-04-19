package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/mail"
	"github.com/spf13/cobra"
)

// BootCheckResult is the JSON output of "gc boot-check".
type BootCheckResult struct {
	Hooked []beads.Bead   `json:"hooked"`
	Routed []beads.Bead   `json:"routed"`
	Ready  []beads.Bead   `json:"ready"`
	Mail   []mail.Message `json:"mail"`
	Rigs   []BootCheckRig `json:"rigs"`
}

// BootCheckRig is a rig entry in the boot-check output.
type BootCheckRig struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Suspended bool   `json:"suspended,omitempty"`
}

// BootCheckSource abstracts the data sources for boot-check, enabling
// testability without real stores.
type BootCheckSource interface {
	// Hooked returns in-progress beads assigned to any of the given identities.
	Hooked(ctx context.Context, identities []string) ([]beads.Bead, error)
	// Routed returns open unassigned beads routed to the agent.
	Routed(ctx context.Context, agentName string) ([]beads.Bead, error)
	// Ready returns all open beads.
	Ready(ctx context.Context) ([]beads.Bead, error)
	// Inbox returns unread mail for the given identities.
	Inbox(ctx context.Context, identities []string) ([]mail.Message, error)
	// Rigs returns the configured rigs.
	Rigs(ctx context.Context) ([]BootCheckRig, error)
}

func newBootCheckCmd(stdout, stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "boot-check",
		Short: "Fast parallel startup probe (JSON to stdout)",
		Long: `Runs all startup queries in parallel with a hard 5-second timeout.
Returns a JSON object with hooked, routed, ready, mail, and rigs arrays.
Each sub-query gets a 2-second timeout; the overall operation has a 5-second
timeout. Partial results are returned if some queries fail or time out.

Designed for agent boot sequences where the multi-step startup (gc mail inbox,
bd list, bd ready) causes cascading timeouts and wasted sessions.`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if cmdBootCheck(stdout, stderr) != 0 {
				return errExit
			}
			return nil
		},
	}
	return cmd
}

// cmdBootCheck is the CLI entry point for gc boot-check.
func cmdBootCheck(stdout, stderr io.Writer) int {
	cityPath, err := resolveCity()
	if err != nil {
		fmt.Fprintf(stderr, "gc boot-check: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}

	cfg, err := loadCityConfig(cityPath)
	if err != nil {
		fmt.Fprintf(stderr, "gc boot-check: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}

	// Open store once, shared across queries.
	store, err := openCityStoreAt(cityPath)
	if err != nil {
		fmt.Fprintf(stderr, "gc boot-check: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}

	mp := newMailProvider(store)
	src := &liveBootCheckSource{store: store, mp: mp, cfg: cfg}
	identities := bootCheckIdentities()

	agentName := bootCheckAgentName(cfg)

	result := doBootCheck(src, identities, agentName)
	return writeBootCheckResult(result, stdout, stderr)
}

// bootCheckIdentities returns the list of identities to check for hooked work,
// mirroring the identity resolution in EffectiveWorkQuery.
func bootCheckIdentities() []string {
	var ids []string
	seen := make(map[string]bool)
	for _, env := range []string{"GC_SESSION_ID", "GC_SESSION_NAME", "GC_ALIAS", "GC_AGENT"} {
		v := strings.TrimSpace(os.Getenv(env))
		if v != "" && !seen[v] {
			ids = append(ids, v)
			seen[v] = true
		}
	}
	return ids
}

// bootCheckAgentName returns the agent name for routed-to queries.
func bootCheckAgentName(cfg *config.City) string {
	if alias := strings.TrimSpace(os.Getenv("GC_ALIAS")); alias != "" {
		// Resolve to qualified name if possible.
		a, ok := resolveAgentIdentity(cfg, alias, currentRigContext(cfg))
		if ok {
			return a.QualifiedName()
		}
		return alias
	}
	if agent := strings.TrimSpace(os.Getenv("GC_AGENT")); agent != "" {
		a, ok := resolveAgentIdentity(cfg, agent, currentRigContext(cfg))
		if ok {
			return a.QualifiedName()
		}
		return agent
	}
	return ""
}

// doBootCheck runs all queries in parallel and returns partial results on timeout.
func doBootCheck(src BootCheckSource, identities []string, agentName string) BootCheckResult {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var (
		mu     sync.Mutex
		result BootCheckResult
		wg     sync.WaitGroup
	)

	// Initialize all slices to empty so JSON output is [] not null.
	result.Hooked = []beads.Bead{}
	result.Routed = []beads.Bead{}
	result.Ready = []beads.Bead{}
	result.Mail = []mail.Message{}
	result.Rigs = []BootCheckRig{}

	// Query 1: hooked work (in_progress assigned to caller).
	if len(identities) > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			qctx, qcancel := context.WithTimeout(ctx, 2*time.Second)
			defer qcancel()
			if bs, err := src.Hooked(qctx, identities); err == nil && len(bs) > 0 {
				mu.Lock()
				result.Hooked = bs
				mu.Unlock()
			}
		}()
	}

	// Query 2: routed work (routed to this agent, unassigned).
	if agentName != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			qctx, qcancel := context.WithTimeout(ctx, 2*time.Second)
			defer qcancel()
			if bs, err := src.Routed(qctx, agentName); err == nil && len(bs) > 0 {
				mu.Lock()
				result.Routed = bs
				mu.Unlock()
			}
		}()
	}

	// Query 3: ready queue.
	wg.Add(1)
	go func() {
		defer wg.Done()
		qctx, qcancel := context.WithTimeout(ctx, 2*time.Second)
		defer qcancel()
		if bs, err := src.Ready(qctx); err == nil && len(bs) > 0 {
			mu.Lock()
			result.Ready = bs
			mu.Unlock()
		}
	}()

	// Query 4: mail (best-effort, 2s sub-timeout).
	if len(identities) > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			qctx, qcancel := context.WithTimeout(ctx, 2*time.Second)
			defer qcancel()
			if msgs, err := src.Inbox(qctx, identities); err == nil && len(msgs) > 0 {
				mu.Lock()
				result.Mail = msgs
				mu.Unlock()
			}
		}()
	}

	// Query 5: rigs (from config, fast).
	wg.Add(1)
	go func() {
		defer wg.Done()
		qctx, qcancel := context.WithTimeout(ctx, 2*time.Second)
		defer qcancel()
		if rigs, err := src.Rigs(qctx); err == nil && len(rigs) > 0 {
			mu.Lock()
			result.Rigs = rigs
			mu.Unlock()
		}
	}()

	// Wait for all goroutines or overall timeout.
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}

	return result
}

// writeBootCheckResult marshals the result to JSON and writes to stdout.
func writeBootCheckResult(result BootCheckResult, stdout, stderr io.Writer) int {
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "gc boot-check: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}
	fmt.Fprintln(stdout, string(data)) //nolint:errcheck // best-effort stdout
	return 0
}

// liveBootCheckSource queries real beads.Store and mail.Provider.
type liveBootCheckSource struct {
	store beads.Store
	mp    mail.Provider
	cfg   *config.City
}

func (s *liveBootCheckSource) Hooked(_ context.Context, identities []string) ([]beads.Bead, error) {
	var all []beads.Bead
	seen := make(map[string]bool)
	for _, id := range identities {
		bs, err := s.store.ListByAssignee(id, "in_progress", 0)
		if err != nil {
			continue
		}
		for _, b := range bs {
			if !seen[b.ID] {
				seen[b.ID] = true
				all = append(all, b)
			}
		}
	}
	return all, nil
}

func (s *liveBootCheckSource) Routed(_ context.Context, agentName string) ([]beads.Bead, error) {
	// Query beads with gc.routed_to metadata matching the agent name.
	// Use ListByLabel with the routing label convention, or fall back to
	// listing open beads and filtering.
	bs, err := s.store.ListOpen()
	if err != nil {
		return nil, err
	}
	var routed []beads.Bead
	for _, b := range bs {
		if b.Metadata["gc.routed_to"] == agentName && b.Assignee == "" {
			routed = append(routed, b)
		}
	}
	return routed, nil
}

func (s *liveBootCheckSource) Ready(_ context.Context) ([]beads.Bead, error) {
	return s.store.Ready()
}

func (s *liveBootCheckSource) Inbox(_ context.Context, identities []string) ([]mail.Message, error) {
	var all []mail.Message
	seen := make(map[string]bool)
	for _, id := range identities {
		msgs, err := s.mp.Inbox(id)
		if err != nil {
			continue
		}
		for _, m := range msgs {
			if !seen[m.ID] {
				seen[m.ID] = true
				all = append(all, m)
			}
		}
	}
	return all, nil
}

func (s *liveBootCheckSource) Rigs(_ context.Context) ([]BootCheckRig, error) {
	var rigs []BootCheckRig
	for _, r := range s.cfg.Rigs {
		rigs = append(rigs, BootCheckRig{
			Name:      r.Name,
			Path:      r.Path,
			Suspended: r.Suspended,
		})
	}
	return rigs, nil
}
