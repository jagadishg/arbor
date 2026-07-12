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

type viewLocation struct {
	section  section
	filter   string
	scope    string
	selected int
}

type requestDoneMsg model.RequestResult
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

	overlay        overlay
	overlayOffset  int
	width          int
	height         int
	running        bool
	cancel         context.CancelFunc
	requestResult  *model.RequestResult
	scenarioReport *model.ScenarioReport
	message        string
	aliases        map[string]string
	hotkeys        map[string]hotkey
	workspaces     []config.Entry
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
		m.running, m.cancel, m.requestResult, m.scenarioReport = false, nil, &result, nil
		m.overlay = responseOverlay
		if result.Passed() {
			m.message = "Request completed"
		} else {
			m.message = "Request failed"
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
			m.message = "Workspace reloaded"
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
	if m.mode == commandMode || m.mode == filterMode {
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
	case "G", "end":
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
		}
	case "d":
		m.overlay, m.overlayOffset = describeOverlay, 0
	case "y":
		m.overlay, m.overlayOffset = yamlOverlay, 0
	case "l":
		if m.hasResultForSelected() {
			m.overlay, m.overlayOffset = responseOverlay, 0
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

func (m *Model) handleOverlayKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "q", "esc", "d", "y", "l", "?", "ctrl+a":
		m.overlay, m.overlayOffset = noOverlay, 0
	case "ctrl+c":
		if m.running && m.cancel != nil {
			m.cancel()
			m.message = "Cancelling request…"
		} else {
			m.overlay = noOverlay
		}
	case "j", "down", "ctrl+d", "pagedown":
		m.overlayOffset += max(1, m.modalHeight()/3)
	case "k", "up", "ctrl+u", "pageup":
		m.overlayOffset = max(0, m.overlayOffset-max(1, m.modalHeight()/3))
	case "e":
		if m.overlay == yamlOverlay || m.overlay == describeOverlay {
			m.overlay = noOverlay
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
	if m.mode == filterMode {
		return m.handleFilterInput(key)
	}
	return m.handleCommandInput(key)
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
	default:
		m.message = "Unknown command: " + fields[0] + " — Ctrl-a lists aliases"
	}
	return m, nil
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
	m.running, m.cancel, m.message = true, cancel, "Running "+ref+"…"
	loaded, environment := m.app, m.environment
	return func() tea.Msg { return requestDoneMsg(loaded.RunRequest(ctx, ref, environment, nil)) }
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
		for _, command := range []suggestion{{"requests", "browse requests"}, {"collections", "browse collections"}, {"scenarios", "browse scenarios"}, {"environments", "browse environments"}, {"workspaces", "switch workspace"}, {"aliases", "show resource aliases"}, {"help", "show keyboard shortcuts"}, {"reload", "reload workspace files"}, {"use", "switch environment"}, {"ctx", "switch environment"}, {"run", "run a request or scenario"}, {"quit", "quit Arbor"}} {
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
	foreground         = lipgloss.Color("#C0CAF5")
)

func (m *Model) render() string {
	width, height := max(m.width, 50), max(m.height, 12)
	header, footer := m.renderHeader(width), m.renderFooter(width)
	if m.overlay != noOverlay {
		return lipgloss.NewStyle().Foreground(foreground).Render(header + "\n" + m.renderOverlay(width, height-lipgloss.Height(header)))
	}
	sections := header
	promptHeight := 0
	if m.mode == commandMode || m.mode == filterMode {
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
	if m.mode == filterMode {
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
	if width < 78 {
		return lipgloss.NewStyle().Background(panel).Width(width).Render(" ARBOR  workspace: " + truncate(m.app.Workspace.Name, max(10, width-30)) + "  env: " + firstOr(m.environment, "none"))
	}
	leftWidth := max(34, width-34)
	rows := []string{
		"ARBOR Workspace: " + m.app.Workspace.Name,
		"Root:      " + m.app.Workspace.Root,
		"Environment: " + firstOr(m.environment, "none"),
		fmt.Sprintf("Resources: %d requests · %d scenarios · %d environments", len(m.app.Workspace.Requests), len(m.app.Workspace.Scenarios), len(m.app.Workspace.Environments)),
		"Arbor Rev: " + buildinfo.Version,
	}
	logo := []string{
		"    _    ____  ____   ___  ____",
		"   / \\  |  _ \\| __ ) / _ \\|  _ \\",
		"  / _ \\ | |_) |  _ \\| | | | |_) |",
		" / ___ \\|  _ <| |_) | |_| |  _ <",
		"/_/   \\_\\_| \\_\\____/ \\___/|_| \\_\\",
	}
	lines := make([]string, 0, len(rows))
	for index, row := range rows {
		left := lipgloss.NewStyle().Foreground(yellow).Render(truncate(row, leftWidth))
		right := lipgloss.NewStyle().Foreground(green).Bold(true).Render(logo[index])
		lines = append(lines, left+strings.Repeat(" ", max(1, width-lipgloss.Width(left)-lipgloss.Width(right)))+right)
	}
	return lipgloss.NewStyle().Background(panel).Width(width).Render(strings.Join(lines, "\n"))
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

// renderOverlay draws describe/YAML/response/help panels as a full-width framed
// pane (like k9s), filling the area beneath the header. Content is wrapped to the
// interior width and each line is framed to an exact width so borders stay aligned.
func (m *Model) renderOverlay(width, height int) string {
	inner := max(20, width-2)
	height = max(6, height)
	title, content := m.overlayContent(inner - 2)
	content = wrap(content, inner-2)

	titleText := fmt.Sprintf("─ %s ", title)
	top := "┌" + lipgloss.NewStyle().Foreground(blue).Bold(true).Render(titleText) + strings.Repeat("─", max(0, inner-lipgloss.Width(titleText))) + "┐"

	hint := "[esc] close  [j/k] scroll"
	switch m.overlay {
	case describeOverlay, yamlOverlay:
		hint += "  [e] edit  [r] run"
	case responseOverlay:
		hint += "  [r] run again"
	}

	allLines := strings.Split(content, "\n")
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
		lines = append(lines, m.frameLine(" "+styleLine(allLines[index]), inner))
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
		"Navigation", "  j/k, ↑/↓     move selection", "  g/G          first/last resource", "  Ctrl-f/b     page down/up", "  Enter        drill into a collection / switch workspace (or describe)", "  Esc, q, h, [ back through view history", "  ], →         forward through view history", "", "Resource actions", "  Enter, d     describe selected resource", "  y            show YAML", "  e            edit in $EDITOR", "  r            run selected request or scenario", "  l            show last response (like logs)", "  Ctrl-w       toggle wide table columns", "", "Views and commands", "  :            command prompt (top, k9s-style)", "  :ws          switch workspace (project); :ws <name> jumps directly", "  :use <env>   set the active environment (:ctx is a k9s-style alias)", "  :collections browse collections; :req :sc :env for the rest", "  Ctrl-a       show resource aliases", "  /            filter the current resource view", "  Tab/Ctrl-f/→ accept command suggestion", "  ↑/↓          choose command suggestion", "  Ctrl-u       clear command; Ctrl-w removes its last word", "  Ctrl-r       reload workspace", "  ?            this help", "  Ctrl-c, :q   quit (or cancel a running request)",
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
	shortcuts := "[d] describe  [y] yaml  [r] run  [/] filter  [:] command  [ctrl-a] aliases  [?] help"
	if width < 105 {
		shortcuts = "[d] describe  [r] run  [/] filter  [:] command  [?] help"
	}
	if width < 90 {
		shortcuts = "[d]  [r]  [/]  [:]  [?]"
	}
	message = truncate(message, max(1, width-lipgloss.Width(shortcuts)-3))
	return tabs + "\n" + lipgloss.NewStyle().Foreground(muted).Width(width).Render(" 😎 "+message+strings.Repeat(" ", max(1, width-lipgloss.Width(message)-lipgloss.Width(shortcuts)-4))+shortcuts)
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
