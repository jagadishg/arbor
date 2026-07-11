package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/jagadishg/arbor/internal/app"
	"github.com/jagadishg/arbor/internal/model"
)

func testModel() *Model {
	ws := &model.Workspace{Name: "Demo", Root: "/tmp/demo", DefaultEnv: "local", Requests: []model.Request{{ID: "users.get", Name: "Get user", Method: "GET", URL: "https://example.com"}, {ID: "users.create", Name: "Create user", Method: "POST", URL: "https://example.com"}}, Environments: []model.Environment{{Name: "local"}, {Name: "staging"}}, Scenarios: []model.Scenario{{ID: "smoke", Name: "Smoke", Steps: []model.ScenarioStep{{Request: "users.get"}}}}}
	return NewModel(context.Background(), "/tmp/demo", "local", &app.App{Workspace: ws})
}

func TestNavigationAndFiltering(t *testing.T) {
	m := testModel()
	_, _ = m.handleKey("j")
	if m.selected != 1 {
		t.Fatalf("selected = %d", m.selected)
	}
	_, _ = m.handleKey("/")
	_, _ = m.handleKey("c")
	_, _ = m.handleKey("r")
	_, _ = m.handleKey("e")
	_, _ = m.handleKey("a")
	_, _ = m.handleKey("t")
	_, _ = m.handleKey("e")
	_, _ = m.handleKey("enter")
	if len(m.items()) != 1 || m.items()[0].label != "users.create" {
		t.Fatalf("filtered items = %#v", m.items())
	}
}

func TestViewContainsContextAndShortcuts(t *testing.T) {
	m := testModel()
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	view := m.View().Content
	for _, expected := range []string{"ARBOR", "Demo", "env: local", "Requests", "[enter] run"} {
		if !strings.Contains(view, expected) {
			t.Errorf("view missing %q", expected)
		}
	}
}

func TestCommandSwitchesEnvironment(t *testing.T) {
	m := testModel()
	m.mode = commandMode
	m.input = "use staging"
	_, _ = m.handleKey("enter")
	if m.environment != "staging" {
		t.Fatalf("environment = %q", m.environment)
	}
}
