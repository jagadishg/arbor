// Package tui implements Arbor's keyboard-first resource browser. Its
// interaction model deliberately follows the conventions people know from k9s:
// resource views, command aliases, a crumb history, contextual overlays, and
// incremental command suggestions.
package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/jagadishg/arbor/internal/app"
	"github.com/jagadishg/arbor/internal/buildinfo"
	"github.com/jagadishg/arbor/internal/config"
	"github.com/jagadishg/arbor/internal/model"
	"gopkg.in/yaml.v3"
)

type section int

const (
	requestsSection section = iota
	collectionsSection
	scenariosSection
	environmentsSection
	workspacesSection
)

func (s section) String() string {
	return []string{"requests", "collections", "scenarios", "environments", "workspaces"}[s]
}

type inputMode int

const (
	normalMode inputMode = iota
	filterMode
	commandMode
	responseSearchMode
)

type overlay int

const (
	noOverlay overlay = iota
	describeOverlay
	yamlOverlay
	responseOverlay
	helpOverlay
	aliasOverlay
)

type pane int

const (
	paneRequest pane = iota
	paneResponse
)

type splitLayout struct {
	height        int
	available     int
	leftOuter     int
	rightOuter    int
	requestWidth  int
	responseWidth int
}

func newSplitLayout(width, height int) splitLayout {
	height = max(6, height)
	leftOuter := width * 42 / 100
	rightOuter := width - leftOuter
	if width >= 48 {
		leftOuter = max(24, leftOuter)
		rightOuter = max(24, rightOuter)
	}
	return splitLayout{
		height:        height,
		available:     max(1, height-3),
		leftOuter:     leftOuter,
		rightOuter:    rightOuter,
		requestWidth:  max(1, leftOuter-3),
		responseWidth: max(1, rightOuter-3),
	}
}

type viewLocation struct {
	section  section
	filter   string
	scope    string
	selected int
}

type requestDoneMsg model.RequestResult
type requestTickMsg time.Time

const requestRefreshInterval = 100 * time.Millisecond

type scenarioDoneMsg model.ScenarioReport
type reloadDoneMsg struct {
	app     *app.App
	aliases map[string]string
	hotkeys map[string]hotkey
	err     error
}
type editorDoneMsg struct{ err error }
type workspaceSwitchedMsg struct {
	app        *app.App
	dir        string
	aliases    map[string]string
	hotkeys    map[string]hotkey
	workspaces []config.Entry
	err        error
}

type Model struct {
	ctx         context.Context
	dir         string
	app         *app.App
	environment string
	section     section
	selected    int
	filter      string
	scope       string
	history     []viewLocation
	forward     []viewLocation
	wide        bool

	mode       inputMode
	input      string
	filterSave string
	suggestion int

	overlay               overlay
	overlayOffset         int
	focusedPane           pane
	requestOffset         int
	responseOffset        int
	width                 int
	height                int
	running               bool
	cancel                context.CancelFunc
	requestStarted        time.Time
	spinner               int
	discardResult         bool
	restoreSplitAfterEdit bool
	requestResult         *model.RequestResult
	requestShowDefinition bool
	revealSecrets         bool
	responseSearch        string
	responseMatch         int
	overlaySearch         string
	overlayMatch          int
	scenarioReport        *model.ScenarioReport
	message               string
	aliases               map[string]string
	hotkeys               map[string]hotkey
	workspaces            []config.Entry
}

type suggestion struct {
	value       string
	description string
}

type hotkey struct {
	description string
	command     string
}

func Run(ctx context.Context, directory, environment string) error {
	loaded, err := app.Load(directory)
	if err != nil {
		return err
	}
	if environment == "" {
		environment = loaded.Workspace.DefaultEnv
	}
	program := tea.NewProgram(NewModel(ctx, directory, environment, loaded))
	_, err = program.Run()
	return err
}

func NewModel(ctx context.Context, directory, environment string, loaded *app.App) *Model {
	aliases, _ := loadAliases(loaded.Workspace.Root)
	hotkeys, _ := loadHotkeys(loaded.Workspace.Root)
	var workspaces []config.Entry
	if cfg, err := config.Load(); err == nil {
		workspaces = cfg.Workspaces
	}
	return &Model{ctx: ctx, dir: directory, app: loaded, environment: environment, aliases: aliases, hotkeys: hotkeys, workspaces: workspaces, width: 100, height: 30}
}

func (m *Model) Init() tea.Cmd { return nil }

func (m *Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case requestDoneMsg:
		result := model.RequestResult(msg)
		if m.discardResult {
			m.running, m.cancel, m.discardResult = false, nil, false
			m.requestResult = nil
			return m, nil
		}
		m.running, m.cancel, m.requestResult, m.scenarioReport = false, nil, &result, nil
		m.overlay, m.focusedPane, m.requestOffset, m.responseOffset = responseOverlay, paneResponse, 0, 0
		m.requestShowDefinition, m.revealSecrets = false, false
		if result.Passed() {
			m.message = "Request completed"
		} else {
			m.message = "Request failed"
		}
	case requestTickMsg:
		if m.running {
			m.spinner++
			return m, tea.Tick(requestRefreshInterval, func(t time.Time) tea.Msg { return requestTickMsg(t) })
		}
	case scenarioDoneMsg:
		report := model.ScenarioReport(msg)
		m.running, m.cancel, m.scenarioReport, m.requestResult = false, nil, &report, nil
		m.overlay = responseOverlay
		if report.Passed() {
			m.message = "Scenario completed"
		} else {
			m.message = "Scenario failed"
		}
	case reloadDoneMsg:
		if msg.err != nil {
			m.message = "Reload failed: " + msg.err.Error()
		} else {
			m.app, m.aliases, m.hotkeys, m.selected = msg.app, msg.aliases, msg.hotkeys, 0
			if m.restoreSplitAfterEdit && m.requestResult != nil {
				ref := m.requestResult.Request.Ref()
				if request, ok := m.app.Workspace.RequestByRef(ref); ok {
					m.requestResult.Request = request
					m.overlay, m.focusedPane = responseOverlay, paneRequest
					m.requestOffset, m.responseOffset = 0, 0
					m.message = "Request updated"
				} else {
					m.overlay, m.requestResult = noOverlay, nil
					m.message = "Request was removed"
				}
				m.restoreSplitAfterEdit = false
			} else {
				m.message = "Workspace reloaded"
			}
		}
	case editorDoneMsg:
		if msg.err != nil {
			m.message = "Editor failed: " + msg.err.Error()
		} else {
			return m, m.reloadCmd()
		}
	case workspaceSwitchedMsg:
		if msg.err != nil {
			m.message = "Switch failed: " + msg.err.Error()
		} else {
			m.app, m.dir = msg.app, msg.dir
			m.aliases, m.hotkeys, m.workspaces = msg.aliases, msg.hotkeys, msg.workspaces
			m.environment = m.app.Workspace.DefaultEnv
			m.section, m.scope, m.filter, m.selected = collectionsSection, "", "", 0
			m.history, m.forward = nil, nil
			m.overlay, m.overlayOffset, m.requestResult, m.scenarioReport = noOverlay, 0, nil, nil
			m.message = "Switched to " + m.app.Workspace.Name
		}
	case tea.KeyPressMsg:
		return m.handleKey(msg.Keystroke())
	}
	return m, nil
}

func (m *Model) handleKey(key string) (tea.Model, tea.Cmd) {
	if m.mode == commandMode || m.mode == filterMode || m.mode == responseSearchMode {
		return m.handleInput(key)
	}
	if m.overlay != noOverlay {
		return m.handleOverlayKey(key)
	}
	if m.running && (key == "ctrl+c" || key == "esc") {
		if m.cancel != nil {
			m.cancel()
		}
		m.message = "Cancelling request…"
		return m, nil
	}
	if binding, ok := m.hotkeys[normalizeKey(key)]; ok {
		return m.executeCommand(binding.command)
	}
	switch key {
	case "ctrl+c":
		return m, tea.Quit
	case "j", "down":
		m.move(1)
	case "k", "up":
		m.move(-1)
	case "g", "home":
		m.selected = 0
	case "G", "shift+g", "end":
		if count := len(m.items()); count > 0 {
			m.selected = count - 1
		}
	case "ctrl+d", "ctrl+f", "pagedown":
		m.move(max(1, m.tableHeight()/2))
	case "ctrl+u", "ctrl+b", "pageup":
		m.move(-max(1, m.tableHeight()/2))
	case "enter":
		switch m.section {
		case collectionsSection:
			m.drillIntoCollection()
		case workspacesSection:
			return m, m.switchToSelectedWorkspace()
		default:
			m.overlay, m.overlayOffset = describeOverlay, 0
			m.overlaySearch, m.overlayMatch = "", -1
		}
	case "d":
		m.overlay, m.overlayOffset = describeOverlay, 0
		m.overlaySearch, m.overlayMatch = "", -1
	case "y":
		m.overlay, m.overlayOffset = yamlOverlay, 0
		m.overlaySearch, m.overlayMatch = "", -1
	case "l":
		if m.hasResultForSelected() {
			m.overlay, m.overlayOffset = responseOverlay, 0
			m.focusedPane, m.requestOffset, m.responseOffset = paneResponse, 0, 0
		} else {
			m.message = "No response recorded for this resource"
		}
	case "r":
		return m, m.runSelected()
	case "e":
		return m, m.editSelected()
	case "/":
		m.mode, m.filterSave, m.input, m.suggestion = filterMode, m.filter, m.filter, 0
	case ":":
		m.mode, m.input, m.suggestion = commandMode, "", 0
	case "?":
		m.overlay, m.overlayOffset = helpOverlay, 0
	case "ctrl+a":
		m.overlay, m.overlayOffset = aliasOverlay, 0
	case "q", "esc", "h", "left", "[":
		m.goBack()
	case "]", "right":
		m.goForward()
	case "ctrl+w":
		m.wide = !m.wide
		if m.wide {
			m.message = "Wide view enabled"
		} else {
			m.message = "Wide view disabled"
		}
	case "ctrl+r":
		m.message = "Reloading workspace…"
		return m, m.reloadCmd()
	}
	return m, nil
}

func (m *Model) inSplitView() bool {
	return m.overlay == responseOverlay && m.requestResult != nil
}

func (m *Model) currentSplitLayout() splitLayout {
	width := max(m.width, 50)
	height := max(m.height, 12)
	header := m.renderHeader(width)
	bodyHeight := height - lipgloss.Height(header) - 1
	if m.mode == commandMode {
		prompt := m.renderPrompt(width)
		bodyHeight -= lipgloss.Height(prompt) + 1
	}
	return newSplitLayout(width, bodyHeight)
}

