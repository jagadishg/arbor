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
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/jagadishg/arbor/internal/app"
	"github.com/jagadishg/arbor/internal/model"
	"gopkg.in/yaml.v3"
)

type section int

const (
	requestsSection section = iota
	scenariosSection
	environmentsSection
)

func (s section) String() string {
	return []string{"requests", "scenarios", "environments"}[s]
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
	selected int
}

type requestDoneMsg model.RequestResult
type scenarioDoneMsg model.ScenarioReport
type reloadDoneMsg struct {
	app     *app.App
	aliases map[string]string
	err     error
}
type editorDoneMsg struct{ err error }

type Model struct {
	ctx         context.Context
	dir         string
	app         *app.App
	environment string
	section     section
	selected    int
	filter      string
	history     []viewLocation

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
}

type suggestion struct {
	value       string
	description string
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
	return &Model{ctx: ctx, dir: directory, app: loaded, environment: environment, aliases: aliases, width: 100, height: 30}
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
			m.app, m.aliases, m.selected = msg.app, msg.aliases, 0
			m.message = "Workspace reloaded"
		}
	case editorDoneMsg:
		if msg.err != nil {
			m.message = "Editor failed: " + msg.err.Error()
		} else {
			return m, m.reloadCmd()
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
	switch key {
	case "ctrl+c", "q":
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
	case "ctrl+d", "pagedown":
		m.move(max(1, m.tableHeight()/2))
	case "ctrl+u", "pageup":
		m.move(-max(1, m.tableHeight()/2))
	case "enter", "d":
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
	case "esc", "h", "left":
		m.goBack()
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
	if target, ok := m.aliases[verb]; ok {
		filter := ""
		if len(fields) > 1 && strings.HasPrefix(fields[1], "/") {
			filter = strings.TrimPrefix(strings.Join(fields[1:], " "), "/")
		}
		m.navigate(sectionFor(target), filter)
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
		if len(fields) != 2 {
			m.message = "Usage: :use <environment>"
			return m, nil
		}
		if _, ok := m.app.Workspace.EnvironmentByName(fields[1]); !ok {
			m.message = "Environment not found: " + fields[1]
		} else {
			m.environment, m.message = fields[1], "Context set to "+fields[1]
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

func (m *Model) navigate(next section, filter string) {
	if m.section != next || m.filter != filter {
		m.history = append(m.history, viewLocation{m.section, m.filter, m.selected})
	}
	m.section, m.filter, m.selected = next, filter, 0
	m.message = "Viewing " + next.String()
}

func (m *Model) goBack() {
	if len(m.history) == 0 {
		m.message = "Already at the root view"
		return
	}
	last := m.history[len(m.history)-1]
	m.history = m.history[:len(m.history)-1]
	m.section, m.filter, m.selected = last.section, last.filter, last.selected
	m.message = "Back to " + m.section.String()
}

func sectionFor(target string) section {
	switch target {
	case "scenarios":
		return scenariosSection
	case "environments":
		return environmentsSection
	default:
		return requestsSection
	}
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
		m.environment, m.message = value.Name, "Context set to "+value.Name
	}
	return nil
}

func (m *Model) runRequest(ref string) tea.Cmd {
	ctx, cancel := context.WithCancel(m.ctx)
	m.running, m.cancel, m.message = true, cancel, "Running "+ref+"…"
	loaded, environment := m.app, m.environment
	return func() tea.Msg { return requestDoneMsg(loaded.RunRequest(ctx, ref, environment, nil)) }
}

func (m *Model) runScenario(ref string) tea.Cmd {
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
		return reloadDoneMsg{app: loaded, aliases: aliases}
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
	case model.Scenario:
		return value.Path
	case model.Environment:
		return value.Path
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
		sort.Slice(requests, func(i, j int) bool { return requests[i].Ref() < requests[j].Ref() })
		for _, request := range requests {
			values = append(values, item{request.Ref(), request})
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
	case model.Scenario:
		return value.Ref() + " " + value.Name
	case model.Environment:
		return value.Name
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
				values = append(values, suggestion{verb + " " + environment.Name, "switch context"})
			}
		}
	} else {
		for alias, target := range m.aliases {
			if strings.HasPrefix(alias, input) {
				values = append(values, suggestion{alias, target + " view"})
			}
		}
		for _, command := range []suggestion{{"aliases", "show resource aliases"}, {"help", "show keyboard shortcuts"}, {"reload", "reload workspace files"}, {"use", "switch environment"}, {"ctx", "switch environment"}, {"run", "run a request or scenario"}, {"quit", "quit Arbor"}} {
			if strings.HasPrefix(command.value, input) {
				values = append(values, command)
			}
		}
	}
	sort.Slice(values, func(i, j int) bool { return values[i].value < values[j].value })
	return values
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
	muted              = lipgloss.Color("#707A8C")
	panel              = lipgloss.Color("#24283B")
	selectedBackground = lipgloss.Color("#364A82")
	foreground         = lipgloss.Color("#C0CAF5")
)

func (m *Model) render() string {
	width, height := max(m.width, 50), max(m.height, 12)
	header, crumbs, footer := m.renderHeader(width), m.renderCrumbs(width), m.renderFooter(width)
	bodyHeight := max(1, height-lipgloss.Height(header)-lipgloss.Height(crumbs)-lipgloss.Height(footer))
	if m.overlay != noOverlay {
		return lipgloss.NewStyle().Foreground(foreground).Render(header + "\n" + crumbs + "\n" + m.renderOverlay(width, height-2))
	}
	body := m.renderTable(width, bodyHeight)
	return lipgloss.NewStyle().Foreground(foreground).Render(header + "\n" + crumbs + "\n" + body + "\n" + footer)
}

func (m *Model) renderHeader(width int) string {
	brand := lipgloss.NewStyle().Bold(true).Foreground(green).Render(" ARBOR ")
	context := lipgloss.NewStyle().Foreground(yellow).Render("context: " + firstOr(m.environment, "none"))
	left := brand + "  " + lipgloss.NewStyle().Bold(true).Render(m.app.Workspace.Name)
	return lipgloss.NewStyle().Background(panel).Width(width).Render(left + strings.Repeat(" ", max(1, width-lipgloss.Width(left)-lipgloss.Width(context)-2)) + context + " ")
}

func (m *Model) renderCrumbs(width int) string {
	crumbs := " arbor > " + m.app.Workspace.Name + " > " + m.section.String()
	if m.filter != "" {
		crumbs += " /" + m.filter
	}
	return lipgloss.NewStyle().Foreground(muted).Width(width).Render(crumbs)
}

func (m *Model) renderTable(width, height int) string {
	title := lipgloss.NewStyle().Bold(true).Foreground(blue).Render(" " + strings.ToUpper(m.section.String()))
	if m.filter != "" {
		title += lipgloss.NewStyle().Foreground(yellow).Render("  FILTER: " + m.filter)
	}
	items := m.items()
	lines := []string{title, m.tableHeader(width)}
	available := max(1, height-len(lines)-1)
	start := 0
	if m.selected >= available {
		start = m.selected - available + 1
	}
	end := min(len(items), start+available)
	for index := start; index < end; index++ {
		lines = append(lines, m.tableRow(index, items[index], width))
	}
	if len(items) == 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(muted).Render("  No resources match this view"))
	}
	return lipgloss.NewStyle().Width(width).Height(height).Render(strings.Join(lines, "\n"))
}

