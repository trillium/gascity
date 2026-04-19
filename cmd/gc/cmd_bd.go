package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/fsys"
	"github.com/spf13/cobra"
)

func newBdCmd(stdout, stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bd [bd-args...]",
		Short: "Run bd in the correct rig directory",
		Long: `Run a bd command routed to the correct rig directory.

When beads belong to a rig (not the city root), bd must run from the
rig directory to find the correct .beads database. This command resolves
the rig automatically from the --rig flag or by detecting the bead prefix
in the arguments.

All arguments after "gc bd" are forwarded to bd unchanged.`,
		Example: `  gc bd --rig my-project list
  gc bd --rig my-project create "New task"
  gc bd show my-project-abc          # auto-detects rig from bead prefix
  gc bd list --rig my-project -s open`,
		DisableFlagParsing: true,
		RunE: func(_ *cobra.Command, args []string) error {
			if doBd(args, stdout, stderr) != 0 {
				return errExit
			}
			return nil
		},
	}
	return cmd
}

func doBd(args []string, stdout, stderr io.Writer) int {
	cityPath, err := resolveCity()
	if err != nil {
		fmt.Fprintf(stderr, "gc bd: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}

	cfg, err := config.Load(fsys.OSFS{}, filepath.Join(cityPath, "city.toml"))
	if err != nil {
		fmt.Fprintf(stderr, "gc bd: loading config: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}

	// Extract --rig from args (since DisableFlagParsing prevents cobra from
	// parsing it). The remaining args are forwarded to bd.
	rigName, bdArgs := extractRigFlag(args)

	dir := resolveBdDir(cfg, cityPath, rigName, bdArgs)

	// Scope guard: prevent mutating operations from accidentally targeting
	// the city-root store when rigs are configured.
	if err := validateBdScope(cfg, cityPath, dir, rigName, bdArgs); err != nil {
		fmt.Fprintf(stderr, "gc bd: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}

	bdPath, err := exec.LookPath("bd")
	if err != nil {
		fmt.Fprintln(stderr, "gc bd: bd not found in PATH") //nolint:errcheck // best-effort stderr
		return 1
	}

	cmd := exec.Command(bdPath, bdArgs...)
	cmd.Dir = dir
	cmd.Stdin = os.Stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	// Build env: strip BEADS_DIR so bd discovers .beads/ from cwd,
	// and inject rig-level Dolt host/port when configured.
	env := removeEnvKey(os.Environ(), "BEADS_DIR")
	if dir != cityPath {
		for _, r := range cfg.Rigs {
			rp := r.Path
			if !filepath.IsAbs(rp) {
				rp = filepath.Join(cityPath, rp)
			}
			if filepath.Clean(rp) == filepath.Clean(dir) {
				if r.DoltHost != "" {
					env = append(env, "BEADS_DOLT_HOST="+r.DoltHost)
				}
				if r.DoltPort != "" {
					env = append(env, "BEADS_DOLT_PORT="+r.DoltPort)
				}
				break
			}
		}
	}
	cmd.Env = env

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		fmt.Fprintf(stderr, "gc bd: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}
	return 0
}

// extractRigFlag extracts --rig <name> from the argument list and returns
// the rig name and remaining args. Also checks the global rigFlag set by
// cobra's persistent flag parsing (for "gc --rig foo bd list" syntax).
func extractRigFlag(args []string) (string, []string) {
	var rigName string
	var rest []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--rig" && i+1 < len(args) {
			rigName = args[i+1]
			i++ // skip value
			continue
		}
		if strings.HasPrefix(args[i], "--rig=") {
			rigName = strings.TrimPrefix(args[i], "--rig=")
			continue
		}
		rest = append(rest, args[i])
	}
	// Fall back to the global persistent flag if set.
	if rigName == "" && rigFlag != "" {
		rigName = rigFlag
	}
	return rigName, rest
}

// bdMutatingSubcommands lists bd subcommands that write to the bead store.
// Used by the scope guard to prevent accidental writes to the wrong store.
var bdMutatingSubcommands = map[string]bool{
	"assign":      true,
	"close":       true,
	"comment":     true,
	"create":      true,
	"create-form": true,
	"delete":      true,
	"edit":        true,
	"gate":        true,
	"label":       true,
	"link":        true,
	"merge-slot":  true,
	"note":        true,
	"priority":    true,
	"promote":     true,
	"q":           true,
	"reopen":      true,
	"rename":      true,
	"set-state":   true,
	"defer":       true,
	"undefer":     true,
}

// bdSubcommand extracts the first non-flag argument from bd args, which
// is the bd subcommand (e.g., "create", "list", "show").
func bdSubcommand(args []string) string {
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		return arg
	}
	return ""
}

// errRigScopeRequired is returned when a mutating bd operation would fall
// back to the city-root store but rigs are configured, indicating the user
// likely intended a rig-scoped store.
var errRigScopeRequired = fmt.Errorf("rigs are configured but no --rig flag provided; specify --rig <name> to target the correct bead store")

// validateBdScope checks whether a bd command is safe to run at the resolved
// directory. When the resolved dir is the city root (fallback), the subcommand
// is mutating, and rigs are configured, returns an error to prevent accidental
// writes to the wrong store.
//
// The guard does NOT fire when:
//   - An explicit --rig flag was provided (rigName is non-empty)
//   - The resolved dir is a rig directory (not the city root fallback)
//   - The subcommand is read-only (list, show, search, etc.)
//   - No rigs are configured (single-store city)
func validateBdScope(cfg *config.City, cityPath, resolvedDir, rigName string, args []string) error {
	// Explicit rig flag was given — user knows what they're doing.
	if rigName != "" {
		return nil
	}
	// Resolved to a rig directory, not the city root fallback.
	if filepath.Clean(resolvedDir) != filepath.Clean(cityPath) {
		return nil
	}
	// No rigs configured — single-store city, no ambiguity.
	if len(cfg.Rigs) == 0 {
		return nil
	}
	// Read-only subcommand — safe at any scope.
	sub := bdSubcommand(args)
	if !bdMutatingSubcommands[sub] {
		return nil
	}
	return fmt.Errorf("gc bd %s: %w", sub, errRigScopeRequired)
}

// resolveBdDir determines the working directory for a bd command.
// Priority: explicit rig name > bead prefix auto-detection > city root.
func resolveBdDir(cfg *config.City, cityPath, rigName string, args []string) string {
	if rigName != "" {
		for _, r := range cfg.Rigs {
			if strings.EqualFold(r.Name, rigName) {
				rp := r.Path
				if !filepath.IsAbs(rp) {
					rp = filepath.Join(cityPath, rp)
				}
				return rp
			}
		}
	}

	// Auto-detect from bead IDs in args.
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		if dir := rigDirForBead(cfg, arg); dir != "" {
			rp := dir
			if !filepath.IsAbs(rp) {
				rp = filepath.Join(cityPath, rp)
			}
			return rp
		}
	}

	return cityPath
}