func (m *Model) handleSplitKey(key string) (tea.Model, tea.Cmd) {
	layout := m.currentSplitLayout()
	lineStep := 1
	pageStep := layout.available
	offset := &m.responseOffset
	lines := m.responsePaneLines(layout.responseWidth)
	if m.focusedPane == paneRequest {
		offset = &m.requestOffset
		lines = m.requestPaneLines(layout.requestWidth)
	}
	switch key {
	case "q", "esc":
		if m.running {
			m.cancelRunningRequest(true)
		} else {
			m.overlay = noOverlay
		}
	case "ctrl+c":
		if m.running && m.cancel != nil {
			m.cancel()
			m.message = "Cancelling request…"
			return m, nil
		}
		return m, tea.Quit
	case "tab":
		if m.focusedPane == paneRequest {
			m.focusedPane = paneResponse
		} else {
			m.focusedPane = paneRequest
		}
	case "h", "left":
		m.focusedPane = paneRequest
	case "l", "right":
		m.focusedPane = paneResponse
	case "j", "down", "ctrl+d", "pagedown":
		if key == "j" || key == "down" {
			*offset += lineStep
		} else {
			*offset += pageStep
		}
	case "k", "up", "ctrl+u", "pageup":
		if key == "k" || key == "up" {
			*offset = max(0, *offset-lineStep)
		} else {
			*offset = max(0, *offset-pageStep)
		}
	case "g", "home":
		*offset = 0
	case "G", "shift+g", "end":
		*offset = max(0, len(lines)-layout.available)
	case "H":
		*offset = 0
	case "M":
		*offset = clampOffset(max(0, len(lines)/2-pageStep/2), len(lines), layout.available)
	case "L":
		*offset = max(0, len(lines)-layout.available)
	case "/":
		if m.focusedPane == paneResponse {
			m.mode, m.input = responseSearchMode, ""
			m.responseSearch, m.responseMatch = "", -1
		}
	case "n":
		if m.focusedPane == paneResponse {
			m.jumpToResponseMatch(true)
		}
	case "N", "shift+n":
		if m.focusedPane == paneResponse {
			m.jumpToResponseMatch(false)
		}
	case "e":
		m.overlay = noOverlay
		return m, m.editRequestInView()
	case "y":
		m.requestShowDefinition = !m.requestShowDefinition
		m.requestOffset = 0
	case "x":
		m.revealSecrets = !m.revealSecrets
	case "r":
		if !m.running {
			return m, m.runRequest(m.requestResult.Request.Ref())
		}
	case ":":
		m.mode, m.input, m.suggestion = commandMode, "", 0
	}
	return m, nil
}

func (m *Model) editRequestInView() tea.Cmd {
	m.restoreSplitAfterEdit = true
	path := m.requestResult.Request.Path
	if path == "" {
		path = m.app.Workspace.Path
	}
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	parts := strings.Fields(editor)
	return tea.ExecProcess(exec.Command(parts[0], append(parts[1:], path)...), func(err error) tea.Msg { return editorDoneMsg{err: err} })
}

func (m *Model) handleOverlayKey(key string) (tea.Model, tea.Cmd) {
	if m.inSplitView() {
		return m.handleSplitKey(key)
	}
	switch key {
	case "q", "esc", "d", "y", "l", "?", "ctrl+a":
		m.overlay, m.overlayOffset = noOverlay, 0
	case "G", "shift+g", "end":
		lines := m.overlayDocumentLines(m.overlayContentWidth())
		m.overlayOffset = max(0, len(lines)-m.overlayAvailable())
	case "g", "home", "H":
		m.overlayOffset = 0
	case "M":
		lines := m.overlayDocumentLines(m.overlayContentWidth())
		available := m.overlayAvailable()
		m.overlayOffset = clampOffset(max(0, len(lines)/2-available/2), len(lines), available)
	case "L":
		lines := m.overlayDocumentLines(m.overlayContentWidth())
		m.overlayOffset = max(0, len(lines)-m.overlayAvailable())
	case "/":
		m.mode, m.input = responseSearchMode, ""
		m.overlaySearch, m.overlayMatch = "", -1
	case "n":
		m.jumpToOverlayMatch(true)
	case "N", "shift+n":
		m.jumpToOverlayMatch(false)
	case "ctrl+c":
		if m.running && m.cancel != nil {
			m.cancel()
			m.message = "Cancelling request…"
			return m, nil
		}
		return m, tea.Quit
	case "j", "down", "ctrl+d", "pagedown":
		m.overlayOffset += max(1, m.modalHeight()/3)
	case "k", "up", "ctrl+u", "pageup":
		m.overlayOffset = max(0, m.overlayOffset-max(1, m.modalHeight()/3))
	case "e":
		if m.overlay == yamlOverlay || m.overlay == describeOverlay {
			m.overlay = noOverlay
			m.restoreSplitAfterEdit = false
			return m, m.editSelected()
		}
	case "r":
		if m.overlay != helpOverlay && m.overlay != aliasOverlay {
			m.overlay = noOverlay
			return m, m.runSelected()
		}
	}
	return m, nil
}

func (m *Model) handleInput(key string) (tea.Model, tea.Cmd) {
	if m.mode == responseSearchMode {
		return m.handleResponseSearchInput(key)
	}
	if m.mode == filterMode {
		return m.handleFilterInput(key)
	}
	return m.handleCommandInput(key)
}

func (m *Model) handleResponseSearchInput(key string) (tea.Model, tea.Cmd) {
	isResponse := m.inSplitView()
	setQuery := func(value string) {
		if isResponse {
			m.responseSearch, m.responseMatch = value, -1
		} else {
			m.overlaySearch, m.overlayMatch = value, -1
		}
	}
	switch key {
	case "esc":
		m.mode, m.input = normalMode, ""
	case "enter":
		setQuery(strings.TrimSpace(m.input))
		m.mode, m.input, m.responseMatch = normalMode, "", -1
		if isResponse && m.responseSearch != "" {
			m.jumpToResponseMatch(true)
		} else if !isResponse && m.overlaySearch != "" {
			m.jumpToOverlayMatch(true)
		}
	case "backspace":
		m.input = trimRune(m.input)
		setQuery(m.input)
	case "ctrl+u":
		m.input = ""
		setQuery("")
	default:
		if isTextKey(key) {
			m.input += key
			setQuery(m.input)
		}
	}
	return m, nil
}

func (m *Model) handleFilterInput(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc":
		m.filter, m.mode, m.input = m.filterSave, normalMode, ""
	case "enter":
		m.filter, m.mode, m.input = strings.TrimSpace(m.input), normalMode, ""
		m.selected = 0
	case "backspace":
		m.input = trimRune(m.input)
	case "ctrl-u":
		m.input = ""
	default:
		if isTextKey(key) {
			m.input += key
		}
	}
	if m.mode == filterMode {
		m.filter, m.selected = m.input, 0
	}
	return m, nil
}

func (m *Model) handleCommandInput(key string) (tea.Model, tea.Cmd) {
	suggestions := m.suggestions()
	switch key {
	case "esc":
		m.mode, m.input, m.suggestion = normalMode, "", 0
	case "up":
		if len(suggestions) > 0 {
			m.suggestion = (m.suggestion - 1 + len(suggestions)) % len(suggestions)
		}
	case "down":
		if len(suggestions) > 0 {
			m.suggestion = (m.suggestion + 1) % len(suggestions)
		}
	case "tab", "ctrl+f", "right":
		m.acceptSuggestion()
	case "ctrl-u":
		m.input, m.suggestion = "", 0
	case "ctrl+w":
		m.input, m.suggestion = trimWord(m.input), 0
	case "backspace":
		m.input, m.suggestion = trimRune(m.input), 0
	case "enter":
		command := strings.TrimSpace(m.input)
		m.mode, m.input, m.suggestion = normalMode, "", 0
		return m.executeCommand(command)
	default:
		if isTextKey(key) {
			m.input += key
			m.suggestion = 0
		}
	}
	return m, nil
}

func (m *Model) acceptSuggestion() {
	suggestions := m.suggestions()
	if len(suggestions) == 0 {
		return
	}
	if m.suggestion >= len(suggestions) {
		m.suggestion = 0
	}
	m.input = suggestions[m.suggestion].value
}

func (m *Model) executeCommand(command string) (tea.Model, tea.Cmd) {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return m, nil
	}
	verb := strings.ToLower(fields[0])
	// Commands are global navigation actions. Leaving a split/overlay view here
	// prevents the command from appearing to do nothing behind the current view.
	// Attach is intentionally excluded because it targets the request in the
	// split view while it is still active.
	if verb != "attach" {
		m.overlay, m.requestResult, m.scenarioReport = noOverlay, nil, nil
		m.responseSearch, m.responseMatch = "", -1
		m.overlaySearch, m.overlayMatch = "", -1
	}
	if (verb == "ws" || verb == "workspace" || verb == "workspaces") && len(fields) == 2 && !strings.HasPrefix(fields[1], "/") {
		if entry, ok := m.findWorkspace(fields[1]); ok {
			m.message = "Switching to " + entry.Name + "…"
			return m, m.switchWorkspace(entry.Path)
		}
		m.message = "Workspace not found: " + fields[1]
		return m, nil
	}
	if target, ok := m.aliases[verb]; ok {
		filter := ""
		if len(fields) > 1 && strings.HasPrefix(fields[1], "/") {
			filter = strings.TrimPrefix(strings.Join(fields[1:], " "), "/")
		}
		m.navigate(sectionFor(target), filter, "")
		return m, nil
	}
	switch verb {
	case "q", "quit", "exit":
		return m, tea.Quit
	case "help", "?":
		m.overlay = helpOverlay
	case "aliases", "alias":
		m.overlay = aliasOverlay
	case "reload":
		return m, m.reloadCmd()
	case "use", "context", "ctx":
		if len(fields) == 1 && (verb == "context" || verb == "ctx") {
			m.navigate(environmentsSection, "", "")
			m.message = "Select an environment and press r to make it active"
			return m, nil
		}
		if len(fields) != 2 {
			m.message = "Usage: :use <environment> or :ctx <environment>"
			return m, nil
		}
		if _, ok := m.app.Workspace.EnvironmentByName(fields[1]); !ok {
			m.message = "Environment not found: " + fields[1]
		} else {
			m.environment, m.message = fields[1], "Environment set to "+fields[1]
		}
	case "run":
		if len(fields) != 2 {
			m.message = "Usage: :run <request-or-scenario>"
			return m, nil
		}
		if request, ok := m.app.Workspace.RequestByRef(fields[1]); ok {
			return m, m.runRequest(request.Ref())
		}
		if scenario, ok := m.app.Workspace.ScenarioByRef(fields[1]); ok {
			return m, m.runScenario(scenario.Ref())
		}
		m.message = "Resource not found: " + fields[1]
	case "attach":
		field, path, ok := strings.Cut(strings.TrimSpace(strings.TrimPrefix(command, fields[0])), "=")
		if !ok || strings.TrimSpace(field) == "" || strings.TrimSpace(path) == "" {
			m.message = "Usage: :attach <field>=<path>"
			return m, nil
		}
		m.attachFile(strings.TrimSpace(field), strings.TrimSpace(path))
	default:
		m.message = "Unknown command: " + fields[0] + " — Ctrl-a lists aliases"
	}
	return m, nil
}

