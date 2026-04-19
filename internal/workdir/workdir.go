// Package workdir resolves agent working directories from config templates.
package workdir

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/gastownhall/gascity/internal/config"
)

// PathContext holds template variables for work_dir expansion.
type PathContext struct {
	Agent     string
	AgentBase string
	Rig       string
	RigRoot   string
	CityRoot  string
	CityName  string
}

// sessionPathContext holds the runtime-only variables for the second-pass
// work_dir expansion performed by ExpandWithSession.
type sessionPathContext struct {
	Session string
	Issue   string
}

// CityName returns the configured workspace name, or the city directory basename
// when workspace.name is unset.
func CityName(cityPath string, cfg *config.City) string {
	if cfg != nil && cfg.Workspace.Name != "" {
		return cfg.Workspace.Name
	}
	return filepath.Base(filepath.Clean(cityPath))
}

// ResolveDirPath returns an absolute path for dir, resolving relative paths
// against the city root.
func ResolveDirPath(cityPath, dir string) string {
	if dir == "" {
		return cityPath
	}
	if filepath.IsAbs(dir) {
		return dir
	}
	return filepath.Join(cityPath, dir)
}

// ConfiguredRigName returns the rig associated with an agent, preferring the
// legacy dir-as-rig convention and falling back to path matching.
func ConfiguredRigName(cityPath string, a config.Agent, rigs []config.Rig) string {
	if a.Dir == "" {
		return ""
	}
	for _, rig := range rigs {
		if a.Dir == rig.Name {
			return rig.Name
		}
	}
	abs := ResolveDirPath(cityPath, a.Dir)
	for _, rig := range rigs {
		if samePath(abs, rig.Path) {
			return rig.Name
		}
	}
	return ""
}

// RigRootForName returns the configured root path for rigName.
func RigRootForName(rigName string, rigs []config.Rig) string {
	for _, rig := range rigs {
		if rig.Name == rigName {
			return rig.Path
		}
	}
	return ""
}

// PathContextForQualifiedName builds template context for work_dir expansion.
func PathContextForQualifiedName(cityPath, cityName, qualifiedName string, a config.Agent, rigs []config.Rig) PathContext {
	rigName := ConfiguredRigName(cityPath, a, rigs)
	_, agentBase := config.ParseQualifiedName(qualifiedName)
	return PathContext{
		Agent:     qualifiedName,
		AgentBase: agentBase,
		Rig:       rigName,
		RigRoot:   RigRootForName(rigName, rigs),
		CityRoot:  cityPath,
		CityName:  cityName,
	}
}

// deferredPlaceholders maps runtime-only template variables to unique
// placeholder strings that survive the config-time expansion pass.
// The placeholders are restored by ExpandWithSession in the second pass.
var deferredPlaceholders = map[string]string{
	"{{.Session}}": "\x00gc_session\x00",
	"{{.Issue}}":   "\x00gc_issue\x00",
}

// ExpandTemplateStrict expands Go text/template placeholders in a work_dir
// string and returns an error when parsing or execution fails.
//
// The runtime-only variables {{.Session}} and {{.Issue}} are preserved through
// this pass by substituting them with internal placeholders before template
// execution; ExpandWithSession restores them in a second pass.
func ExpandTemplateStrict(spec string, ctx PathContext) (string, error) {
	if spec == "" || !strings.Contains(spec, "{{") {
		return spec, nil
	}
	// Temporarily replace runtime-only placeholders so they survive the
	// config-time expansion without causing missingkey=error failures.
	deferred := spec
	for marker, placeholder := range deferredPlaceholders {
		deferred = strings.ReplaceAll(deferred, marker, placeholder)
	}
	tmpl, err := template.New("workdir").Option("missingkey=error").Parse(deferred)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, ctx); err != nil {
		return "", err
	}
	result := buf.String()
	// Restore the runtime-only placeholders back to their template form so
	// that ExpandWithSession can expand them in a subsequent pass.
	for marker, placeholder := range deferredPlaceholders {
		result = strings.ReplaceAll(result, placeholder, marker)
	}
	return result, nil
}

