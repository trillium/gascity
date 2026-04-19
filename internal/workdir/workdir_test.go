package workdir

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gastownhall/gascity/internal/config"
)

func intPtr(n int) *int { return &n }

func TestResolveWorkDirPathUsesWorkDirTemplate(t *testing.T) {
	cityPath := t.TempDir()
	cityName := "gastown"
	cfg := &config.City{
		Workspace: config.Workspace{Name: cityName},
		Rigs:      []config.Rig{{Name: "demo", Path: filepath.Join(cityPath, "repos", "demo")}},
	}
	agent := config.Agent{
		Name:    "refinery",
		Dir:     "demo",
		WorkDir: ".gc/worktrees/{{.Rig}}/{{.AgentBase}}",
	}

	got := ResolveWorkDirPath(cityPath, cityName, "demo/refinery", agent, cfg.Rigs)
	want := filepath.Join(cityPath, ".gc", "worktrees", "demo", "refinery")
	if got != want {
		t.Fatalf("ResolveWorkDirPath() = %q, want %q", got, want)
	}
}

func TestResolveWorkDirPathDefaultsRigScopedAgentsToRigRoot(t *testing.T) {
	cityPath := t.TempDir()
	rigRoot := filepath.Join(t.TempDir(), "demo-repo")
	got := ResolveWorkDirPath(cityPath, "gastown", "demo/refinery", config.Agent{
		Name: "refinery",
		Dir:  "demo",
	}, []config.Rig{{Name: "demo", Path: rigRoot}})
	if got != rigRoot {
		t.Fatalf("ResolveWorkDirPath() = %q, want %q", got, rigRoot)
	}
}

func TestResolveWorkDirPathUsesPoolInstanceBase(t *testing.T) {
	cityPath := t.TempDir()
	got := ResolveWorkDirPath(cityPath, "gastown", "demo/polecat-2", config.Agent{
		Name:              "polecat",
		Dir:               "demo",
		WorkDir:           ".gc/worktrees/{{.Rig}}/polecats/{{.AgentBase}}",
		MinActiveSessions: intPtr(0), MaxActiveSessions: intPtr(3),
	}, []config.Rig{{Name: "demo", Path: filepath.Join(cityPath, "repos", "demo")}})
	want := filepath.Join(cityPath, ".gc", "worktrees", "demo", "polecats", "polecat-2")
	if got != want {
		t.Fatalf("ResolveWorkDirPath() = %q, want %q", got, want)
	}
}

func TestCityNameFallsBackToCityDirBase(t *testing.T) {
	cityPath := filepath.Join(t.TempDir(), "city-root")
	got := CityName(cityPath, &config.City{})
	if got != "city-root" {
		t.Fatalf("CityName() = %q, want %q", got, "city-root")
	}
}

func TestResolveWorkDirPathStrictRejectsInvalidTemplate(t *testing.T) {
	cityPath := t.TempDir()
	_, err := ResolveWorkDirPathStrict(cityPath, "gastown", "demo/refinery", config.Agent{
		Name:    "refinery",
		Dir:     "demo",
		WorkDir: ".gc/worktrees/{{.RigName}}/refinery",
	}, []config.Rig{{Name: "demo", Path: filepath.Join(cityPath, "repos", "demo")}})
	if err == nil {
		t.Fatal("ResolveWorkDirPathStrict() error = nil, want invalid template error")
	}
}

func TestExpandWithSessionExpandsSessionPlaceholder(t *testing.T) {
	got := ExpandWithSession("/worktrees/{{.Session}}", "ct-wm5", "")
	want := "/worktrees/ct-wm5"
	if got != want {
		t.Fatalf("ExpandWithSession() = %q, want %q", got, want)
	}
}

func TestExpandWithSessionExpandsIssuePlaceholder(t *testing.T) {
	got := ExpandWithSession("/worktrees/{{.Issue}}", "", "ba-56ry")
	want := "/worktrees/ba-56ry"
	if got != want {
		t.Fatalf("ExpandWithSession() = %q, want %q", got, want)
	}
}

func TestExpandWithSessionExpandsBothPlaceholders(t *testing.T) {
	got := ExpandWithSession("/worktrees/{{.Session}}/{{.Issue}}", "ct-wm5", "ba-56ry")
	want := "/worktrees/ct-wm5/ba-56ry"
	if got != want {
		t.Fatalf("ExpandWithSession() = %q, want %q", got, want)
	}
}

func TestExpandWithSessionIsNoOpWhenNoPlaceholders(t *testing.T) {
	spec := "/worktrees/myrig/agent"
	got := ExpandWithSession(spec, "ct-wm5", "ba-56ry")
	if got != spec {
		t.Fatalf("ExpandWithSession() = %q, want %q (unchanged)", got, spec)
	}
}

