package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/jagadishg/arbor/internal/app"
	"github.com/jagadishg/arbor/internal/config"
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
	_, _ = m.handleKey("q")
	if m.section != requestsSection {
		t.Fatalf("q did not return to requests: %s", m.section)
	}
	_, _ = m.handleKey("]")
	if m.section != scenariosSection {
		t.Fatalf("] did not move forward: %s", m.section)
	}
}

func TestWideViewAndK9sPagingKeys(t *testing.T) {
	m := testModel()
	_, _ = m.handleKey("ctrl+w")
	if !m.wide || !strings.Contains(m.tableHeader(100), "FILE") {
		t.Fatalf("wide view was not enabled")
	}
	m.height = 7
	_, _ = m.handleKey("ctrl+f")
	if m.selected == 0 {
		t.Fatalf("ctrl-f did not page the list")
	}
}

func TestResourceCommandCanSetFilterAndContextView(t *testing.T) {
	m := testModel()
	m.mode, m.input = commandMode, "req /create"
	_, _ = m.handleKey("enter")
	if m.section != requestsSection || m.filter != "create" || len(m.items()) != 1 {
		t.Fatalf("unexpected resource filter state: %s /%s (%d items)", m.section, m.filter, len(m.items()))
	}
	m.mode, m.input = commandMode, "ctx"
	_, _ = m.handleKey("enter")
	if m.section != environmentsSection {
		t.Fatalf("ctx did not open environments: %s", m.section)
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
	for _, expected := range []string{"ARBOR", "Workspace:", "requests(all)[2]", "NAME", "METHOD", "[j/k] move"} {
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

func TestCollectionsDrillDownAndBack(t *testing.T) {
	ws := &model.Workspace{Name: "Demo", Root: "/tmp/demo",
		Requests: []model.Request{
			{ID: "users.get", Name: "Get", Method: "GET", URL: "https://x/1", Collection: "users"},
			{ID: "users.create", Name: "Create", Method: "POST", URL: "https://x", Collection: "users"},
			{ID: "billing.charge", Name: "Charge", Method: "POST", URL: "https://y", Collection: "billing"},
		},
		Collections: []model.Collection{{Name: "billing"}, {Name: "users"}},
	}
	m := NewModel(context.Background(), "/tmp/demo", "", &app.App{Workspace: ws})

	m.mode, m.input = commandMode, "col"
	_, _ = m.handleKey("enter")
	if m.section != collectionsSection || len(m.items()) != 2 {
		t.Fatalf(":col did not open collections: section=%s items=%d", m.section, len(m.items()))
	}

	m.selected = 1 // sorted: [billing, users]
	_, _ = m.handleKey("enter")
	if m.section != requestsSection || m.scope != "users" {
		t.Fatalf("drill-down failed: section=%s scope=%q", m.section, m.scope)
	}
	if len(m.items()) != 2 {
		t.Fatalf("scoped requests = %d, want 2", len(m.items()))
	}

	_, _ = m.handleKey("q")
	if m.section != collectionsSection || m.scope != "" {
		t.Fatalf("back did not restore collections view: section=%s scope=%q", m.section, m.scope)
	}
}

func TestCommandAndFilterPromptRenderAtTop(t *testing.T) {
	m := testModel()
	_, _ = m.Update(tea.WindowSizeMsg{Width: 110, Height: 32})
	_, _ = m.handleKey(":")
	for _, key := range []string{"r", "e", "q"} {
		_, _ = m.handleKey(key)
	}
	if view := m.View().Content; !strings.Contains(view, "req▊") {
		t.Fatalf("command prompt not rendered at top: %q", view)
	}

	m = testModel()
	_, _ = m.Update(tea.WindowSizeMsg{Width: 110, Height: 32})
	_, _ = m.handleKey("/")
	_, _ = m.handleKey("c")
	if view := m.View().Content; !strings.Contains(view, "c▊") {
		t.Fatalf("filter prompt not rendered at top: %q", view)
	}
}

func TestYAMLHighlighting(t *testing.T) {
	for _, line := range []string{"name: Demo", "version: 1", "# a comment", "  - status == 200"} {
		if out := highlightYAMLLine(line); !strings.Contains(out, "\x1b[") {
			t.Errorf("expected %q to be colourised, got %q", line, out)
		}
	}
	// A wrapped URL fragment must not be mistaken for a key/value split.
	if got := highlightYAMLLine("https://example.com/users/1"); !strings.Contains(got, "https://example.com/users/1") {
		t.Errorf("url fragment mangled: %q", got)
	}
	if isNumber("10s") || !isNumber("42") {
		t.Fatal("isNumber classification wrong")
	}
}

func TestWorkspaceSwitchReloadsAndResets(t *testing.T) {
	t.Setenv("ARBOR_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	writeWS := func(name string) string {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "arbor.yaml"), []byte("version: 1\nname: "+name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	dirA, dirB := writeWS("alpha"), writeWS("beta")
	cfg := &config.Config{}
	cfg.Register(dirA, "alpha")
	cfg.Register(dirB, "beta")
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	loadedA, err := app.Load(dirA)
	if err != nil {
		t.Fatal(err)
	}
	m := NewModel(context.Background(), dirA, "", &app.App{Workspace: loadedA.Workspace})
	m.section, m.scope = collectionsSection, "users"

	// :ws beta should switch; run the returned command and apply its message.
	_, cmd := m.executeCommand("ws beta")
	if cmd == nil {
		t.Fatal("no switch command returned")
	}
	m.Update(cmd())
	if m.app.Workspace.Name != "beta" {
		t.Fatalf("workspace not switched: %q", m.app.Workspace.Name)
	}
	if m.section != collectionsSection || m.scope != "" {
		t.Fatalf("state not reset after switch: section=%s scope=%q", m.section, m.scope)
	}
}

func TestHeaderUsesEnvironmentLabel(t *testing.T) {
	m := testModel()
	_, _ = m.Update(tea.WindowSizeMsg{Width: 110, Height: 32})
	if view := m.View().Content; !strings.Contains(view, "Environment:") {
		t.Fatalf("header missing Environment label: %q", view)
	}
}

func TestJSONHighlighting(t *testing.T) {
	for _, line := range []string{`  "id": 42,`, `  "name": "Ada",`, `  "active": true`} {
		if out := highlightJSONLine(line); !strings.Contains(out, "\x1b[") {
			t.Errorf("expected %q colourised, got %q", line, out)
		}
	}
}

func TestSplitViewFocusAndScroll(t *testing.T) {
	ws := &model.Workspace{Name: "Demo", Root: "/tmp/demo", Requests: []model.Request{{ID: "users.get", Name: "Get", Method: "GET", URL: strings.Repeat("https://x/", 20)}}}
	m := NewModel(context.Background(), "/tmp/demo", "", &app.App{Workspace: ws})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m.requestResult = &model.RequestResult{Request: ws.Requests[0], Response: &model.Response{Status: "200 OK", StatusCode: 200, Body: []byte(`{"a":1}`)}}
	m.overlay, m.focusedPane = responseOverlay, paneResponse
	if !m.inSplitView() {
		t.Fatal("expected split view")
	}
	_, _ = m.handleKey("tab")
	if m.focusedPane != paneRequest {
		t.Fatalf("tab did not focus request: %v", m.focusedPane)
	}
	_, _ = m.handleKey("j")
	if m.requestOffset == 0 {
		t.Fatal("j did not scroll the request pane")
	}
	if m.responseOffset != 0 {
		t.Fatal("scrolling request should not move response")
	}
	_, _ = m.handleKey("l")
	if m.focusedPane != paneResponse {
		t.Fatal("l did not focus response")
	}
}

func TestSplitPaneRowsKeepStableWidth(t *testing.T) {
	m := testModel()
	rows := m.renderPane("Response", []string{"short", lipgloss.NewStyle().Bold(true).Render(strings.Repeat("x", 80))}, 32, 8, 0, true, strings.Repeat("hint ", 20))
	for index, row := range rows {
		if width := lipgloss.Width(row); width != 32 {
			t.Fatalf("row %d has width %d, want 32: %q", index, width, row)
		}
	}
}

func TestSplitViewFitsBelowHeader(t *testing.T) {
	m := testModel()
	m.width, m.height = 100, 24
	m.requestResult = &model.RequestResult{
		Request:  m.app.Workspace.Requests[0],
		Response: &model.Response{Status: "200 OK", StatusCode: 200},
	}
	m.overlay, m.focusedPane = responseOverlay, paneResponse
	if height := lipgloss.Height(m.render()); height > m.height {
		t.Fatalf("split view exceeds terminal height: got %d, want <= %d", height, m.height)
	}
}

func TestRunRequestOpensLiveSplitView(t *testing.T) {
	ws := &model.Workspace{Name: "Demo", Root: "/tmp/demo", Requests: []model.Request{{ID: "users.get", Name: "Get", Method: "GET", URL: "https://x"}}}
	m := NewModel(context.Background(), "/tmp/demo", "", &app.App{Workspace: ws})
	cmd := m.runRequest("users.get")
	if cmd == nil {
		t.Fatal("runRequest returned no command")
	}
	if !m.running || !m.inSplitView() || m.requestResult == nil {
		t.Fatalf("request did not open live split view: running=%v split=%v result=%#v", m.running, m.inSplitView(), m.requestResult)
	}
	if m.requestResult.Response != nil {
		t.Fatal("live request unexpectedly has a response")
	}
	if lines := m.responsePaneLines(40); !strings.Contains(strings.Join(lines, "\n"), "Running request") {
		t.Fatalf("running response pane missing status: %v", lines)
	}
}

func TestRunningResponseShowsMilliseconds(t *testing.T) {
	m := testModel()
	m.running = true
	m.requestStarted = time.Now().Add(-1234 * time.Millisecond)
	m.requestResult = &model.RequestResult{Request: m.app.Workspace.Requests[0]}
	content := strings.Join(m.responsePaneLines(40), "\n")
	if !strings.Contains(content, "Elapsed ") || !strings.Contains(content, " ms") {
		t.Fatalf("running response did not show milliseconds: %q", content)
	}
}

func TestResponseInspectorNavigationAndSearch(t *testing.T) {
	m := testModel()
	m.width, m.height = 100, 24
	m.requestResult = &model.RequestResult{
		Request:  m.app.Workspace.Requests[0],
		Response: &model.Response{Status: "200 OK", StatusCode: 200, Body: []byte(`{"message":"needle","items":[1,2,3,4,5,6,7,8,9,10,11,12]}`)},
	}
	m.overlay, m.focusedPane = responseOverlay, paneResponse
	_, _ = m.handleKey("/")
	if m.mode != responseSearchMode {
		t.Fatalf("/ did not enter response search mode: %v", m.mode)
	}
	_, _ = m.handleKey("e")
	_, _ = m.handleKey("enter")
	if m.responseSearch != "e" || m.mode != normalMode {
		t.Fatalf("search was not committed: query=%q mode=%v", m.responseSearch, m.mode)
	}
	if !strings.Contains(ansi.Strip(m.renderSplit(100, 16)), "Response /e") {
		t.Fatal("committed search was not shown in the response pane title")
	}
	matches := m.responseSearchMatches(m.responseInnerWidth())
	if len(matches) < 2 {
		t.Fatal("search did not find response content")
	}
	_, _ = m.handleKey("n")
	if !strings.Contains(m.message, "Match") {
		t.Fatalf("n did not report the current match: %q", m.message)
	}
	current := m.responseMatch
	_, _ = m.handleKey("shift+n")
	if m.responseMatch == current {
		t.Fatal("shift+n did not move to the previous match")
	}
	_, _ = m.handleKey("shift+g")
	layout := m.currentSplitLayout()
	wantEnd := max(0, len(m.responsePaneLines(layout.responseWidth))-layout.available)
	if m.responseOffset != wantEnd {
		t.Fatalf("shift+g moved to offset %d, want %d", m.responseOffset, wantEnd)
	}
	_, _ = m.handleKey("/")
	if m.mode != responseSearchMode || m.input != "" || m.responseSearch != "" {
		t.Fatalf("new search did not clear the previous query: mode=%v input=%q search=%q", m.mode, m.input, m.responseSearch)
	}
}

func TestCommandBarOpensFromSplitView(t *testing.T) {
	m := testModel()
	m.width, m.height = 100, 24
	m.requestResult = &model.RequestResult{
		Request:  m.app.Workspace.Requests[0],
		Response: &model.Response{Status: "200 OK", StatusCode: 200},
	}
	m.overlay, m.focusedPane = responseOverlay, paneResponse
	_, _ = m.handleKey(":")
	if m.mode != commandMode {
		t.Fatalf(": did not open command mode from split view: %v", m.mode)
	}
	if !strings.Contains(ansi.Strip(m.render()), "▊") {
		t.Fatal("command prompt was not rendered above split view")
	}
	m.input = "collections"
	_, _ = m.handleKey("enter")
	if m.mode != normalMode || m.overlay != noOverlay || m.section != collectionsSection {
		t.Fatalf("split command did not navigate: mode=%v overlay=%v section=%v", m.mode, m.overlay, m.section)
	}
}

func TestReadonlyOverlayUsesSearchViewer(t *testing.T) {
	m := testModel()
	m.width, m.height = 100, 24
	m.overlay = helpOverlay
	_, _ = m.handleKey("/")
	_, _ = m.handleKey("n")
	_, _ = m.handleKey("enter")
	if m.overlaySearch != "n" || m.overlayMatch < 0 {
		t.Fatalf("readonly overlay search was not applied: query=%q match=%d", m.overlaySearch, m.overlayMatch)
	}
	if !strings.Contains(ansi.Strip(m.renderOverlay(100, 16)), "Keyboard shortcuts /n") {
		t.Fatal("readonly overlay did not show its search query")
	}
	if matches := m.overlaySearchMatches(); len(matches) == 0 {
		t.Fatal("readonly overlay search did not find help content")
	}
}

func TestSearchHighlightPreservesUnmatchedText(t *testing.T) {
	rendered := ansi.Strip(highlightSearchLine("alpha beta alpha", "alpha", false))
	if rendered != "alpha beta alpha" {
		t.Fatalf("search highlighting changed unmatched text: %q", rendered)
	}
}

func TestBackingOutOfRunningRequestCancelsAndDiscardsResult(t *testing.T) {
	ws := &model.Workspace{Name: "Demo", Root: "/tmp/demo", Requests: []model.Request{{ID: "users.get", Name: "Get", Method: "GET", URL: "https://x"}}}
	m := NewModel(context.Background(), "/tmp/demo", "", &app.App{Workspace: ws})
	m.runRequest("users.get")
	_, _ = m.handleKey("q")
	if m.overlay != noOverlay || !m.discardResult {
		t.Fatalf("backing out did not cancel live view: overlay=%v discard=%v", m.overlay, m.discardResult)
	}
	_, _ = m.Update(requestDoneMsg(model.RequestResult{Request: ws.Requests[0], Response: &model.Response{Status: "200 OK"}}))
	if m.overlay != noOverlay || m.running || m.requestResult != nil {
		t.Fatalf("cancelled result reopened or remained active: overlay=%v running=%v result=%#v", m.overlay, m.running, m.requestResult)
	}
}

func TestReloadAfterSplitEditRestoresSplitView(t *testing.T) {
	oldRequest := model.Request{ID: "users.get", Name: "Old", Method: "GET", URL: "https://old"}
	newRequest := model.Request{ID: "users.get", Name: "Updated", Method: "POST", URL: "https://new"}
	m := NewModel(context.Background(), "/tmp/demo", "", &app.App{Workspace: &model.Workspace{Requests: []model.Request{oldRequest}}})
	m.requestResult = &model.RequestResult{Request: oldRequest, Response: &model.Response{Status: "200 OK"}}
	m.restoreSplitAfterEdit = true
	loaded := &app.App{Workspace: &model.Workspace{Requests: []model.Request{newRequest}}}
	_, _ = m.Update(reloadDoneMsg{app: loaded})
	if !m.inSplitView() || m.requestResult.Request.Name != "Updated" || m.focusedPane != paneRequest {
		t.Fatalf("split view was not restored after edit: overlay=%v request=%#v focus=%v", m.overlay, m.requestResult.Request, m.focusedPane)
	}
}

func TestAttachWritesFilesToRequest(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "arbor.yaml"), []byte("version: 1\nname: Demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reqPath := filepath.Join(root, "collections", "up", "post.yaml")
	if err := os.MkdirAll(filepath.Dir(reqPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reqPath, []byte("version: 1\nkind: request\nid: up.post\nname: Up\nmethod: POST\nurl: 'https://x'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := app.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	m := NewModel(context.Background(), root, "", &app.App{Workspace: loaded.Workspace})
	m.selected = 0 // up.post
	_, _ = m.executeCommand("attach document=./files/x.txt")
	data, err := os.ReadFile(reqPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "files:") || !strings.Contains(string(data), "document: ./files/x.txt") {
		t.Fatalf("files not written: %s", data)
	}
}
