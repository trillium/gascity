//go:build !windows

package herdr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// recordingHerdr installs a herdr stand-in on p that appends every argv to a
// log and returns the log path. Any call at all shows up in it.
func recordingHerdr(t *testing.T, p *Provider) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "argv.log")
	script := filepath.Join(dir, "herdr")
	body := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + log + "'\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil { //nolint:gosec // test stand-in must be executable
		t.Fatal(err)
	}
	p.c.bin = script
	return log
}

func readArgvLog(t *testing.T, log string) string {
	t.Helper()
	b, err := os.ReadFile(log)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// With manage_server=false gc does not own the session, so TeardownServer must
// never run `herdr session stop` (which would stop a shared, live herdr) and
// ConfigureServer must never launch a server.
func TestUnmanagedServerNeverStopsOrStarts(t *testing.T) {
	p := New("default", t.TempDir(), t.TempDir(), time.Second, 0, WithManageServer(false))
	log := recordingHerdr(t, p)

	if err := p.ConfigureServer(); err != nil {
		t.Fatalf("ConfigureServer: %v", err)
	}
	if err := p.TeardownServer(); err != nil {
		t.Fatalf("TeardownServer: %v", err)
	}
	if got := readArgvLog(t, log); got != "" {
		t.Fatalf("unmanaged server ran herdr:\n%s", got)
	}
}

// Control: the default provider still owns the server and stops it.
func TestManagedServerStopsSession(t *testing.T) {
	p := New("city-x", t.TempDir(), t.TempDir(), time.Second, 0)
	log := recordingHerdr(t, p)

	if err := p.TeardownServer(); err != nil {
		t.Fatalf("TeardownServer: %v", err)
	}
	if got := readArgvLog(t, log); !strings.Contains(got, "session stop city-x") {
		t.Fatalf("managed TeardownServer did not run `session stop city-x`; calls:\n%s", got)
	}
}
