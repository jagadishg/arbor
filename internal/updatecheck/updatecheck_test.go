package updatecheck

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCheckUsesFreshCache(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := cachePath()
	if err != nil {
		t.Fatal(err)
	}
	if err := writeCache(path, cache{CheckedAt: time.Now(), LatestVersion: "v0.3.0"}); err != nil {
		t.Fatal(err)
	}
	message := Check(context.Background(), "v0.2.0", "not-a-valid-url")
	if !strings.Contains(message, "v0.2.0") || !strings.Contains(message, "v0.3.0") {
		t.Fatalf("notification = %q", message)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "arbor", cacheName)); err != nil {
		t.Fatalf("cache file was not written: %v", err)
	}
}

func TestCheckHonorsOptOut(t *testing.T) {
	t.Setenv("ARBOR_NO_UPDATE_CHECK", "1")
	if message := Check(context.Background(), "v0.1.0", "not-a-valid-url"); message != "" {
		t.Fatalf("opt-out notification = %q", message)
	}
}

func TestStableLatest(t *testing.T) {
	got := stableLatest([]release{
		{TagName: "v1.0.0"},
		{TagName: "v2.0.0", Prerelease: true},
		{TagName: "v3.0.0", Draft: true},
		{TagName: "v1.2.0"},
	})
	if got != "v1.2.0" {
		t.Fatalf("stableLatest() = %q, want v1.2.0", got)
	}
}

func TestNotificationIncludesPlatformInstruction(t *testing.T) {
	message := notification("v0.1.0", "v0.2.0")
	if runtime.GOOS == "darwin" {
		if !strings.Contains(message, "brew upgrade arbor") {
			t.Fatalf("notification = %q", message)
		}
	} else if !strings.Contains(message, "go install") {
		t.Fatalf("notification = %q", message)
	}
}
