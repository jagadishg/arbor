package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRegisterSaveLoadRoundTrip(t *testing.T) {
	t.Setenv("ARBOR_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))

	empty, err := Load()
	if err != nil {
		t.Fatalf("Load() on missing file = %v", err)
	}
	if len(empty.Workspaces) != 0 || empty.Version != Version {
		t.Fatalf("expected empty config, got %#v", empty)
	}

	a, b := t.TempDir(), t.TempDir()
	cfg := &Config{}
	cfg.Register(a, "alpha")
	cfg.Register(b, "")              // name defaults to folder base
	cfg.Register(a, "alpha-renamed") // same path → dedupe + rename
	cfg.Touch(b)
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Workspaces) != 2 {
		t.Fatalf("expected 2 workspaces, got %d: %#v", len(loaded.Workspaces), loaded.Workspaces)
	}
	if entry, ok := loaded.Find("alpha-renamed"); !ok || entry.Path != a {
		t.Fatalf("dedupe/rename failed: %#v (%v)", entry, ok)
	}
	if loaded.LastWorkspace != b {
		t.Fatalf("LastWorkspace = %q, want %q", loaded.LastWorkspace, b)
	}
	if base, ok := loaded.Find(filepath.Base(b)); !ok || base.Path != b {
		t.Fatalf("default-name entry missing: %#v (%v)", base, ok)
	}

	if !loaded.Remove("alpha-renamed") {
		t.Fatal("Remove reported no deletion")
	}
	if _, ok := loaded.Find("alpha-renamed"); ok {
		t.Fatal("entry still present after Remove")
	}
}

func TestNormalizeExpandsHomeAndRelative(t *testing.T) {
	if got := normalize("~"); got == "~" || got == "" {
		t.Fatalf("~ was not expanded: %q", got)
	}
	if got := normalize("relative/dir"); !filepath.IsAbs(got) {
		t.Fatalf("relative path not made absolute: %q", got)
	}
}

func TestDirIsUniversalXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg-home")
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if dir != "/tmp/xdg-home/arbor" {
		t.Fatalf("Dir() with XDG_CONFIG_HOME = %q, want /tmp/xdg-home/arbor", dir)
	}

	t.Setenv("XDG_CONFIG_HOME", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	dir, err = Dir()
	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Join(home, ".config", "arbor") {
		t.Fatalf("Dir() fallback = %q, want %s/.config/arbor", dir, home)
	}
}
