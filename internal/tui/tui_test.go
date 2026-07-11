package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/jagadishg/arbor/internal/app"
	"github.com/jagadishg/arbor/internal/model"
)

func testModel() *Model {
	ws := &model.Workspace{Name: "Demo", Root: "/tmp/demo", DefaultEnv: "local", Requests: []model.Request{{ID: "users.get", Name: "Get user", Method: "GET", URL: "https://example.com/users/1"}, {ID: "users.create", Name: "Create user", Method: "POST", URL: "https://example.com/users"}}, Environments: []model.Environment{{Name: "local"}, {Name: "staging"}}, Scenarios: []model.Scenario{{ID: "smoke", Name: "Smoke", Steps: []model.ScenarioStep{{Request: "users.get"}}}}}
	return NewModel(context.Background(), "/tmp/demo", "local", &app.App{Workspace: ws})
}

func TestK9sStyleResourceAliasAndHistory(t *testing.T) {
	m := testModel()
	m.mode, m.input = commandMode, "sc"
	_, _ = m.handleKey("enter")
	if m.section != scenariosSection {
		t.Fatalf("section = %s", m.section)
	}
	_, _ = m.handleKey("esc")
	if m.section != requestsSection {
		t.Fatalf("esc did not return to requests: %s", m.section)
	}
}

func TestCommandCompletion(t *testing.T) {
	m := testModel()
	m.mode, m.input = commandMode, "req"
	_, _ = m.handleKey("tab")
	if m.input != "req" {
		t.Fatalf("input = %q", m.input)
	}
	m.mode, m.input = commandMode, "use st"
	_, _ = m.handleKey("tab")
	if m.input != "use staging" {
		t.Fatalf("input = %q", m.input)
	}
}

func TestFilteringIsIncrementalAndEscapeRestores(t *testing.T) {
	m := testModel()
	_, _ = m.handleKey("/")
	for _, key := range []string{"c", "r", "e"} {
		_, _ = m.handleKey(key)
	}
	if len(m.items()) != 1 || m.items()[0].label != "users.create" {
		t.Fatalf("filtered items = %#v", m.items())
	}
	_, _ = m.handleKey("esc")
	if m.filter != "" || len(m.items()) != 2 {
		t.Fatalf("filter = %q; items = %d", m.filter, len(m.items()))
	}
}

func TestViewUsesResourceTableAndK9sHints(t *testing.T) {
	m := testModel()
	_, _ = m.Update(tea.WindowSizeMsg{Width: 110, Height: 32})
	view := m.View().Content
	for _, expected := range []string{"ARBOR", "arbor > Demo > requests", "NAME", "METHOD", "[ctrl-a] aliases"} {
		if !strings.Contains(view, expected) {
			t.Errorf("view missing %q", expected)
		}
	}
}

func TestDescribeAndAliasesOverlays(t *testing.T) {
	m := testModel()
	_, _ = m.handleKey("d")
	if m.overlay != describeOverlay {
		t.Fatalf("overlay = %d", m.overlay)
	}
	_, _ = m.handleKey("esc")
	_, _ = m.handleKey("ctrl+a")
	if m.overlay != aliasOverlay || !strings.Contains(m.aliasContent(), ":req") {
		t.Fatalf("aliases not shown: %s", m.aliasContent())
	}
}

func TestWorkspaceAliasesExtendBuiltIns(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".arbor", "aliases.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("aliases:\n  smoke: scenarios\n  api: requests\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	aliases, err := loadAliases(root)
	if err != nil {
		t.Fatal(err)
	}
	if aliases["smoke"] != "scenarios" || aliases["req"] != "requests" {
		t.Fatalf("unexpected aliases: %#v", aliases)
	}
}

func TestWorkspaceHotkeys(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".arbor", "hotkeys.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	contents := "hotKeys:\n  shift-0:\n    shortCut: Shift-0\n    description: Open requests\n    command: requests\n"
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	hotkeys, err := loadHotkeys(root)
	if err != nil {
		t.Fatal(err)
	}
	if hotkeys["shift+0"].command != "requests" {
		t.Fatalf("unexpected hotkeys: %#v", hotkeys)
	}
}

func TestConfiguredHotkeyExecutesCommand(t *testing.T) {
	m := testModel()
	m.section = scenariosSection
	m.hotkeys = map[string]hotkey{"shift+0": {description: "Open requests", command: "requests"}}
	_, _ = m.handleKey("shift+0")
	if m.section != requestsSection {
		t.Fatalf("hotkey did not navigate: %s", m.section)
	}
}