func (m *Model) tableHeader(width int) string {
	style := lipgloss.NewStyle().Foreground(muted).Bold(true)
	switch m.section {
	case requestsSection:
		nameWidth, urlWidth := m.requestColumnWidths(width)
		return style.Render(fmt.Sprintf("  %-*s %-8s %-*s %s", nameWidth, "NAME", "METHOD", urlWidth, "URL", "A"))
	case scenariosSection:
		nameWidth := max(18, width-24)
		return style.Render(fmt.Sprintf("  %-*s %-8s %s", nameWidth, "NAME", "STEPS", "ON FAILURE"))
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
		return style.Render(fmt.Sprintf("%s%-*s %-8s %-*s %d", prefix, nameWidth, truncate(value.Ref(), nameWidth), methodStyle(value.Method).Render(strings.ToUpper(value.Method)), urlWidth, truncate(value.URL, urlWidth), len(value.Assert)))
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
	}
	return ""
}

func (m *Model) renderOverlay(width, height int) string {
	title, content := m.overlayContent(width - 10)
	modalWidth, modalHeight := min(max(50, width-12), width-4), min(max(10, height-6), height-4)
	lines := strings.Split(content, "\n")
	visibleHeight := max(1, modalHeight-4)
	if m.overlayOffset > max(0, len(lines)-visibleHeight) {
		m.overlayOffset = max(0, len(lines)-visibleHeight)
	}
	end := min(len(lines), m.overlayOffset+visibleHeight)
	content = strings.Join(lines[m.overlayOffset:end], "\n")
	header := lipgloss.NewStyle().Bold(true).Foreground(blue).Render(title)
	footer := lipgloss.NewStyle().Foreground(muted).Render("[esc] close  [j/k] scroll  [r] run  [e] edit")
	box := lipgloss.NewStyle().Width(modalWidth).Height(modalHeight).Padding(1, 2).Border(lipgloss.RoundedBorder()).BorderForeground(blue).Background(panel).Render(header + "\n\n" + content + "\n\n" + footer)
	left, top := max(0, (width-lipgloss.Width(box))/2), max(0, (height-lipgloss.Height(box))/2)
	return strings.Repeat("\n", top) + strings.Repeat(" ", left) + box
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
		lines := []string{"Name: " + value.Name, "Reference: " + value.Ref(), "Method: " + strings.ToUpper(value.Method), "URL: " + value.URL, "File: " + relative(m.app.Workspace.Root, value.Path)}
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
	case model.Scenario:
		lines := []string{"Name: " + value.Name, "Reference: " + value.Ref(), "File: " + relative(m.app.Workspace.Root, value.Path), "", "Steps"}
		for index, step := range value.Steps {
			lines = append(lines, fmt.Sprintf("  %d. %s", index+1, step.Request))
		}
		return strings.Join(lines, "\n")
	case model.Environment:
		lines := []string{"Name: " + value.Name, "File: " + relative(m.app.Workspace.Root, value.Path), "", "Variables"}
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
	return strings.Join([]string{
		"Navigation", "  j/k, ↑/↓     move selection", "  g/G          first/last resource", "  Ctrl-d/u     page down/up", "  Esc or h     return to the previous view", "", "Resource actions", "  Enter, d     describe selected resource", "  y            show YAML", "  e            edit in $EDITOR", "  r            run selected request or scenario", "  l            show last response (like logs)", "", "Views and commands", "  :            command mode", "  Ctrl-a       show resource aliases", "  /            filter the current resource view", "  Tab/Ctrl-f/→ accept command suggestion", "  ↑/↓          choose command suggestion", "  Ctrl-u       clear command; Ctrl-w removes its last word", "  Ctrl-r       reload workspace", "  ?            this help", "  Ctrl-c, q    quit (or cancel a running request)",
	}, "\n")
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
	if m.mode == filterMode {
		return lipgloss.NewStyle().Foreground(yellow).Width(width).Render(" /" + m.input + "   [enter] keep filter  [esc] cancel")
	}
	if m.mode == commandMode {
		return m.renderCommandFooter(width)
	}
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
	if width < 78 {
		shortcuts = "[d] describe  [r] run  [:] command  [?] help"
	}
	message = truncate(message, max(1, width-lipgloss.Width(shortcuts)-3))
	return lipgloss.NewStyle().Foreground(muted).Width(width).Render(" " + message + strings.Repeat(" ", max(1, width-lipgloss.Width(message)-lipgloss.Width(shortcuts)-2)) + shortcuts)
}

