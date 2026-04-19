package main

import (
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/config"
)

func TestExtractRigFlag(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantRig  string
		wantArgs []string
	}{
		{
			name:     "no rig flag",
			args:     []string{"list", "--limit", "5"},
			wantRig:  "",
			wantArgs: []string{"list", "--limit", "5"},
		},
		{
			name:     "rig flag with space",
			args:     []string{"--rig", "myproject", "list"},
			wantRig:  "myproject",
			wantArgs: []string{"list"},
		},
		{
			name:     "rig flag with equals",
			args:     []string{"--rig=myproject", "list"},
			wantRig:  "myproject",
			wantArgs: []string{"list"},
		},
		{
			name:     "rig flag in middle",
			args:     []string{"show", "--rig", "myproject", "BL-42"},
			wantRig:  "myproject",
			wantArgs: []string{"show", "BL-42"},
		},
		{
			name:     "empty args",
			args:     nil,
			wantRig:  "",
			wantArgs: nil,
		},
		{
			name:     "rig flag at end missing value",
			args:     []string{"list", "--rig"},
			wantRig:  "",
			wantArgs: []string{"list", "--rig"},
		},
	}

	// Save and restore global rigFlag.
	origRigFlag := rigFlag
	defer func() { rigFlag = origRigFlag }()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rigFlag = "" // reset global
			gotRig, gotArgs := extractRigFlag(tt.args)
			if gotRig != tt.wantRig {
				t.Errorf("rig = %q, want %q", gotRig, tt.wantRig)
			}
			if len(gotArgs) != len(tt.wantArgs) {
				t.Fatalf("args len = %d, want %d; got %v", len(gotArgs), len(tt.wantArgs), gotArgs)
			}
			for i := range gotArgs {
				if gotArgs[i] != tt.wantArgs[i] {
					t.Errorf("args[%d] = %q, want %q", i, gotArgs[i], tt.wantArgs[i])
				}
			}
		})
	}
}

func TestExtractRigFlagFallsBackToGlobal(t *testing.T) {
	origRigFlag := rigFlag
	defer func() { rigFlag = origRigFlag }()

	rigFlag = "from-global"
	gotRig, gotArgs := extractRigFlag([]string{"list"})
	if gotRig != "from-global" {
		t.Errorf("rig = %q, want %q", gotRig, "from-global")
	}
	if len(gotArgs) != 1 || gotArgs[0] != "list" {
		t.Errorf("args = %v, want [list]", gotArgs)
	}
}

func TestBdSubcommand(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"simple", []string{"create", "title"}, "create"},
		{"with flags before", []string{"--verbose", "list"}, "list"},
		{"empty", nil, ""},
		{"only flags", []string{"--help"}, ""},
		{"show with bead id", []string{"show", "gc-abc"}, "show"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := bdSubcommand(tt.args)
			if got != tt.want {
				t.Errorf("bdSubcommand(%v) = %q, want %q", tt.args, got, tt.want)
			}
		})
	}
}