// ExpandTemplate expands Go text/template placeholders in a work_dir string.
// On parse or execute error, the raw string is returned.
func ExpandTemplate(spec string, ctx PathContext) string {
	expanded, err := ExpandTemplateStrict(spec, ctx)
	if err != nil {
		return spec
	}
	return expanded
}

// ResolveWorkDirPathStrict returns the effective session working directory and
// surfaces work_dir template errors to callers that need to fail closed.
func ResolveWorkDirPathStrict(cityPath, cityName, qualifiedName string, a config.Agent, rigs []config.Rig) (string, error) {
	if a.WorkDir == "" {
		if rigName := ConfiguredRigName(cityPath, a, rigs); rigName != "" {
			if rigRoot := RigRootForName(rigName, rigs); rigRoot != "" {
				return ResolveDirPath(cityPath, rigRoot), nil
			}
		}
		return ResolveDirPath(cityPath, a.Dir), nil
	}
	ctx := PathContextForQualifiedName(cityPath, cityName, qualifiedName, a, rigs)
	expanded, err := ExpandTemplateStrict(a.WorkDir, ctx)
	if err != nil {
		return "", fmt.Errorf("expand work_dir %q: %w", a.WorkDir, err)
	}
	return ResolveDirPath(cityPath, expanded), nil
}

// ResolveWorkDirPath returns the effective session working directory for an
// agent. When work_dir is unset, rig-scoped agents continue to use their rig
// root for backward compatibility.
func ResolveWorkDirPath(cityPath, cityName, qualifiedName string, a config.Agent, rigs []config.Rig) string {
	path, err := ResolveWorkDirPathStrict(cityPath, cityName, qualifiedName, a, rigs)
	if err != nil {
		ctx := PathContextForQualifiedName(cityPath, cityName, qualifiedName, a, rigs)
		return ResolveDirPath(cityPath, ExpandTemplate(a.WorkDir, ctx))
	}
	return path
}

// ExpandWithSession performs a second-pass expansion of {{.Session}} and
// {{.Issue}} placeholders in an already-config-expanded work_dir string.
//
// This is intentionally a separate pass from the primary ExpandTemplateStrict
// expansion: config-time variables (Agent, Rig, RigRoot, etc.) are resolved
// once at template-load time, while session/issue IDs are only known at
// reconciliation time.
//
// The function is safe to call unconditionally:
//   - It is a no-op when spec contains neither "{{.Session}}" nor "{{.Issue}}".
//   - When sessionID or issueID is empty, the corresponding placeholder is
//     replaced with an empty string (consistent with the caller leaving the
//     field unset).
func ExpandWithSession(spec, sessionID, issueID string) string {
	if spec == "" {
		return spec
	}
	hasSession := strings.Contains(spec, "{{.Session}}")
	hasIssue := strings.Contains(spec, "{{.Issue}}")
	if !hasSession && !hasIssue {
		return spec
	}
	ctx := sessionPathContext{
		Session: sessionID,
		Issue:   issueID,
	}
	// missingkey=zero so that any other template markers remaining in spec
	// (which should not exist after the primary pass) produce empty strings
	// rather than errors — a safe fallback that preserves the rest of the path.
	tmpl, err := template.New("workdir_session").Option("missingkey=zero").Parse(spec)
	if err != nil {
		return spec
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, ctx); err != nil {
		return spec
	}
	return buf.String()
}

func samePath(a, b string) bool {
	return normalizePathForCompare(a) == normalizePathForCompare(b)
}

func normalizePathForCompare(path string) string {
	if path == "" {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	path = filepath.Clean(path)
	path = canonicalizeExistingPathPrefix(path)
	return filepath.Clean(path)
}

func canonicalizeExistingPathPrefix(path string) string {
	current := path
	var suffix []string
	for {
		if resolved, err := filepath.EvalSymlinks(current); err == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return resolved
		}
		parent := filepath.Dir(current)
		if parent == current {
			return path
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}