// attachFile adds a multipart file entry to the targeted request (the one shown in
// the response view, else the selected request) and rewrites its YAML file.
func (m *Model) attachFile(field, path string) {
	var request model.Request
	if m.inSplitView() {
		request = m.requestResult.Request
	} else if selected, ok := m.selectedRequest(); ok {
		request = selected
	} else {
		m.message = "Select a request to attach a file to"
		return
	}
	current, ok := m.app.Workspace.RequestByRef(request.Ref())
	if !ok || current.Path == "" {
		m.message = "Request not found on disk"
		return
	}
	if current.Body != nil {
		m.message = "Cannot attach a file to a request that has a body"
		return
	}
	if current.Files == nil {
		current.Files = map[string]string{}
	}
	current.Files[field] = path
	data, err := yaml.Marshal(current)
	if err != nil {
		m.message = "Attach failed: " + err.Error()
		return
	}
	if err := os.WriteFile(current.Path, data, 0o644); err != nil {
		m.message = "Attach failed: " + err.Error()
		return
	}
	if loaded, err := app.Load(m.dir); err == nil {
		m.app = loaded
	}
	m.message = fmt.Sprintf("Attached %s (%s) to %s", field, path, current.Ref())
}

func (m *Model) selectedRequest() (model.Request, bool) {
	items := m.items()
	if len(items) == 0 {
		return model.Request{}, false
	}
	if request, ok := items[m.selected].value.(model.Request); ok {
		return request, true
	}
	return model.Request{}, false
}

func (m *Model) navigate(next section, filter, scope string) {
	if next == workspacesSection {
		m.refreshWorkspaces()
	}
	if m.section != next || m.filter != filter || m.scope != scope {
		m.history = append(m.history, viewLocation{m.section, m.filter, m.scope, m.selected})
		m.forward = nil
	}
	m.section, m.filter, m.scope, m.selected = next, filter, scope, 0
	m.message = "Viewing " + next.String()
}

func (m *Model) goBack() {
	if len(m.history) == 0 {
		m.message = "Already at the root view"
		return
	}
	last := m.history[len(m.history)-1]
	m.forward = append(m.forward, viewLocation{m.section, m.filter, m.scope, m.selected})
	m.history = m.history[:len(m.history)-1]
	m.section, m.filter, m.scope, m.selected = last.section, last.filter, last.scope, last.selected
	m.message = "Back to " + m.section.String()
}

func (m *Model) goForward() {
	if len(m.forward) == 0 {
		m.message = "No forward view"
		return
	}
	last := m.forward[len(m.forward)-1]
	m.history = append(m.history, viewLocation{m.section, m.filter, m.scope, m.selected})
	m.forward = m.forward[:len(m.forward)-1]
	m.section, m.filter, m.scope, m.selected = last.section, last.filter, last.scope, last.selected
	m.message = "Forward to " + m.section.String()
}

func sectionFor(target string) section {
	switch target {
	case "collections":
		return collectionsSection
	case "scenarios":
		return scenariosSection
	case "environments":
		return environmentsSection
	case "workspaces":
		return workspacesSection
	default:
		return requestsSection
	}
}

func (m *Model) drillIntoCollection() {
	items := m.items()
	if len(items) == 0 {
		return
	}
	if collection, ok := items[m.selected].value.(model.Collection); ok {
		m.navigate(requestsSection, "", collection.Name)
		m.message = "Viewing collection " + collection.Name
	}
}

func (m *Model) switchToSelectedWorkspace() tea.Cmd {
	items := m.items()
	if len(items) == 0 {
		return nil
	}
	if entry, ok := items[m.selected].value.(config.Entry); ok {
		if entry.Path == m.app.Workspace.Root {
			// Already the active workspace — drill into its collections.
			m.navigate(collectionsSection, "", "")
			m.message = "Viewing " + entry.Name
			return nil
		}
		m.message = "Switching to " + entry.Name + "…"
		return m.switchWorkspace(entry.Path)
	}
	return nil
}

// switchWorkspace loads a different workspace, refreshes its interaction files,
// and records it in the central registry as most-recently-used.
func (m *Model) switchWorkspace(path string) tea.Cmd {
	return func() tea.Msg {
		loaded, err := app.Load(path)
		if err != nil {
			return workspaceSwitchedMsg{err: err}
		}
		aliases, _ := loadAliases(loaded.Workspace.Root)
		hotkeys, _ := loadHotkeys(loaded.Workspace.Root)
		var entries []config.Entry
		if cfg, cfgErr := config.Load(); cfgErr == nil {
			cfg.Register(loaded.Workspace.Root, loaded.Workspace.Name)
			cfg.Touch(loaded.Workspace.Root)
			_ = cfg.Save()
			entries = cfg.Workspaces
		}
		return workspaceSwitchedMsg{app: loaded, dir: loaded.Workspace.Root, aliases: aliases, hotkeys: hotkeys, workspaces: entries}
	}
}

func (m *Model) refreshWorkspaces() {
	if cfg, err := config.Load(); err == nil {
		m.workspaces = cfg.Workspaces
	}
}

func (m *Model) findWorkspace(name string) (config.Entry, bool) {
	m.refreshWorkspaces()
	for _, entry := range m.workspaces {
		if entry.Name == name {
			return entry, true
		}
	}
	return config.Entry{}, false
}

func (m *Model) move(delta int) {
	count := len(m.items())
	if count == 0 {
		m.selected = 0
		return
	}
	m.selected = (m.selected + delta + count) % count
}

func (m *Model) runSelected() tea.Cmd {
	items := m.items()
	if len(items) == 0 || m.running {
		return nil
	}
	switch value := items[m.selected].value.(type) {
	case model.Request:
		return m.runRequest(value.Ref())
	case model.Scenario:
		return m.runScenario(value.Ref())
	case model.Environment:
		m.environment, m.message = value.Name, "Environment set to "+value.Name
	}
	return nil
}

func (m *Model) runRequest(ref string) tea.Cmd {
	if !m.requireEnvironment() {
		return nil
	}
	ctx, cancel := context.WithCancel(m.ctx)
	request, _ := m.app.Workspace.RequestByRef(ref)
	m.running, m.cancel, m.message = true, cancel, "Running "+ref+"…"
	m.requestStarted, m.spinner, m.discardResult = time.Now(), 0, false
	m.requestResult = &model.RequestResult{Request: request}
	m.overlay, m.focusedPane, m.requestOffset, m.responseOffset = responseOverlay, paneResponse, 0, 0
	loaded, environment := m.app, m.environment
	requestCmd := func() tea.Msg { return requestDoneMsg(loaded.RunRequest(ctx, ref, environment, nil)) }
	return tea.Batch(requestCmd, tea.Tick(requestRefreshInterval, func(t time.Time) tea.Msg { return requestTickMsg(t) }))
}

func (m *Model) cancelRunningRequest(closeView bool) {
	if m.cancel != nil {
		m.cancel()
	}
	m.message = "Cancelling request…"
	m.discardResult = closeView
	if closeView {
		m.overlay = noOverlay
		m.requestResult = nil
	}
}

// requireEnvironment blocks a run when the workspace defines environments but
// none is active, so a run never fails obscurely on undefined {{variables}}.
func (m *Model) requireEnvironment() bool {
	if m.environment == "" && len(m.app.Workspace.Environments) > 0 {
		m.message = "No environment selected — press :use <name> or open :env and pick one with r"
		return false
	}
	return true
}