func TestExpandWithSessionIsNoOpOnEmptySpec(t *testing.T) {
	got := ExpandWithSession("", "ct-wm5", "ba-56ry")
	if got != "" {
		t.Fatalf("ExpandWithSession() = %q, want empty string", got)
	}
}

func TestExpandWithSessionEmptySessionIDExpandsToEmpty(t *testing.T) {
	got := ExpandWithSession("/worktrees/{{.Session}}", "", "")
	want := "/worktrees/"
	if got != want {
		t.Fatalf("ExpandWithSession() = %q, want %q", got, want)
	}
}

func TestExpandTemplateStrictPreservesSessionPlaceholderForSecondPass(t *testing.T) {
	cityPath := t.TempDir()
	cityName := "gastown"
	rigPath := filepath.Join(cityPath, "repos", "demo")
	ctx := PathContextForQualifiedName(cityPath, cityName, "demo/refinery", config.Agent{
		Name: "refinery",
		Dir:  "demo",
	}, []config.Rig{{Name: "demo", Path: rigPath}})

	// {{.Session}} should survive the first pass as-is.
	spec := "{{.RigRoot}}/worktrees/{{.Session}}"
	expanded, err := ExpandTemplateStrict(spec, ctx)
	if err != nil {
		t.Fatalf("ExpandTemplateStrict() unexpected error: %v", err)
	}
	// RigRoot should be expanded; {{.Session}} should be preserved.
	want := rigPath + "/worktrees/{{.Session}}"
	if expanded != want {
		t.Fatalf("ExpandTemplateStrict() = %q, want %q", expanded, want)
	}

	// Second pass should then expand {{.Session}}.
	final := ExpandWithSession(expanded, "ct-wm5", "")
	wantFinal := rigPath + "/worktrees/ct-wm5"
	if final != wantFinal {
		t.Fatalf("ExpandWithSession() after first pass = %q, want %q", final, wantFinal)
	}
}

func TestExpandTemplateStrictStillRejectsUnknownNonDeferredKeys(t *testing.T) {
	cityPath := t.TempDir()
	ctx := PathContextForQualifiedName(cityPath, "gastown", "demo/refinery", config.Agent{
		Name: "refinery",
		Dir:  "demo",
	}, nil)

	// {{.RigName}} is not a valid PathContext field — should still fail.
	_, err := ExpandTemplateStrict("{{.RigName}}/worktrees", ctx)
	if err == nil {
		t.Fatal("ExpandTemplateStrict() error = nil, want error for unknown key")
	}
}

func TestResolveWorkDirPathStrictWithSessionPlaceholder(t *testing.T) {
	cityPath := t.TempDir()
	cityName := "gastown"
	rigPath := filepath.Join(cityPath, "repos", "demo")

	// A config work_dir with {{.Session}} should succeed at first pass,
	// returning a path that still contains the {{.Session}} marker.
	path, err := ResolveWorkDirPathStrict(cityPath, cityName, "demo/refinery", config.Agent{
		Name:    "refinery",
		Dir:     "demo",
		WorkDir: "{{.RigRoot}}/worktrees/{{.Session}}",
	}, []config.Rig{{Name: "demo", Path: rigPath}})
	if err != nil {
		t.Fatalf("ResolveWorkDirPathStrict() unexpected error: %v", err)
	}
	// RigRoot is absolute, so ResolveDirPath returns it directly.
	want := rigPath + "/worktrees/{{.Session}}"
	if path != want {
		t.Fatalf("ResolveWorkDirPathStrict() = %q, want %q", path, want)
	}
}

func TestConfiguredRigNameMatchesSymlinkAliasPath(t *testing.T) {
	root := t.TempDir()
	realRoot := filepath.Join(root, "real")
	rigPath := filepath.Join(realRoot, "demo")
	if err := os.MkdirAll(rigPath, 0o755); err != nil {
		t.Fatal(err)
	}
	aliasRoot := filepath.Join(root, "alias")
	if err := os.Symlink(realRoot, aliasRoot); err != nil {
		t.Skipf("symlink setup unavailable: %v", err)
	}

	aliasRigPath := filepath.Join(aliasRoot, "demo")
	got := ConfiguredRigName(t.TempDir(), config.Agent{
		Name: "worker",
		Dir:  aliasRigPath,
	}, []config.Rig{{Name: "demo", Path: rigPath}})
	if got != "demo" {
		t.Fatalf("ConfiguredRigName() = %q, want %q", got, "demo")
	}
}