func TestValidateBdScope(t *testing.T) {
	cfgWithRigs := &config.City{
		Rigs: []config.Rig{
			{Name: "wren", Path: "/projects/wren", Prefix: "wr"},
		},
	}
	cfgNoRigs := &config.City{}

	tests := []struct {
		name        string
		cfg         *config.City
		cityPath    string
		resolvedDir string
		rigName     string
		args        []string
		wantErr     bool
	}{
		{
			name:        "mutating at city root with rigs — blocked",
			cfg:         cfgWithRigs,
			cityPath:    "/city",
			resolvedDir: "/city",
			rigName:     "",
			args:        []string{"create", "new task"},
			wantErr:     true,
		},
		{
			name:        "mutating at city root no rigs — allowed",
			cfg:         cfgNoRigs,
			cityPath:    "/city",
			resolvedDir: "/city",
			rigName:     "",
			args:        []string{"create", "new task"},
			wantErr:     false,
		},
		{
			name:        "mutating at rig dir — allowed",
			cfg:         cfgWithRigs,
			cityPath:    "/city",
			resolvedDir: "/projects/wren",
			rigName:     "",
			args:        []string{"create", "new task"},
			wantErr:     false,
		},
		{
			name:        "mutating with explicit rig flag — allowed",
			cfg:         cfgWithRigs,
			cityPath:    "/city",
			resolvedDir: "/city",
			rigName:     "wren",
			args:        []string{"create", "new task"},
			wantErr:     false,
		},
		{
			name:        "read-only at city root with rigs — allowed",
			cfg:         cfgWithRigs,
			cityPath:    "/city",
			resolvedDir: "/city",
			rigName:     "",
			args:        []string{"list"},
			wantErr:     false,
		},
		{
			name:        "show at city root with rigs — allowed",
			cfg:         cfgWithRigs,
			cityPath:    "/city",
			resolvedDir: "/city",
			rigName:     "",
			args:        []string{"show", "wr-abc"},
			wantErr:     false,
		},
		{
			name:        "close at city root with rigs — blocked",
			cfg:         cfgWithRigs,
			cityPath:    "/city",
			resolvedDir: "/city",
			rigName:     "",
			args:        []string{"close", "wr-abc"},
			wantErr:     true,
		},
		{
			name:        "note at city root with rigs — blocked",
			cfg:         cfgWithRigs,
			cityPath:    "/city",
			resolvedDir: "/city",
			rigName:     "",
			args:        []string{"note", "wr-abc", "some note"},
			wantErr:     true,
		},
		{
			name:        "q (quick create) at city root with rigs — blocked",
			cfg:         cfgWithRigs,
			cityPath:    "/city",
			resolvedDir: "/city",
			rigName:     "",
			args:        []string{"q", "quick task"},
			wantErr:     true,
		},
		{
			name:        "search at city root with rigs — allowed",
			cfg:         cfgWithRigs,
			cityPath:    "/city",
			resolvedDir: "/city",
			rigName:     "",
			args:        []string{"search", "keyword"},
			wantErr:     false,
		},
		{
			name:        "no args at city root with rigs — allowed",
			cfg:         cfgWithRigs,
			cityPath:    "/city",
			resolvedDir: "/city",
			rigName:     "",
			args:        nil,
			wantErr:     false,
		},
		{
			name:        "ready at city root with rigs — allowed",
			cfg:         cfgWithRigs,
			cityPath:    "/city",
			resolvedDir: "/city",
			rigName:     "",
			args:        []string{"ready"},
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateBdScope(tt.cfg, tt.cityPath, tt.resolvedDir, tt.rigName, tt.args)
			if tt.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateBdScope_ErrorMessage(t *testing.T) {
	cfg := &config.City{
		Rigs: []config.Rig{{Name: "app", Path: "/app"}},
	}
	err := validateBdScope(cfg, "/city", "/city", "", []string{"create", "task"})
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "create") {
		t.Errorf("error should mention subcommand, got: %s", msg)
	}
	if !strings.Contains(msg, "--rig") {
		t.Errorf("error should mention --rig flag, got: %s", msg)
	}
}

func TestResolveBdDir(t *testing.T) {
	cfg := &config.City{
		Rigs: []config.Rig{
			{Name: "wren", Path: "/projects/wren", Prefix: "projectwrenunity"},
			{Name: "gascity", Path: "/projects/gascity"},
		},
	}

	tests := []struct {
		name    string
		rigName string
		args    []string
		wantDir string
	}{
		{
			name:    "explicit rig name",
			rigName: "wren",
			args:    []string{"list"},
			wantDir: "/projects/wren",
		},
		{
			name:    "explicit rig name case insensitive",
			rigName: "Wren",
			args:    []string{"list"},
			wantDir: "/projects/wren",
		},
		{
			name:    "auto-detect from bead prefix",
			rigName: "",
			args:    []string{"show", "projectwrenunity-0xk"},
			wantDir: "/projects/wren",
		},
		{
			name:    "no rig falls back to city",
			rigName: "",
			args:    []string{"list"},
			wantDir: "/city",
		},
		{
			name:    "unknown rig name falls back to auto-detect",
			rigName: "nonexistent",
			args:    []string{"show", "projectwrenunity-abc"},
			wantDir: "/projects/wren",
		},
		{
			name:    "skips flags during auto-detect",
			rigName: "",
			args:    []string{"list", "--status", "open"},
			wantDir: "/city",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveBdDir(cfg, "/city", tt.rigName, tt.args)
			if got != tt.wantDir {
				t.Errorf("resolveBdDir() = %q, want %q", got, tt.wantDir)
			}
		})
	}
}
