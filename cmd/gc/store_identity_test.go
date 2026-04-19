package main

import (
	"path/filepath"
	"testing"

	beadsexec "github.com/gastownhall/gascity/internal/beads/exec"
)

// TestOpenStoreAtForCity_execSetsBeadsDir verifies that the exec provider
// receives BEADS_DIR in its environment so the script knows which store
// directory to operate on. Without this, K8s multi-prefix setups route
// all CRUD operations to the wrong store (gh-391 / gc-vko).
func TestOpenStoreAtForCity_execSetsBeadsDir(t *testing.T) {
	t.Setenv("GC_BEADS", "exec:/bin/true")
	t.Setenv("GC_DOLT", "skip")

	cityPath := t.TempDir()

	store, err := openStoreAtForCity(cityPath, cityPath)
	if err != nil {
		t.Fatalf("openStoreAtForCity: %v", err)
	}

	es, ok := store.(*beadsexec.Store)
	if !ok {
		t.Fatalf("expected *beadsexec.Store, got %T", store)
	}

	env := es.Env()
	want := filepath.Join(cityPath, ".beads")
	if got := env["BEADS_DIR"]; got != want {
		t.Errorf("BEADS_DIR = %q, want %q", got, want)
	}
}

// TestOpenStoreAtForCity_execRigGetsRigBeadsDir verifies that when opening
// a store for a rig path (storePath != cityPath), BEADS_DIR points to the
// rig's .beads/ directory, not the city's.
func TestOpenStoreAtForCity_execRigGetsRigBeadsDir(t *testing.T) {
	t.Setenv("GC_BEADS", "exec:/bin/true")
	t.Setenv("GC_DOLT", "skip")

	cityPath := t.TempDir()
	rigPath := t.TempDir()

	store, err := openStoreAtForCity(rigPath, cityPath)
	if err != nil {
		t.Fatalf("openStoreAtForCity: %v", err)
	}

	es, ok := store.(*beadsexec.Store)
	if !ok {
		t.Fatalf("expected *beadsexec.Store, got %T", store)
	}

	env := es.Env()
	want := filepath.Join(rigPath, ".beads")
	if got := env["BEADS_DIR"]; got != want {
		t.Errorf("BEADS_DIR = %q, want %q (should point to rig, not city)", got, want)
	}
}
