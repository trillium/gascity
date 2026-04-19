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

func TestExtractHQFlag(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantHQ   bool
		wantArgs []string
	}{
		{
			name:     "no hq flag",
			args:     []string{"list", "--limit", "5"},
			wantHQ:   false,
			wantArgs: []string{"list", "--limit", "5"},
		},
		{
			name:     "hq flag present",
			args:     []string{"--hq", "create", "New task"},
			wantHQ:   true,
			wantArgs: []string{"create", "New task"},
		},
		{
			name:     "hq flag at end",
			args:     []string{"create", "New task", "--hq"},
			wantHQ:   true,
			wantArgs: []string{"create", "New task"},
		},
		{
			name:     "empty args",
			args:     nil,
			wantHQ:   false,
			wantArgs: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotHQ, gotArgs := extractHQFlag(tt.args)
			if gotHQ != tt.wantHQ {
				t.Errorf("hq = %v, want %v", gotHQ, tt.wantHQ)
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

func TestIsMutatingBdSubcommand(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "create", args: []string{"create", "task title"}, want: true},
		{name: "update", args: []string{"update", "BL-42", "--notes", "done"}, want: true},
		{name: "close", args: []string{"close", "BL-42"}, want: true},
		{name: "list", args: []string{"list"}, want: false},
		{name: "show", args: []string{"show", "BL-42"}, want: false},
		{name: "search", args: []string{"search", "bug"}, want: false},
		{name: "ready", args: []string{"ready"}, want: false},
		{name: "empty", args: nil, want: false},
		{name: "flags before create", args: []string{"--type=task", "create", "title"}, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isMutatingBdSubcommand(tt.args)
			if got != tt.want {
				t.Errorf("isMutatingBdSubcommand(%v) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}

func TestCheckBdScopeGuard(t *testing.T) {
	cfgWithRigs := &config.City{
		Rigs: []config.Rig{
			{Name: "frontend", Path: "/projects/frontend"},
			{Name: "backend", Path: "/projects/backend"},
		},
	}
	cfgNoRigs := &config.City{}

	t.Run("create blocked when rigs exist", func(t *testing.T) {
		msg := checkBdScopeGuard([]string{"create", "new task"}, cfgWithRigs)
		if msg == "" {
			t.Fatal("expected non-empty guard message for create with rigs")
		}
		if !strings.Contains(msg, "--rig") {
			t.Errorf("message should mention --rig: %q", msg)
		}
		if !strings.Contains(msg, "--hq") {
			t.Errorf("message should mention --hq: %q", msg)
		}
		if !strings.Contains(msg, "frontend") {
			t.Errorf("message should list rig names: %q", msg)
		}
	})

	t.Run("list allowed without rig", func(t *testing.T) {
		msg := checkBdScopeGuard([]string{"list"}, cfgWithRigs)
		if msg != "" {
			t.Errorf("list should not be blocked, got: %q", msg)
		}
	})

	t.Run("show allowed without rig", func(t *testing.T) {
		msg := checkBdScopeGuard([]string{"show", "BL-42"}, cfgWithRigs)
		if msg != "" {
			t.Errorf("show should not be blocked, got: %q", msg)
		}
	})

	t.Run("create allowed when no rigs", func(t *testing.T) {
		// Note: checkBdScopeGuard itself doesn't check len(rigs); the
		// caller does. But it still works with an empty rig list.
		msg := checkBdScopeGuard([]string{"create", "task"}, cfgNoRigs)
		if msg != "" {
			t.Errorf("create should be allowed with no rigs, got: %q", msg)
		}
	})

	t.Run("update blocked when rigs exist", func(t *testing.T) {
		msg := checkBdScopeGuard([]string{"update", "BL-42", "--notes", "context"}, cfgWithRigs)
		if msg == "" {
			t.Fatal("expected non-empty guard message for update with rigs")
		}
	})

	t.Run("close blocked when rigs exist", func(t *testing.T) {
		msg := checkBdScopeGuard([]string{"close", "BL-42"}, cfgWithRigs)
		if msg == "" {
			t.Fatal("expected non-empty guard message for close with rigs")
		}
	})
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