func (m *Model) renderCommandFooter(width int) string {
	suggestions := m.suggestions()
	hint := "[enter] execute  [tab] complete  [esc] cancel"
	if len(suggestions) > 0 {
		index := min(m.suggestion, len(suggestions)-1)
		current := suggestions[index]
		hint = "[" + current.value + "] " + current.description + "  [tab] complete"
	}
	return lipgloss.NewStyle().Foreground(yellow).Width(width).Render(" :" + m.input + "\n " + truncate(hint, width-2))
}

func (m *Model) tableHeight() int { return max(1, m.height-5) }
func (m *Model) modalHeight() int { return min(max(10, m.height-6), m.height-4) }
func (m *Model) requestColumnWidths(width int) (int, int) {
	nameWidth := min(28, max(16, width/3))
	return nameWidth, max(12, width-nameWidth-14)
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
func trimWord(value string) string { return strings.TrimRight(strings.TrimRight(value, " \t"), "^ \t") }
func isTextKey(key string) bool    { return len([]rune(key)) == 1 }
func firstOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func loadAliases(root string) (map[string]string, error) {
	aliases := map[string]string{"requests": "requests", "request": "requests", "req": "requests", "r": "requests", "apis": "requests", "scenarios": "scenarios", "scenario": "scenarios", "sc": "scenarios", "environments": "environments", "environment": "environments", "env": "environments", "envs": "environments"}
	path := filepath.Join(root, ".arbor", "aliases.yaml")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return aliases, nil
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
		if target != "requests" && target != "scenarios" && target != "environments" {
			return nil, fmt.Errorf("alias %q targets unknown view %q", alias, target)
		}
		aliases[strings.ToLower(alias)] = target
	}
	return aliases, nil
}