func (m *Model) runScenario(ref string) tea.Cmd {
	if !m.requireEnvironment() {
		return nil
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.running, m.cancel, m.message = true, cancel, "Running "+ref+"…"
	loaded, environment := m.app, m.environment
	return func() tea.Msg {
		report, err := loaded.RunScenario(ctx, ref, environment, nil)
		if err != nil {
			report = model.ScenarioReport{Scenario: model.Scenario{Name: ref}, Steps: []model.RequestResult{{Error: err}}}
		}
		return scenarioDoneMsg(report)
	}
}

func (m *Model) reloadCmd() tea.Cmd {
	directory := m.dir
	return func() tea.Msg {
		loaded, err := app.Load(directory)
		if err != nil {
			return reloadDoneMsg{err: err}
		}
		aliases, aliasErr := loadAliases(loaded.Workspace.Root)
		if aliasErr != nil {
			return reloadDoneMsg{err: aliasErr}
		}
		hotkeys, hotkeyErr := loadHotkeys(loaded.Workspace.Root)
		if hotkeyErr != nil {
			return reloadDoneMsg{err: hotkeyErr}
		}
		return reloadDoneMsg{app: loaded, aliases: aliases, hotkeys: hotkeys}
	}
}

func (m *Model) editSelected() tea.Cmd {
	path := m.selectedPath()
	if path == "" {
		path = m.app.Workspace.Path
	}
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	parts := strings.Fields(editor)
	return tea.ExecProcess(exec.Command(parts[0], append(parts[1:], path)...), func(err error) tea.Msg { return editorDoneMsg{err: err} })
}

func (m *Model) selectedPath() string {
	items := m.items()
	if len(items) == 0 {
		return ""
	}
	switch value := items[m.selected].value.(type) {
	case model.Request:
		return value.Path
	case model.Collection:
		if value.Path != "" {
			return value.Path
		}
		return value.Dir
	case model.Scenario:
		return value.Path
	case model.Environment:
		return value.Path
	case config.Entry:
		return filepath.Join(value.Path, "arbor.yaml")
	}
	return ""
}

func (m *Model) hasResultForSelected() bool {
	items := m.items()
	if len(items) == 0 {
		return false
	}
	switch value := items[m.selected].value.(type) {
	case model.Request:
		return m.requestResult != nil && m.requestResult.Request.Ref() == value.Ref()
	case model.Scenario:
		return m.scenarioReport != nil && m.scenarioReport.Scenario.Ref() == value.Ref()
	}
	return false
}

type item struct {
	label string
	value any
}

func (m *Model) items() []item {
	var values []item
	switch m.section {
	case requestsSection:
		requests := append([]model.Request(nil), m.app.Workspace.Requests...)
		if m.scope != "" {
			scoped := requests[:0]
			for _, request := range requests {
				if request.Collection == m.scope {
					scoped = append(scoped, request)
				}
			}
			requests = scoped
		}
		sort.Slice(requests, func(i, j int) bool { return requests[i].Ref() < requests[j].Ref() })
		for _, request := range requests {
			values = append(values, item{request.Ref(), request})
		}
	case collectionsSection:
		collections := append([]model.Collection(nil), m.app.Workspace.Collections...)
		sort.Slice(collections, func(i, j int) bool { return collections[i].Name < collections[j].Name })
		for _, collection := range collections {
			values = append(values, item{collection.Name, collection})
		}
	case scenariosSection:
		scenarios := append([]model.Scenario(nil), m.app.Workspace.Scenarios...)
		sort.Slice(scenarios, func(i, j int) bool { return scenarios[i].Ref() < scenarios[j].Ref() })
		for _, scenario := range scenarios {
			values = append(values, item{scenario.Ref(), scenario})
		}
	case environmentsSection:
		environments := append([]model.Environment(nil), m.app.Workspace.Environments...)
		sort.Slice(environments, func(i, j int) bool { return environments[i].Name < environments[j].Name })
		for _, environment := range environments {
			values = append(values, item{environment.Name, environment})
		}
	case workspacesSection:
		for _, entry := range m.workspaces {
			values = append(values, item{entry.Name, entry})
		}
	}
	if m.filter == "" {
		return values
	}
	filtered := values[:0]
	for _, value := range values {
		if fuzzyMatch(strings.ToLower(m.itemSearchText(value)), strings.ToLower(m.filter)) {
			filtered = append(filtered, value)
		}
	}
	return filtered
}

func (m *Model) itemSearchText(item item) string {
	switch value := item.value.(type) {
	case model.Request:
		return value.Ref() + " " + value.Name + " " + value.Method + " " + value.URL
	case model.Collection:
		return value.Name + " " + value.Description
	case model.Scenario:
		return value.Ref() + " " + value.Name
	case model.Environment:
		return value.Name
	case config.Entry:
		return value.Name + " " + value.Path
	}
	return item.label
}

func fuzzyMatch(value, pattern string) bool {
	if pattern == "" {
		return true
	}
	index := 0
	for _, char := range strings.ToLower(value) {
		if index < len(pattern) && string(char) == string([]rune(pattern)[index]) {
			index++
		}
	}
	return index == len([]rune(pattern))
}

func (m *Model) suggestions() []suggestion {
	input := strings.ToLower(strings.TrimSpace(m.input))
	var values []suggestion
	if strings.HasPrefix(input, "run ") {
		prefix := strings.TrimSpace(strings.TrimPrefix(input, "run "))
		for _, request := range m.app.Workspace.Requests {
			if strings.HasPrefix(strings.ToLower(request.Ref()), prefix) {
				values = append(values, suggestion{"run " + request.Ref(), "run request"})
			}
		}
		for _, scenario := range m.app.Workspace.Scenarios {
			if strings.HasPrefix(strings.ToLower(scenario.Ref()), prefix) {
				values = append(values, suggestion{"run " + scenario.Ref(), "run scenario"})
			}
		}
	} else if strings.HasPrefix(input, "use ") || strings.HasPrefix(input, "ctx ") {
		verb, prefix := "use", strings.TrimSpace(strings.TrimPrefix(input, "use "))
		if strings.HasPrefix(input, "ctx ") {
			verb, prefix = "ctx", strings.TrimSpace(strings.TrimPrefix(input, "ctx "))
		}
		for _, environment := range m.app.Workspace.Environments {
			if strings.HasPrefix(strings.ToLower(environment.Name), prefix) {
				values = append(values, suggestion{verb + " " + environment.Name, "switch environment"})
			}
		}
	} else {
		for alias, target := range m.aliases {
			if strings.HasPrefix(alias, input) {
				values = append(values, suggestion{alias, target + " view"})
			}
		}
		for _, command := range []suggestion{{"requests", "browse requests"}, {"collections", "browse collections"}, {"scenarios", "browse scenarios"}, {"environments", "browse environments"}, {"workspaces", "switch workspace"}, {"attach", "attach a file to a request"}, {"aliases", "show resource aliases"}, {"help", "show keyboard shortcuts"}, {"reload", "reload workspace files"}, {"use", "switch environment"}, {"ctx", "switch environment"}, {"run", "run a request or scenario"}, {"quit", "quit Arbor"}} {
			if strings.HasPrefix(command.value, input) {
				values = append(values, command)
			}
		}
	}
	// Collapse duplicates (a resource word is both an alias and a command),
	// preferring the more descriptive entry over the generic "… view" label.
	deduped := map[string]suggestion{}
	for _, value := range values {
		if existing, ok := deduped[value.value]; ok {
			if strings.HasSuffix(existing.description, " view") && !strings.HasSuffix(value.description, " view") {
				deduped[value.value] = value
			}
			continue
		}
		deduped[value.value] = value
	}
	unique := make([]suggestion, 0, len(deduped))
	for _, value := range deduped {
		unique = append(unique, value)
	}
	sort.Slice(unique, func(i, j int) bool { return unique[i].value < unique[j].value })
	return unique
}

func (m *Model) View() tea.View {
	view := tea.NewView(m.render())
	view.AltScreen = true
	view.WindowTitle = "Arbor — " + m.app.Workspace.Name
	return view
}

var (
	green              = lipgloss.Color("#73DACA")
	blue               = lipgloss.Color("#7AA2F7")
	yellow             = lipgloss.Color("#E0AF68")
	red                = lipgloss.Color("#F7768E")
	purple             = lipgloss.Color("#BB9AF7")
	muted              = lipgloss.Color("#707A8C")
	panel              = lipgloss.Color("#24283B")
	selectedBackground = lipgloss.Color("#364A82")
	searchBackground   = lipgloss.Color("#3B4252")
	searchCurrent      = lipgloss.Color("#EBCB8B")
	foreground         = lipgloss.Color("#C0CAF5")
)

func (m *Model) render() string {
	width, height := max(m.width, 50), max(m.height, 12)
	header, footer := m.renderHeader(width), m.renderFooter(width)
	if m.overlay != noOverlay {
		var body string
		if m.inSplitView() {
			bodyHeight := height - lipgloss.Height(header) - 1
			if m.mode == commandMode {
				prompt := m.renderPrompt(width)
				body = prompt + "\n" + m.renderSplit(width, bodyHeight-lipgloss.Height(prompt)-1)
			} else {
				body = m.renderSplit(width, bodyHeight)
			}
		} else {
			body = m.renderOverlay(width, height-lipgloss.Height(header)-1)
		}
		return lipgloss.NewStyle().Foreground(foreground).Render(header + "\n" + body)
	}
	sections := header
	promptHeight := 0
	if m.mode == commandMode || m.mode == filterMode || m.mode == responseSearchMode {
		prompt := m.renderPrompt(width)
		sections += "\n" + prompt
		promptHeight = lipgloss.Height(prompt)
	}
	bodyHeight := max(3, height-lipgloss.Height(header)-lipgloss.Height(footer)-promptHeight)
	body := m.renderTable(width, bodyHeight)
	sections += "\n" + body + "\n" + footer
	return lipgloss.NewStyle().Foreground(foreground).Render(sections)
}

// renderPrompt draws the k9s-style command or filter prompt in its own bordered
// box directly beneath the header. Command mode shows the top completion inline
// as dim ghost text after the cursor (k9s completes in place; it has no dropdown).
func (m *Model) renderPrompt(width int) string {
	inner := max(20, width-2)
	glyph, accent := ">", blue
	if m.mode == filterMode || m.mode == responseSearchMode {
		glyph, accent = "/", red
	}
	content := " " + lipgloss.NewStyle().Foreground(accent).Bold(true).Render(glyph) + " " + m.input + "▊"
	if m.mode == commandMode {
		if suggestions := m.suggestions(); len(suggestions) > 0 {
			index := m.suggestion
			if index < 0 || index >= len(suggestions) {
				index = 0
			}
			if remainder := strings.TrimPrefix(suggestions[index].value, strings.ToLower(m.input)); remainder != suggestions[index].value {
				content += lipgloss.NewStyle().Foreground(muted).Render(remainder)
			}
		}
	}
	border := lipgloss.NewStyle().Foreground(accent)
	top := border.Render("┌" + strings.Repeat("─", inner) + "┐")
	mid := border.Render("│") + content + strings.Repeat(" ", max(0, inner-lipgloss.Width(content))) + border.Render("│")
	bottom := border.Render("└" + strings.Repeat("─", inner) + "┘")
	return top + "\n" + mid + "\n" + bottom
}

func (m *Model) renderHeader(width int) string {
	label := lipgloss.NewStyle().Foreground(yellow).Bold(true)
	brand := lipgloss.NewStyle().Foreground(blue).Bold(true)
	value := lipgloss.NewStyle().Foreground(foreground)
	accent := lipgloss.NewStyle().Foreground(green).Bold(true)
	if width < 90 {
		line1 := " " + brand.Render("ARBOR") + "  " + label.Render("Workspace:") + " " + value.Render(truncate(m.app.Workspace.Name, max(10, width/4))) + "  " + label.Render("Env:") + " " + accent.Render(firstOr(m.environment, "none"))
		line2 := " " + label.Render("View:") + " " + accent.Render(m.section.String()) + "  " + label.Render("Keys:") + " " + highlightShortcutLine(truncate(m.contextualShortcuts(), max(10, width-16)))
		return lipgloss.NewStyle().Width(width).Render(line1 + "\n" + line2)
	}

	logo := []string{
		`    _    ____  ____   ___  ____`,
		`   / \  |  _ \| __ ) / _ \|  _ \`,
		`  / _ \ | |_) |  _ \| | | | |_) |`,
		` / ___ \|  _ <| |_) | |_| |  _ <`,
		`/_/   \_\_| \_\____/ \___/|_| \_\`,
	}
	logoWidth := 34
	leftWidth := width - logoWidth - 2
	infoWidth := min(60, max(36, width/3))
	shortcutWidth := max(28, leftWidth-infoWidth-1)
	shortcuts := strings.Split(wrap(m.contextualShortcuts(), max(10, shortcutWidth)), "\n")
	shortcutLines := shortcuts
	workspace := truncate(m.app.Workspace.Name, max(10, infoWidth-14))
	environment := firstOr(m.environment, "none")
	resources := fmt.Sprintf("%d requests · %d scenarios · %d environments", len(m.app.Workspace.Requests), len(m.app.Workspace.Scenarios), len(m.app.Workspace.Environments))
	info := []string{
		label.Render("Workspace:") + " " + value.Render(workspace),
		label.Render("Environment:") + " " + accent.Render(environment),
		label.Render("View:") + " " + accent.Render(m.section.String()),
		label.Render("Resources:") + " " + value.Render(resources),
		label.Render("Rev:") + " " + value.Render(buildinfo.Version),
	}
	rows := make([]string, 0, len(logo))
	for index := range logo {
		logoCell := lipgloss.NewStyle().Foreground(green).Bold(true).Render(logo[index])
		infoCell := truncateStyled(info[index], infoWidth)
		shortcutCell := ""
		if index < len(shortcutLines) {
			if index == 0 {
				shortcutCell = label.Render(shortcutLines[index])
			} else {
				shortcutCell = highlightShortcutLine(truncate(shortcutLines[index], shortcutWidth))
			}
		}
		rows = append(rows, fitHeaderCell(infoCell, infoWidth)+" "+fitHeaderCell(shortcutCell, shortcutWidth)+" "+fitHeaderCell(logoCell, logoWidth))
	}
	return lipgloss.NewStyle().Width(width).Render(strings.Join(rows, "\n"))
}

func highlightShortcutLine(line string) string {
	base := lipgloss.NewStyle().Foreground(foreground)
	key := lipgloss.NewStyle().Foreground(yellow).Bold(true)
	var out strings.Builder
	for len(line) > 0 {
		start := strings.IndexByte(line, '[')
		if start < 0 {
			out.WriteString(base.Render(line))
			break
		}
		out.WriteString(base.Render(line[:start]))
		end := strings.IndexByte(line[start:], ']')
		if end < 0 {
			out.WriteString(base.Render(line[start:]))
			break
		}
		end += start + 1
		out.WriteString(key.Render(line[start:end]))
		line = line[end:]
	}
	return out.String()
}

func fitHeaderCell(value string, width int) string {
	value = ansi.Truncate(value, max(0, width), "…")
	return value + strings.Repeat(" ", max(0, width-lipgloss.Width(value)))
}

func truncateStyled(value string, width int) string {
	return ansi.Truncate(value, max(0, width), "…")
}

func (m *Model) renderTable(width, height int) string {
	items := m.items()
	inner := max(20, width-2)
	filterScope := "all"
	if m.filter != "" {
		filterScope = "/" + m.filter
	}
	label := m.section.String()
	if m.section == requestsSection && m.scope != "" {
		label += "·" + m.scope
	}
	title := fmt.Sprintf("─ %s(%s)[%d] ", label, filterScope, len(items))
	top := "┌" + lipgloss.NewStyle().Foreground(blue).Bold(true).Render(title) + strings.Repeat("─", max(0, inner-lipgloss.Width(title))) + "┐"
	lines := []string{top, m.frameLine(m.tableHeader(inner), inner)}
	available := max(1, height-3)
	start := 0
	if m.selected >= available {
		start = m.selected - available + 1
	}
	end := min(len(items), start+available)
	for index := start; index < end; index++ {
		lines = append(lines, m.frameLine(m.tableRow(index, items[index], inner), inner))
	}
	if len(items) == 0 {
		lines = append(lines, m.frameLine(lipgloss.NewStyle().Foreground(muted).Render("  No resources match this view"), inner))
	}
	for len(lines) < height-1 {
		lines = append(lines, "│"+strings.Repeat(" ", inner)+"│")
	}
	lines = append(lines, "└"+strings.Repeat("─", inner)+"┘")
	return strings.Join(lines, "\n")
}

func (m *Model) frameLine(value string, width int) string {
	padding := max(0, width-lipgloss.Width(value))
	return "│" + value + strings.Repeat(" ", padding) + "│"
}

func (m *Model) tableHeader(width int) string {
	style := lipgloss.NewStyle().Foreground(muted).Bold(true)
	switch m.section {
	case requestsSection:
		nameWidth, urlWidth := m.requestColumnWidths(width)
		line := fmt.Sprintf("  %-*s %-8s %-*s %s", nameWidth, "NAME", "METHOD", urlWidth, "URL", "A")
		if m.wide {
			line += " FILE"
		}
		return style.Render(line)
	case collectionsSection:
		nameWidth := min(28, max(16, width/3))
		return style.Render(fmt.Sprintf("  %-*s %-9s %s", nameWidth, "NAME", "REQUESTS", "DESCRIPTION"))
	case scenariosSection:
		nameWidth := max(18, width-24)
		return style.Render(fmt.Sprintf("  %-*s %-8s %s", nameWidth, "NAME", "STEPS", "ON FAILURE"))
	case workspacesSection:
		nameWidth := min(28, max(16, width/3))
		return style.Render(fmt.Sprintf("  %-*s %s", nameWidth, "NAME", "PATH"))
	default:
		nameWidth := max(18, width-30)
		return style.Render(fmt.Sprintf("  %-*s %-10s %-8s %s", nameWidth, "NAME", "VARIABLES", "SECRETS", "ACTIVE"))
	}
}

func (m *Model) tableRow(index int, item item, width int) string {
	prefix, style := "  ", lipgloss.NewStyle().Width(width)
	if index == m.selected {
		prefix, style = "› ", style.Background(selectedBackground).Bold(true)
	}
	switch value := item.value.(type) {
	case model.Request:
		nameWidth, urlWidth := m.requestColumnWidths(width)
		line := fmt.Sprintf("%s%-*s %-8s %-*s %d", prefix, nameWidth, truncate(value.Ref(), nameWidth), methodStyle(value.Method).Render(strings.ToUpper(value.Method)), urlWidth, truncate(value.URL, urlWidth), len(value.Assert))
		if m.wide {
			line += " " + truncate(relative(m.app.Workspace.Root, value.Path), 18)
		}
		return style.Render(line)
	case model.Collection:
		nameWidth := min(28, max(16, width/3))
		count := len(m.app.Workspace.RequestsInCollection(value.Name))
		descWidth := max(10, width-nameWidth-13)
		return style.Render(fmt.Sprintf("%s%-*s %-9d %s", prefix, nameWidth, truncate(value.Name, nameWidth), count, truncate(value.Description, descWidth)))
	case model.Scenario:
		mode := "STOP"
		if value.ContinueOnFailure {
			mode = "CONTINUE"
		}
		nameWidth := max(18, width-24)
		return style.Render(fmt.Sprintf("%s%-*s %-8d %s", prefix, nameWidth, truncate(value.Ref(), nameWidth), len(value.Steps), mode))
	case model.Environment:
		active := ""
		if value.Name == m.environment {
			active = lipgloss.NewStyle().Foreground(green).Render("●")
		}
		nameWidth := max(18, width-30)
		return style.Render(fmt.Sprintf("%s%-*s %-10d %-8d %s", prefix, nameWidth, truncate(value.Name, nameWidth), len(value.Variables), len(value.Secrets), active))
	case config.Entry:
		nameWidth := min(28, max(16, width/3))
		marker := ""
		if value.Path == m.app.Workspace.Root {
			marker = lipgloss.NewStyle().Foreground(green).Render(" ●")
		}
		pathWidth := max(10, width-nameWidth-6)
		return style.Render(fmt.Sprintf("%s%-*s %s%s", prefix, nameWidth, truncate(value.Name, nameWidth), truncate(value.Path, pathWidth), marker))
	}
	return ""
}

// renderSplit draws the run result as two side-by-side framed panes: the request
// on the left and the response on the right, k9s-style. The focused pane has a
// blue border and receives scroll keys; the other is muted.
func (m *Model) renderSplit(width, height int) string {
	layout := newSplitLayout(width, height)

	reqLines := m.requestPaneLines(layout.requestWidth)
	respLines := m.responsePaneLines(layout.responseWidth)

	m.requestOffset = clampOffset(m.requestOffset, len(reqLines), layout.available)
	m.responseOffset = clampOffset(m.responseOffset, len(respLines), layout.available)

	left := m.renderPane(m.requestPaneTitle(), reqLines, layout.leftOuter, layout.height, m.requestOffset, m.focusedPane == paneRequest, m.requestPaneFooter())
	responseFooter := "[j/k] scroll  [g/G] top/end  [/] search  [n/N] match  [q] close"
	if m.responseSearch != "" {
		matches := m.responseSearchMatches(layout.responseWidth)
		responseFooter += fmt.Sprintf("  /%s (%d)", m.responseSearch, len(matches))
	}
	responseTitle := "Response"
	if m.mode == responseSearchMode {
		responseTitle = "/ " + m.input + "▊"
	} else if m.responseSearch != "" {
		responseTitle = "Response /" + m.responseSearch
	}
	right := m.renderPane(responseTitle, respLines, layout.rightOuter, layout.height, m.responseOffset, m.focusedPane == paneResponse, responseFooter)

	rows := make([]string, layout.height)
	for index := 0; index < layout.height; index++ {
		rows[index] = left[index] + right[index]
	}
	return strings.Join(rows, "\n")
}

// requestPaneTitle labels the request pane with the active view: the resolved
// request that was sent, or the raw YAML definition.
func (m *Model) requestPaneTitle() string {
	ref := m.requestResult.Request.Ref()
	if m.requestShowDefinition || m.requestResult.Sent == nil {
		return "Request " + ref + " (definition)"
	}
	if m.revealSecrets {
		return "Request " + ref + " (sent, revealed)"
	}
	return "Request " + ref + " (sent)"
}

func (m *Model) requestPaneFooter() string {
	if m.requestShowDefinition || m.requestResult.Sent == nil {
		return "[y] sent  [e] edit  [tab] focus"
	}
	reveal := "[x] reveal"
	if m.revealSecrets {
		reveal = "[x] hide"
	}
	return "[y] definition  " + reveal + "  [e] edit"
}

func clampOffset(offset, total, available int) int {
	maxOffset := max(0, total-available)
	if offset > maxOffset {
		offset = maxOffset
	}
	if offset < 0 {
		offset = 0
	}
	return offset
}

// renderPane frames a list of (already-styled) content lines into a box of exactly
// outerWidth columns and height rows, scrolled to offset.
func (m *Model) renderPane(title string, lines []string, outerWidth, height, offset int, focused bool, footer string) []string {
	outerWidth = max(4, outerWidth)
	inner := outerWidth - 2
	contentWidth := max(0, inner-1)
	color := muted
	if focused {
		color = blue
	}
	border := lipgloss.NewStyle().Foreground(color)
	titleText := "─ " + title + " "
	if lipgloss.Width(titleText) > inner {
		titleText = ansi.Truncate(titleText, inner, "…")
	}
	styledTitle := lipgloss.NewStyle().Foreground(blue).Bold(true).Render(titleText)
	top := border.Render("┌") + styledTitle + border.Render(strings.Repeat("─", max(0, inner-lipgloss.Width(titleText)))+"┐")

	frame := func(content string) string {
		content = ansi.Truncate(content, contentWidth, "…")
		pad := max(0, contentWidth-lipgloss.Width(content))
		return border.Render("│") + " " + content + strings.Repeat(" ", pad) + border.Render("│")
	}

	out := []string{top}
	available := max(1, height-3)
	end := min(len(lines), offset+available)
	for index := offset; index < end; index++ {
		out = append(out, frame(lines[index]))
	}
	for len(out) < height-2 {
		out = append(out, border.Render("│")+strings.Repeat(" ", inner)+border.Render("│"))
	}
	hint := footer
	if len(lines) > available {
		hint += fmt.Sprintf("  %d-%d/%d", offset+1, end, len(lines))
	}
	out = append(out, frame(lipgloss.NewStyle().Foreground(muted).Render(hint)))
	out = append(out, border.Render("└"+strings.Repeat("─", inner)+"┘"))
	return out
}

func (m *Model) requestPaneLines(inner int) []string {
	sent := m.requestResult.Sent
	if m.requestShowDefinition || sent == nil {
		return m.requestDefinitionLines(inner)
	}
	return m.sentRequestLines(sent, inner)
}

// requestDefinitionLines renders the raw YAML definition (the edit target),
// with placeholders left unresolved.
func (m *Model) requestDefinitionLines(inner int) []string {
	raw := ""
	if path := m.requestResult.Request.Path; path != "" {
		if data, err := os.ReadFile(path); err == nil {
			raw = string(data)
		}
	}
	if strings.TrimSpace(raw) == "" {
		raw = strings.ToUpper(m.requestResult.Request.Method) + " " + m.requestResult.Request.URL
	}
	var out []string
	for _, line := range strings.Split(wrap(raw, inner), "\n") {
		out = append(out, highlightYAMLLine(line))
	}
	return out
}

// sentRequestLines renders the fully-resolved request that went on the wire:
// method + URL, headers, and body. Secret values are masked unless revealed.
func (m *Model) sentRequestLines(sent *model.SentRequest, inner int) []string {
	reveal := func(text string) string {
		if m.revealSecrets {
			return text
		}
		return sent.Redact(text)
	}
	sectionStyle := lipgloss.NewStyle().Foreground(muted).Bold(true)
	headerStyle := lipgloss.NewStyle().Foreground(muted)

	var out []string
	requestLine := strings.ToUpper(sent.Method) + " " + reveal(sent.URL)
	for _, line := range strings.Split(wrap(requestLine, inner), "\n") {
		out = append(out, highlightYAMLLine(line))
	}

	if len(sent.Headers) > 0 {
		out = append(out, "", sectionStyle.Render("Headers"))
		for _, key := range sortedHeaderKeys(sent.Headers) {
			value := reveal(strings.Join(sent.Headers[key], ", "))
			for _, line := range strings.Split(wrap(key+": "+value, inner), "\n") {
				out = append(out, headerStyle.Render(line))
			}
		}
	}

	if strings.TrimSpace(sent.Body) != "" {
		out = append(out, "", sectionStyle.Render("Body"))
		for _, line := range strings.Split(wrap(reveal(sent.Body), inner), "\n") {
			out = append(out, highlightJSONLine(line))
		}
	}
	return out
}

func (m *Model) responsePaneLines(inner int) []string {
	document := m.responseDocumentLines(inner)
	if m.running {
		frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		return []string{
			lipgloss.NewStyle().Foreground(blue).Bold(true).Render(frames[m.spinner%len(frames)] + " " + document[0]),
			lipgloss.NewStyle().Foreground(muted).Render(document[1]),
			"",
			lipgloss.NewStyle().Foreground(muted).Render(document[3]),
		}
	}

	result := m.requestResult
	lines := make([]string, 0, len(document))
	section := ""
	for index, line := range document {
		styled := line
		switch {
		case result.Error != nil:
			if index == 0 {
				styled = lipgloss.NewStyle().Foreground(red).Bold(true).Render(line)
			} else {
				styled = lipgloss.NewStyle().Foreground(red).Render(line)
			}
		case index == 0:
			styled = statusStyle(result.Response.StatusCode).Render(line)
		case index == 1:
			styled = lipgloss.NewStyle().Foreground(muted).Render(line)
		case line == "Assertions" || line == "Headers" || line == "Body":
			section = line
			styled = lipgloss.NewStyle().Foreground(muted).Bold(true).Render(line)
		case strings.HasPrefix(line, "✓ "):
			styled = lipgloss.NewStyle().Foreground(green).Render("✓") + line[1:]
		case strings.HasPrefix(line, "✗ "):
			styled = lipgloss.NewStyle().Foreground(red).Render("✗") + line[1:]
		case section == "Headers":
			styled = lipgloss.NewStyle().Foreground(muted).Render(line)
		default:
			styled = highlightJSONLine(line)
		}
		lines = append(lines, styled)
	}

	if m.responseSearch != "" {
		matches := m.responseSearchMatches(inner)
		current := -1
		if m.responseMatch >= 0 && m.responseMatch < len(matches) {
			current = matches[m.responseMatch]
		}
		for index, line := range lines {
			if strings.Contains(strings.ToLower(document[index]), strings.ToLower(m.responseSearch)) {
				lines[index] = highlightSearchLine(ansi.Strip(line), m.responseSearch, index == current)
			}
		}
	}
	return lines
}

func (m *Model) responseDocumentLines(inner int) []string {
	result := m.requestResult
	if m.running {
		return []string{"Running request…", fmt.Sprintf("Elapsed %d ms", time.Since(m.requestStarted).Milliseconds()), "", "Press q or esc to cancel and go back"}
	}
	if result.Error != nil {
		lines := []string{"Request failed", ""}
		for _, line := range strings.Split(wrap(result.Error.Error(), inner), "\n") {
			lines = append(lines, line)
		}
		return lines
	}
	response := result.Response
	lines := []string{"● " + truncate(response.Status, max(1, inner-2)), truncate(fmt.Sprintf("%s · %d B", response.Duration.Round(time.Millisecond), response.Size), inner)}
	if len(result.Assertions) > 0 {
		lines = append(lines, "", "Assertions")
		for _, assertion := range result.Assertions {
			mark := "✓"
			if !assertion.Passed {
				mark = "✗"
			}
			text := assertion.Expression
			if assertion.Message != "" {
				text += " — " + assertion.Message
			}
			lines = append(lines, mark+" "+truncate(text, max(1, inner-2)))
		}
	}
	if len(response.Headers) > 0 {
		lines = append(lines, "", "Headers")
		for _, key := range sortedHeaderKeys(response.Headers) {
			lines = append(lines, truncate(key+": "+strings.Join(response.Headers[key], ", "), inner))
		}
	}
	lines = append(lines, "", "Body")
	lines = append(lines, strings.Split(formatBody(response.Body, inner), "\n")...)
	return lines
}

func (m *Model) responseInnerWidth() int {
	return m.currentSplitLayout().responseWidth
}

func (m *Model) responseSearchMatches(inner int) []int {
	if m.responseSearch == "" {
		return nil
	}
	query := strings.ToLower(m.responseSearch)
	document := m.responseDocumentLines(inner)
	matches := make([]int, 0)
	for index, line := range document {
		if strings.Contains(strings.ToLower(line), query) {
			matches = append(matches, index)
		}
	}
	return matches
}

func highlightSearchLine(line, query string, current bool) string {
	query = strings.TrimSpace(query)
	if query == "" {
		return line
	}
	background := searchBackground
	foregroundColor := foreground
	if current {
		background = searchCurrent
		foregroundColor = lipgloss.Color("#1A1B26")
	}
	match := lipgloss.NewStyle().Foreground(foregroundColor).Background(background).Bold(true)
	lowerLine, lowerQuery := strings.ToLower(line), strings.ToLower(query)
	var out strings.Builder
	for len(lowerLine) > 0 {
		index := strings.Index(lowerLine, lowerQuery)
		if index < 0 {
			out.WriteString(line)
			break
		}
		out.WriteString(line[:index])
		end := index + len(query)
		if end > len(line) {
			end = len(line)
		}
		out.WriteString(match.Render(line[index:end]))
		line, lowerLine = line[end:], lowerLine[end:]
	}
	return out.String()
}

func (m *Model) jumpToResponseMatch(next bool) {
	matches := m.responseSearchMatches(m.responseInnerWidth())
	if len(matches) == 0 {
		m.message = "No response matches for /" + m.responseSearch
		return
	}
	if m.responseMatch < 0 || m.responseMatch >= len(matches) {
		m.responseMatch = 0
	} else if next {
		m.responseMatch = (m.responseMatch + 1) % len(matches)
	} else {
		m.responseMatch = (m.responseMatch - 1 + len(matches)) % len(matches)
	}
	layout := m.currentSplitLayout()
	document := m.responseDocumentLines(layout.responseWidth)
	m.responseOffset = clampOffset(matches[m.responseMatch]-layout.available/2, len(document), layout.available)
	m.message = fmt.Sprintf("Match %d/%d for /%s", m.responseMatch+1, len(matches), m.responseSearch)
}

func statusStyle(code int) lipgloss.Style {
	color := blue
	switch {
	case code >= 200 && code < 300:
		color = green
	case code >= 300 && code < 400:
		color = yellow
	case code >= 400:
		color = red
	}
	return lipgloss.NewStyle().Foreground(color).Bold(true)
}

func sortedHeaderKeys(headers map[string][]string) []string {
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// highlightJSONLine colours a single line of pretty-printed JSON: object keys
// blue, string values green, numbers yellow, booleans/null purple.
func highlightJSONLine(line string) string {
	trimmed := strings.TrimLeft(line, " ")
	indent := line[:len(line)-len(trimmed)]
	if trimmed == "" {
		return line
	}
	if strings.HasPrefix(trimmed, "\"") {
		if end := strings.Index(trimmed[1:], "\""); end >= 0 {
			keyEnd := end + 2
			if keyEnd < len(trimmed) && strings.HasPrefix(trimmed[keyEnd:], ":") {
				key := lipgloss.NewStyle().Foreground(blue).Render(trimmed[:keyEnd])
				return indent + key + ":" + highlightJSONValue(trimmed[keyEnd+1:])
			}
		}
	}
	return indent + highlightJSONValue(trimmed)
}

func highlightJSONValue(value string) string {
	lead := value[:len(value)-len(strings.TrimLeft(value, " "))]
	scalar := strings.TrimLeft(value, " ")
	trailing := ""
	if strings.HasSuffix(scalar, ",") {
		trailing, scalar = ",", scalar[:len(scalar)-1]
	}
	switch {
	case scalar == "":
		return value
	case strings.HasPrefix(scalar, "{") || strings.HasPrefix(scalar, "[") || strings.HasPrefix(scalar, "}") || strings.HasPrefix(scalar, "]"):
		return lead + scalar + trailing
	case strings.HasPrefix(scalar, "\""):
		return lead + lipgloss.NewStyle().Foreground(green).Render(scalar) + trailing
	case scalar == "true" || scalar == "false" || scalar == "null":
		return lead + lipgloss.NewStyle().Foreground(purple).Render(scalar) + trailing
	case isNumber(scalar):
		return lead + lipgloss.NewStyle().Foreground(yellow).Render(scalar) + trailing
	default:
		return lead + lipgloss.NewStyle().Foreground(green).Render(scalar) + trailing
	}
}

// renderOverlay draws describe/YAML/response/help panels as a full-width framed
// pane (like k9s), filling the area beneath the header. Content is wrapped to the
// interior width and each line is framed to an exact width so borders stay aligned.
func (m *Model) renderOverlay(width, height int) string {
	inner := max(20, width-2)
	height = max(6, height)
	title, _ := m.overlayContent(inner - 2)
	allLines := m.overlayDocumentLines(inner - 2)
	if m.mode == responseSearchMode {
		title = "/ " + m.input + "▊"
	} else if m.overlaySearch != "" {
		title += " /" + m.overlaySearch
	}

	titleText := fmt.Sprintf("─ %s ", title)
	top := "┌" + lipgloss.NewStyle().Foreground(blue).Bold(true).Render(titleText) + strings.Repeat("─", max(0, inner-lipgloss.Width(titleText))) + "┐"

	hint := "[esc] close  [j/k] scroll  [g/G] top/end  [/] search  [n/N] match"
	switch m.overlay {
	case describeOverlay, yamlOverlay:
		hint += "  [e] edit  [r] run"
	case responseOverlay:
		hint += "  [r] run again"
	}

	available := max(1, height-3) // top border, footer line, bottom border
	maxOffset := max(0, len(allLines)-available)
	if m.overlayOffset > maxOffset {
		m.overlayOffset = maxOffset
	}
	if m.overlayOffset < 0 {
		m.overlayOffset = 0
	}
	end := min(len(allLines), m.overlayOffset+available)

	lines := []string{top}
	styleLine := func(value string) string { return value }
	if m.overlay == yamlOverlay {
		styleLine = highlightYAMLLine
	}
	for index := m.overlayOffset; index < end; index++ {
		line := styleLine(allLines[index])
		if m.overlaySearch != "" && strings.Contains(strings.ToLower(allLines[index]), strings.ToLower(m.overlaySearch)) {
			matches := m.overlaySearchMatches()
			current := m.overlayMatch >= 0 && m.overlayMatch < len(matches) && matches[m.overlayMatch] == index
			line = highlightSearchLine(ansi.Strip(line), m.overlaySearch, current)
		}
		lines = append(lines, m.frameLine(" "+line, inner))
	}
	for len(lines) < height-2 {
		lines = append(lines, m.frameLine("", inner))
	}
	scroll := ""
	if len(allLines) > available {
		scroll = fmt.Sprintf("  %d-%d/%d", m.overlayOffset+1, end, len(allLines))
	}
	footer := lipgloss.NewStyle().Foreground(muted).Render(" "+hint) + lipgloss.NewStyle().Foreground(muted).Render(scroll)
	lines = append(lines, m.frameLine(footer, inner))
	lines = append(lines, "└"+strings.Repeat("─", inner)+"┘")
	return strings.Join(lines, "\n")
}

func (m *Model) overlayContentWidth() int {
	return max(1, max(m.width, 50)-4)
}

func (m *Model) overlayAvailable() int {
	width := max(m.width, 50)
	header := m.renderHeader(width)
	height := max(m.height, 12) - lipgloss.Height(header) - 1
	return max(1, max(6, height)-3)
}

func (m *Model) overlayDocumentLines(width int) []string {
	_, content := m.overlayContent(width)
	return strings.Split(wrap(content, width), "\n")
}

func (m *Model) overlaySearchMatches() []int {
	if m.overlaySearch == "" {
		return nil
	}
	query := strings.ToLower(m.overlaySearch)
	matches := []int{}
	for index, line := range m.overlayDocumentLines(m.overlayContentWidth()) {
		if strings.Contains(strings.ToLower(line), query) {
			matches = append(matches, index)
		}
	}
	return matches
}

func (m *Model) jumpToOverlayMatch(next bool) {
	matches := m.overlaySearchMatches()
	if len(matches) == 0 {
		m.message = "No matches for /" + m.overlaySearch
		return
	}
	if m.overlayMatch < 0 || m.overlayMatch >= len(matches) {
		m.overlayMatch = 0
	} else if next {
		m.overlayMatch = (m.overlayMatch + 1) % len(matches)
	} else {
		m.overlayMatch = (m.overlayMatch - 1 + len(matches)) % len(matches)
	}
	lines := m.overlayDocumentLines(m.overlayContentWidth())
	available := m.overlayAvailable()
	m.overlayOffset = clampOffset(matches[m.overlayMatch]-available/2, len(lines), available)
	m.message = fmt.Sprintf("Match %d/%d for /%s", m.overlayMatch+1, len(matches), m.overlaySearch)
}

func (m *Model) overlayContent(width int) (string, string) {
	switch m.overlay {
	case describeOverlay:
		return "Describe " + m.selectedLabel(), m.describeSelected()
	case yamlOverlay:
		return "YAML " + m.selectedLabel(), m.yamlSelected()
	case responseOverlay:
		return m.responseTitle(), m.responseContent(width)
	case helpOverlay:
		return "Keyboard shortcuts", m.helpContent()
	case aliasOverlay:
		return "Resource aliases", m.aliasContent()
	}
	return "", ""
}

func (m *Model) selectedLabel() string {
	items := m.items()
	if len(items) == 0 {
		return m.section.String()
	}
	return items[m.selected].label
}

func (m *Model) describeSelected() string {
	items := m.items()
	if len(items) == 0 {
		return "No resource selected"
	}
	switch value := items[m.selected].value.(type) {
	case model.Request:
		lines := []string{"Name: " + value.Name, "Reference: " + value.Ref(), "Collection: " + firstOr(value.Collection, "default"), "Method: " + strings.ToUpper(value.Method), "URL: " + value.URL, "File: " + relative(m.app.Workspace.Root, value.Path)}
		if value.Description != "" {
			lines = append(lines, "", value.Description)
		}
		if len(value.Headers) > 0 {
			lines = append(lines, "", "Headers")
			for _, key := range sortedKeys(value.Headers) {
				lines = append(lines, "  "+key+": "+value.Headers[key])
			}
		}
		if len(value.Assert) > 0 {
			lines = append(lines, "", "Assertions")
			for _, rule := range value.Assert {
				lines = append(lines, "  "+rule)
			}
		}
		return strings.Join(lines, "\n")
	case model.Collection:
		requests := m.app.Workspace.RequestsInCollection(value.Name)
		lines := []string{"Collection: " + value.Name}
		if value.Path != "" {
			lines = append(lines, "File: "+relative(m.app.Workspace.Root, value.Path))
		}
		if value.Description != "" {
			lines = append(lines, "", value.Description)
		}
		lines = append(lines, "", fmt.Sprintf("Requests (%d)", len(requests)))
		sort.Slice(requests, func(i, j int) bool { return requests[i].Ref() < requests[j].Ref() })
		for _, request := range requests {
			lines = append(lines, fmt.Sprintf("  %-8s %s", strings.ToUpper(request.Method), request.Ref()))
		}
		return strings.Join(lines, "\n")
	case model.Scenario:
		lines := []string{"Name: " + value.Name, "Reference: " + value.Ref(), "File: " + relative(m.app.Workspace.Root, value.Path)}
		if value.Description != "" {
			lines = append(lines, "", value.Description)
		}
		lines = append(lines, "", "Steps")
		for index, step := range value.Steps {
			lines = append(lines, fmt.Sprintf("  %d. %s", index+1, step.Request))
		}
		return strings.Join(lines, "\n")
	case model.Environment:
		lines := []string{"Name: " + value.Name, "File: " + relative(m.app.Workspace.Root, value.Path)}
		if value.Description != "" {
			lines = append(lines, "", value.Description)
		}
		lines = append(lines, "", "Variables")
		for _, key := range sortedKeys(value.Variables) {
			lines = append(lines, "  "+key+": "+value.Variables[key])
		}
		if len(value.Secrets) > 0 {
			lines = append(lines, "", "Secrets")
			for _, key := range sortedKeys(value.Secrets) {
				lines = append(lines, "  "+key+": ••••••")
			}
		}
		return strings.Join(lines, "\n")
	case config.Entry:
		lines := []string{"Workspace: " + value.Name, "Path: " + value.Path}
		if value.Path == m.app.Workspace.Root {
			lines = append(lines, "", "(current workspace — press Enter to reopen)")
		} else {
			lines = append(lines, "", "Press Enter to switch to this workspace")
		}
		return strings.Join(lines, "\n")
	}
	return ""
}

func (m *Model) yamlSelected() string {
	path := m.selectedPath()
	if path == "" {
		path = m.app.Workspace.Path
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err.Error()
	}
	return string(data)
}

func (m *Model) responseTitle() string {
	if m.requestResult != nil {
		return "Response " + m.requestResult.Request.Ref()
	}
	if m.scenarioReport != nil {
		return "Scenario " + m.scenarioReport.Scenario.Ref()
	}
	return "Response"
}

func (m *Model) responseContent(width int) string {
	if m.requestResult != nil {
		result := m.requestResult
		if result.Error != nil {
			return "Request failed\n\n" + result.Error.Error()
		}
		response := result.Response
		lines := []string{response.Status, fmt.Sprintf("%s · %d B", response.Duration.Round(time.Millisecond), response.Size)}
		for _, assertion := range result.Assertions {
			mark := "✓"
			if !assertion.Passed {
				mark = "✗"
			}
			line := mark + " " + assertion.Expression
			if assertion.Message != "" {
				line += " — " + assertion.Message
			}
			lines = append(lines, line)
		}
		return strings.Join(lines, "\n") + "\n\n" + formatBody(response.Body, width)
	}
	if m.scenarioReport != nil {
		lines := []string{}
		for index, step := range m.scenarioReport.Steps {
			mark := "✓"
			if !step.Passed() {
				mark = "✗"
			}
			status := "—"
			if step.Response != nil {
				status = fmt.Sprint(step.Response.StatusCode)
			}
			lines = append(lines, fmt.Sprintf("%s %2d  %-28s %s", mark, index+1, step.Request.Ref(), status))
			if step.Error != nil {
				lines = append(lines, "     "+step.Error.Error())
			}
		}
		return strings.Join(lines, "\n")
	}
	return "No response available"
}

func (m *Model) helpContent() string {
	help := strings.Join([]string{
		"Navigation", "  j/k, ↑/↓     move selection", "  g/G          first/last resource", "  Ctrl-f/b     page down/up", "  Enter        drill into a collection / switch workspace (or describe)", "  Esc, q, h, [ back through view history", "  ], →         forward through view history", "", "Resource actions", "  Enter, d     describe selected resource", "  y            show YAML", "  e            edit in $EDITOR", "  r            run selected request or scenario", "  l            show last response (like logs)", "  Ctrl-w       toggle wide table columns", "", "Views and commands", "  :            command prompt (top, k9s-style)", "  :ws          switch workspace (project); :ws <name> jumps directly", "  :use <env>   set the active environment (:ctx is a k9s-style alias)", "  :attach f=./x attach a multipart file to a request", "  :collections browse collections; :req :sc :env for the rest", "", "Response split view", "  Tab          switch focus between request and response", "  h/l, ←/→     focus request / response pane", "  j/k          scroll one line; Ctrl-f/b page; g/G top/end", "  H/M/L        visible top/middle/bottom", "  /            search response; Enter applies live query", "  n/N          next/previous response match", "  y            request pane: toggle sent / definition", "  x            request pane: reveal / hide secrets", "  e            edit the request", "  Ctrl-a       show resource aliases", "  Tab/Ctrl-f/→ accept command suggestion", "  ↑/↓          choose command suggestion", "  Ctrl-u       clear command; Ctrl-w removes its last word", "  Ctrl-r       reload workspace", "  ?            this help", "  Ctrl-c, :q   quit (or cancel a running request)",
	}, "\n")
	if len(m.hotkeys) == 0 {
		return help
	}
	lines := []string{help, "", "Configured hotkeys"}
	for _, key := range sortedKeys(m.hotkeys) {
		lines = append(lines, "  "+key+"  "+m.hotkeys[key].description)
	}
	return strings.Join(lines, "\n")
}

func (m *Model) aliasContent() string {
	keys := sortedKeys(m.aliases)
	lines := []string{"Type :<alias> to open a resource view."}
	for _, key := range keys {
		lines = append(lines, fmt.Sprintf("  :%-12s %s", key, m.aliases[key]))
	}
	return strings.Join(lines, "\n")
}

func (m *Model) renderFooter(width int) string {
	tabs := m.renderTabs(width)
	message := m.message
	if message == "" {
		message = "Ready"
	}
	if m.running {
		message = "● " + message
	}
	shortcuts := m.contextualShortcuts()
	if width < 90 {
		shortcuts = "[j/k] move  [enter] open  [:] command  [?] help"
	}
	message = truncate(message, max(1, width-lipgloss.Width(shortcuts)-3))
	return tabs + "\n" + lipgloss.NewStyle().Foreground(muted).Width(width).Render(" 😎 "+message+strings.Repeat(" ", max(1, width-lipgloss.Width(message)-lipgloss.Width(shortcuts)-4))+shortcuts)
}

func (m *Model) contextualShortcuts() string {
	if m.mode == commandMode {
		return "[enter] execute  [tab] complete  [esc] cancel"
	}
	if m.inSplitView() {
		return "[tab] pane  [j/k] scroll  [y] view  [x] reveal  [/] search  [q] close"
	}
	if m.overlay != noOverlay {
		shortcuts := "[j/k] scroll  [g/G] top/end  [/] search  [n/N] match  [q] close"
		if m.overlay == yamlOverlay || m.overlay == describeOverlay {
			shortcuts += "  [e] edit  [r] run"
		}
		return shortcuts
	}
	switch m.section {
	case requestsSection:
		return "[j/k] move  [enter] describe  [e] edit  [r] run  [/] filter  [:] command"
	case collectionsSection:
		return "[j/k] move  [enter] open  [d] describe  [r] run  [:] command"
	case scenariosSection:
		return "[j/k] move  [enter] describe  [e] edit  [r] run  [:] command"
	case environmentsSection:
		return "[j/k] move  [enter] describe  [e] edit  [r] select  [:] command"
	default:
		return "[j/k] move  [enter] open  [:] command  [?] help"
	}
}

func (m *Model) renderTabs(width int) string {
	parts := []string{}
	for _, view := range []section{requestsSection, collectionsSection, scenariosSection, environmentsSection} {
		label := " <" + view.String() + "> "
		style := lipgloss.NewStyle().Foreground(muted)
		if view == m.section {
			style = lipgloss.NewStyle().Background(yellow).Foreground(lipgloss.Color("#1A1B26")).Bold(true)
		}
		parts = append(parts, style.Render(label))
	}
	value := strings.Join(parts, "")
	if m.section == requestsSection && m.scope != "" {
		crumb := lipgloss.NewStyle().Foreground(muted).Render("  collections › " + m.scope)
		value += crumb
	}
	return lipgloss.NewStyle().Width(width).Render(value)
}

func (m *Model) tableHeight() int { return max(1, m.height-5) }
func (m *Model) modalHeight() int { return min(max(10, m.height-6), m.height-4) }
func (m *Model) requestColumnWidths(width int) (int, int) {
	nameWidth := min(28, max(16, width/3))
	reserved := 14
	if m.wide {
		reserved += 19
	}
	return nameWidth, max(12, width-nameWidth-reserved)
}
func methodStyle(method string) lipgloss.Style {
	color := blue
	switch strings.ToUpper(method) {
	case "POST":
		color = green
	case "PUT", "PATCH":
		color = yellow
	case "DELETE":
		color = red
	}
	return lipgloss.NewStyle().Bold(true).Foreground(color)
}

// highlightYAMLLine applies k9s-style syntax colouring to a single YAML line:
// comments, keys, and scalar values (strings, numbers, booleans/null) each get
// their own colour. It is applied after wrapping so it only ever adds ANSI.
func highlightYAMLLine(line string) string {
	trimmed := strings.TrimLeft(line, " ")
	indent := line[:len(line)-len(trimmed)]
	if trimmed == "" {
		return line
	}
	if strings.HasPrefix(trimmed, "#") {
		return indent + lipgloss.NewStyle().Foreground(muted).Italic(true).Render(trimmed)
	}
	prefix := ""
	rest := trimmed
	for strings.HasPrefix(rest, "- ") {
		prefix += "- "
		rest = rest[2:]
	}
	if idx := strings.Index(rest, ":"); idx > 0 && (idx+1 == len(rest) || rest[idx+1] == ' ') && isYAMLKey(rest[:idx]) {
		key := lipgloss.NewStyle().Foreground(blue).Render(rest[:idx]) + ":"
		return indent + prefix + key + highlightYAMLValue(rest[idx+1:])
	}
	return indent + prefix + highlightYAMLValue(rest)
}

func highlightYAMLValue(value string) string {
	lead := value[:len(value)-len(strings.TrimLeft(value, " "))]
	scalar := strings.TrimLeft(value, " ")
	if scalar == "" {
		return value
	}
	var style lipgloss.Style
	switch {
	case scalar == "true" || scalar == "false" || scalar == "null" || scalar == "~":
		style = lipgloss.NewStyle().Foreground(purple)
	case isNumber(scalar):
		style = lipgloss.NewStyle().Foreground(yellow)
	default:
		style = lipgloss.NewStyle().Foreground(green)
	}
	return lead + style.Render(scalar)
}

func isYAMLKey(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && r != '_' && r != '-' && r != '.' {
			return false
		}
	}
	return true
}

func isNumber(value string) bool {
	_, err := strconv.ParseFloat(value, 64)
	return err == nil
}

func formatBody(body []byte, width int) string {
	var value any
	if json.Unmarshal(body, &value) == nil {
		formatted, _ := json.MarshalIndent(value, "", "  ")
		return wrap(string(formatted), width)
	}
	return wrap(string(body), width)
}
func wrap(value string, width int) string {
	value = sanitizeText(value)
	if width < 10 {
		return value
	}
	var lines []string
	for _, line := range strings.Split(value, "\n") {
		for len([]rune(line)) > width {
			runes := []rune(line)
			lines = append(lines, string(runes[:width]))
			line = string(runes[width:])
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// sanitizeText normalises line endings and removes control characters that would
// corrupt the pane borders when drawn — carriage returns move the cursor back to
// column 0, and stray escape sequences from a response body could inject terminal
// codes. Newlines are preserved so wrap can split on them; tabs become spaces so
// widths stay predictable.
func sanitizeText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n':
			return r
		case r == '\t':
			return ' '
		case r < 0x20 || r == 0x7f:
			return -1
		default:
			return r
		}
	}, value)
}
func truncate(value string, width int) string {
	runes := []rune(value)
	if width <= 0 {
		return ""
	}
	if len(runes) <= width {
		return value
	}
	if width == 1 {
		return "…"
	}
	return string(runes[:width-1]) + "…"
}
func relative(root, path string) string {
	value, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return value
}
func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
func trimRune(value string) string {
	runes := []rune(value)
	if len(runes) == 0 {
		return ""
	}
	return string(runes[:len(runes)-1])
}
func trimWord(value string) string {
	value = strings.TrimRight(value, " \t")
	index := strings.LastIndexAny(value, " \t")
	if index < 0 {
		return ""
	}
	return strings.TrimRight(value[:index+1], " \t")
}
func isTextKey(key string) bool { return len([]rune(key)) == 1 }
func firstOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func loadAliases(root string) (map[string]string, error) {
	aliases := map[string]string{"requests": "requests", "request": "requests", "req": "requests", "r": "requests", "apis": "requests", "collections": "collections", "collection": "collections", "col": "collections", "cols": "collections", "scenarios": "scenarios", "scenario": "scenarios", "sc": "scenarios", "environments": "environments", "environment": "environments", "env": "environments", "envs": "environments", "contexts": "environments", "workspaces": "workspaces", "workspace": "workspaces", "ws": "workspaces"}
	for _, path := range interactionPaths(root, "aliases.yaml") {
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var config struct {
			Aliases map[string]string `yaml:"aliases"`
		}
		if err := yaml.Unmarshal(data, &config); err != nil {
			return nil, fmt.Errorf("read aliases: %w", err)
		}
		for alias, target := range config.Aliases {
			target = strings.ToLower(strings.TrimSpace(target))
			if target != "requests" && target != "collections" && target != "scenarios" && target != "environments" && target != "workspaces" {
				return nil, fmt.Errorf("alias %q targets unknown view %q", alias, target)
			}
			aliases[strings.ToLower(alias)] = target
		}
	}
	return aliases, nil
}

func loadHotkeys(root string) (map[string]hotkey, error) {
	bindings := map[string]hotkey{}
	for _, path := range interactionPaths(root, "hotkeys.yaml") {
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var config struct {
			HotKeys map[string]struct {
				ShortCut    string `yaml:"shortCut"`
				Description string `yaml:"description"`
				Command     string `yaml:"command"`
			} `yaml:"hotKeys"`
		}
		if err := yaml.Unmarshal(data, &config); err != nil {
			return nil, fmt.Errorf("read hotkeys: %w", err)
		}
		for name, definition := range config.HotKeys {
			key := normalizeKey(firstOr(definition.ShortCut, name))
			if key == "" || strings.TrimSpace(definition.Command) == "" {
				return nil, fmt.Errorf("hotkey %q requires shortCut and command", name)
			}
			bindings[key] = hotkey{description: firstOr(definition.Description, definition.Command), command: definition.Command}
		}
	}
	return bindings, nil
}

func interactionPaths(root, file string) []string {
	paths := []string{}
	if dir, err := config.Dir(); err == nil {
		paths = append(paths, filepath.Join(dir, file))
	}
	return append(paths, filepath.Join(root, ".arbor", file))
}

func normalizeKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.ReplaceAll(value, "-", "+")
}
