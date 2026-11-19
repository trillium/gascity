package config

import (
	"testing"

	"github.com/BurntSushi/toml"
)

func TestHerdrSessionConfigParses(t *testing.T) {
	var cfg City
	src := "[session]\nprovider = \"herdr\"\n[session.herdr]\nsession = \"default\"\nmanage_server = false\n"
	if _, err := toml.Decode(src, &cfg); err != nil {
		t.Fatal(err)
	}
	h := cfg.Session.Herdr
	if h.Session != "default" {
		t.Errorf("Session = %q, want default", h.Session)
	}
	if h.ManageServerEnabled() {
		t.Error("ManageServerEnabled() = true, want false")
	}
}

func TestHerdrManageServerDefaultsTrue(t *testing.T) {
	if !(HerdrSessionConfig{}).ManageServerEnabled() {
		t.Error("unset manage_server must default to true")
	}
}
