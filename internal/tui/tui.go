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

	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/jagadishg/arbor/internal/app"
	"github.com/jagadishg/arbor/internal/model"
)

type section int

const (
	requestsSection section = iota
	scenariosSection
	environmentsSection
)

type inputMode int

const (
	normalMode inputMode = iota
	filterMode
	commandMode
	helpMode
)

type requestDoneMsg model.RequestResult
type scenarioDoneMsg model.ScenarioReport
type reloadDoneMsg struct {
	app *app.App
	err error
}
type editorDoneMsg struct{ err error }

type Model struct {
	ctx            context.Context
	dir            string
	app            *app.App
	environment    string
	section        section
	selected       int
	mode           inputMode
	input          string
	filter         string
	width          int
	height         int
	running        bool
	cancel         context.CancelFunc
	requestResult  *model.RequestResult
	scenarioReport *model.ScenarioReport
	message        string
	responseOffset int
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
	return &Model{ctx: ctx, dir: directory, app: loaded, environment: environment, width: 100, height: 30}
}

func (m *Model) Init() tea.Cmd { return nil }

func (m *Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case requestDoneMsg:
		result := model.RequestResult(msg)
		m.running, m.cancel, m.requestResult, m.scenarioReport = false, nil, &result, nil
		if result.Passed() {
			m.message = "Request passed"
		} else {
			m.message = "Request failed"
		}
		return m, nil
	case scenarioDoneMsg:
		report := model.ScenarioReport(msg)
		m.running, m.cancel, m.scenarioReport, m.requestResult = false, nil, &report, nil
		if report.Passed() {
			m.message = "Scenario passed"
		} else {
			m.message = "Scenario failed"
		}
		return m, nil
	case reloadDoneMsg:
		if msg.err != nil {
			m.message = "Reload failed: " + msg.err.Error()
		} else {
			m.app = msg.app
			m.selected = 0
			m.message = "Workspace reloaded"
		}
		return m, nil
	case editorDoneMsg:
		if msg.err != nil {
			m.message = "Editor failed: " + msg.err.Error()
			return m, nil
		}
		return m, m.reloadCmd()
	case tea.KeyPressMsg:
		return m.handleKey(msg.Keystroke())
	}
	return m, nil
}

func (m *Model) handleKey(key string) (tea.Model, tea.Cmd) {
	if m.mode == helpMode {
		if key == "?" || key == "esc" || key == "q" {
			m.mode = normalMode
		}
		return m, nil
	}
	if m.mode == filterMode || m.mode == commandMode {
		return m.handleInput(key)
	}
	if m.running && (key == "ctrl+c" || key == "esc") {
		if m.cancel != nil {
			m.cancel()
		}
		m.message = "Cancelling…"
		return m, nil
	}
	switch key {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "j", "down":
		m.move(1)
	case "k", "up":
		m.move(-1)
	case "g":
		m.selected = 0
		m.responseOffset = 0
	case "G":
		items := m.items()
		if len(items) > 0 {
			m.selected = len(items) - 1
		}
		m.responseOffset = 0
	case "tab", "l":
		m.section = (m.section + 1) % 3
		m.selected = 0
		m.filter = ""
	case "shift+tab", "h":
		m.section = (m.section + 2) % 3
		m.selected = 0
		m.filter = ""
	case "1":
		m.section = requestsSection
		m.selected = 0
	case "2":
		m.section = scenariosSection
		m.selected = 0
	case "3":
		m.section = environmentsSection
		m.selected = 0
	case "/":
		m.mode, m.input = filterMode, m.filter
	case ":":
		m.mode, m.input = commandMode, ""
	case "?":
		m.mode = helpMode
	case "enter", "r":
		return m, m.runSelected()
	case "ctrl+r":
		m.message = "Reloading…"
		return m, m.reloadCmd()
	case "e":
		return m, m.editSelected()
	case "ctrl+d":
		m.responseOffset += max(1, (m.height-8)/2)
	case "ctrl+u":
		m.responseOffset = max(0, m.responseOffset-max(1, (m.height-8)/2))
	}
	return m, nil
}

func (m *Model) handleInput(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc":
		m.mode, m.input = normalMode, ""
		return m, nil
	case "enter":
		input, mode := strings.TrimSpace(m.input), m.mode
		m.mode, m.input, m.selected = normalMode, "", 0
		if mode == filterMode {
			m.filter = input
			return m, nil
		}
		return m.executeCommand(input)
	case "backspace":
		if len(m.input) > 0 {
			runes := []rune(m.input)
			m.input = string(runes[:len(runes)-1])
		}
	default:
		if len([]rune(key)) == 1 {
			m.input += key
		}
	}
	if m.mode == filterMode {
		m.filter = m.input
		m.selected = 0
	}
	return m, nil
}

func (m *Model) executeCommand(command string) (tea.Model, tea.Cmd) {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return m, nil
	}
	switch fields[0] {
	case "q", "quit":
		return m, tea.Quit
	case "requests", "req":
		m.section = requestsSection
	case "scenarios", "sc":
		m.section = scenariosSection
	case "environments", "envs":
		m.section = environmentsSection
	case "help":
		m.mode = helpMode
	case "reload":
		return m, m.reloadCmd()
	case "use":
		if len(fields) != 2 {
			m.message = "Usage: :use <environment>"
			return m, nil
		}
		if _, ok := m.app.Workspace.EnvironmentByName(fields[1]); !ok {
			m.message = "Environment not found: " + fields[1]
		} else {
			m.environment = fields[1]
			m.message = "Using environment " + fields[1]
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
		m.message = "Unknown command: " + fields[0]
	}
	return m, nil
}

func (m *Model) move(delta int) {
	count := len(m.items())
	if count == 0 {
		m.selected = 0
		return
	}
	m.selected = (m.selected + delta + count) % count
	m.responseOffset = 0
}

func (m *Model) runSelected() tea.Cmd {
	items := m.items()
	if len(items) == 0 || m.running {
		return nil
	}
	switch item := items[m.selected].value.(type) {
	case model.Request:
		return m.runRequest(item.Ref())
	case model.Scenario:
		return m.runScenario(item.Ref())
	case model.Environment:
		m.environment = item.Name
		m.message = "Using environment " + item.Name
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
	m.running, m.cancel, m.message = true, cancel, "Running scenario "+ref+"…"
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
	return func() tea.Msg { loaded, err := app.Load(directory); return reloadDoneMsg{app: loaded, err: err} }
}

func (m *Model) editSelected() tea.Cmd {
	items := m.items()
	if len(items) == 0 {
		return nil
	}
	path := ""
	switch item := items[m.selected].value.(type) {
	case model.Request:
		path = item.Path
	case model.Scenario:
		path = item.Path
	case model.Environment:
		path = item.Path
	}
	if path == "" {
		path = m.app.Workspace.Path
	}
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	parts := strings.Fields(editor)
	command := exec.Command(parts[0], append(parts[1:], path)...)
	return tea.ExecProcess(command, func(err error) tea.Msg { return editorDoneMsg{err: err} })
}

type item struct {
	label, subtitle string
	value           any
}

func (m *Model) items() []item {
	var result []item
	switch m.section {
	case requestsSection:
		requests := append([]model.Request(nil), m.app.Workspace.Requests...)
		sort.Slice(requests, func(i, j int) bool { return requests[i].Ref() < requests[j].Ref() })
		for _, value := range requests {
			result = append(result, item{label: value.Ref(), subtitle: strings.ToUpper(value.Method), value: value})
		}
	case scenariosSection:
		scenarios := append([]model.Scenario(nil), m.app.Workspace.Scenarios...)
		sort.Slice(scenarios, func(i, j int) bool { return scenarios[i].Ref() < scenarios[j].Ref() })
		for _, value := range scenarios {
			result = append(result, item{label: value.Ref(), subtitle: fmt.Sprintf("%d steps", len(value.Steps)), value: value})
		}
	case environmentsSection:
		for _, value := range m.app.Workspace.Environments {
			result = append(result, item{label: value.Name, subtitle: fmt.Sprintf("%d vars", len(value.Variables)+len(value.Secrets)), value: value})
		}
	}
	if m.filter == "" {
		return result
	}
	needle := strings.ToLower(m.filter)
	filtered := result[:0]
	for _, value := range result {
		if fuzzyMatch(strings.ToLower(value.label+" "+value.subtitle), needle) {
			filtered = append(filtered, value)
		}
	}
	return filtered
}

func fuzzyMatch(value, pattern string) bool {
	if pattern == "" {
		return true
	}
	index := 0
	for _, char := range value {
		if index < len(pattern) && byte(char) == pattern[index] {
			index++
		}
	}
	return index == len(pattern)
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
	baseStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("#C0CAF5"))
)

func (m *Model) render() string {
	width, height := max(m.width, 40), max(m.height, 12)
	header := m.renderHeader(width)
	footer := m.renderFooter(width)
	bodyHeight := max(1, height-lipgloss.Height(header)-lipgloss.Height(footer))
	if m.mode == helpMode {
		return baseStyle.Render(header + "\n" + m.renderHelp(width, bodyHeight) + "\n" + footer)
	}
	leftWidth := min(max(28, width/3), 42)
	rightWidth := max(10, width-leftWidth-1)
	left := m.renderList(leftWidth, bodyHeight)
	right := m.renderDetail(rightWidth, bodyHeight)
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	return baseStyle.Render(header + "\n" + body + "\n" + footer)
}

func (m *Model) renderHeader(width int) string {
	brand := lipgloss.NewStyle().Bold(true).Foreground(green).Render(" ARBOR ")
	title := lipgloss.NewStyle().Bold(true).Render(m.app.Workspace.Name)
	context := "env: none"
	if m.environment != "" {
		context = "env: " + m.environment
	}
	context = lipgloss.NewStyle().Foreground(yellow).Render(context)
	left := brand + "  " + title
	space := max(1, width-lipgloss.Width(left)-lipgloss.Width(context)-2)
	return lipgloss.NewStyle().Background(panel).Width(width).Render(left + strings.Repeat(" ", space) + context + " ")
}

func (m *Model) renderList(width, height int) string {
	sectionNames := []string{"Requests", "Scenarios", "Environments"}
	title := lipgloss.NewStyle().Bold(true).Foreground(blue).Render(sectionNames[m.section])
	items := m.items()
	lines := []string{title, lipgloss.NewStyle().Foreground(muted).Render(fmt.Sprintf("%d items", len(items))), ""}
	available := max(0, height-len(lines)-1)
	start := 0
	if m.selected >= available && available > 0 {
		start = m.selected - available + 1
	}
	end := min(len(items), start+available)
	for index := start; index < end; index++ {
		prefix := "  "
		style := lipgloss.NewStyle().Width(max(1, width-2))
		if index == m.selected {
			prefix = "› "
			style = style.Background(selectedBackground).Bold(true)
		}
		labelWidth := max(8, width-15)
		label := truncate(items[index].label, labelWidth)
		row := fmt.Sprintf("%s%-*s %s", prefix, labelWidth, label, truncate(items[index].subtitle, 10))
		lines = append(lines, style.Render(row))
	}
	if len(items) == 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(muted).Render("  No matching resources"))
	}
	content := strings.Join(lines, "\n")
	return lipgloss.NewStyle().Width(width).Height(height).Padding(0, 1).BorderRight(true).BorderForeground(panel).Render(content)
}

func (m *Model) renderDetail(width, height int) string {
	var content string
	if m.requestResult != nil {
		content = m.renderRequestResult(width)
	} else if m.scenarioReport != nil {
		content = m.renderScenarioReport(width)
	} else {
		content = m.renderSelected(width)
	}
	lines := strings.Split(content, "\n")
	maxLines := max(1, height-1)
	if m.responseOffset > max(0, len(lines)-maxLines) {
		m.responseOffset = max(0, len(lines)-maxLines)
	}
	end := min(len(lines), m.responseOffset+maxLines)
	visible := strings.Join(lines[m.responseOffset:end], "\n")
	return lipgloss.NewStyle().Width(max(1, width-2)).Height(height).PaddingLeft(2).Render(visible)
}

func (m *Model) renderSelected(width int) string {
	items := m.items()
	if len(items) == 0 {
		return lipgloss.NewStyle().Foreground(muted).Render("Nothing selected")
	}
	switch value := items[m.selected].value.(type) {
	case model.Request:
		lines := []string{lipgloss.NewStyle().Bold(true).Foreground(blue).Render(value.Name), "", methodStyle(value.Method).Render(strings.ToUpper(value.Method)) + "  " + value.URL, "", "Reference: " + value.Ref(), "File: " + relative(m.app.Workspace.Root, value.Path)}
		if len(value.Assert) > 0 {
			lines = append(lines, "", lipgloss.NewStyle().Bold(true).Render("Assertions"))
			for _, assertion := range value.Assert {
				lines = append(lines, "  • "+assertion)
			}
		}
		return strings.Join(lines, "\n")
	case model.Scenario:
		lines := []string{lipgloss.NewStyle().Bold(true).Foreground(blue).Render(value.Name), "", fmt.Sprintf("%d steps", len(value.Steps)), "File: " + relative(m.app.Workspace.Root, value.Path), ""}
		for i, step := range value.Steps {
			lines = append(lines, fmt.Sprintf("%2d  %s", i+1, step.Request))
		}
		return strings.Join(lines, "\n")
	case model.Environment:
		lines := []string{lipgloss.NewStyle().Bold(true).Foreground(yellow).Render(value.Name), "", "File: " + relative(m.app.Workspace.Root, value.Path), "", "Variables"}
		keys := make([]string, 0, len(value.Variables))
		for key := range value.Variables {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			lines = append(lines, fmt.Sprintf("  %-20s %s", key, value.Variables[key]))
		}
		for key := range value.Secrets {
			lines = append(lines, fmt.Sprintf("  %-20s %s", key, "••••••"))
		}
		return strings.Join(lines, "\n")
	}
	return ""
}

func (m *Model) renderRequestResult(width int) string {
	result := m.requestResult
	if result.Error != nil {
		return lipgloss.NewStyle().Foreground(red).Bold(true).Render("Request failed") + "\n\n" + result.Error.Error()
	}
	response := result.Response
	statusColor := green
	if response.StatusCode >= 400 {
		statusColor = red
	} else if response.StatusCode >= 300 {
		statusColor = yellow
	}
	lines := []string{lipgloss.NewStyle().Bold(true).Render(result.Request.Name), "", lipgloss.NewStyle().Bold(true).Foreground(statusColor).Render(response.Status), fmt.Sprintf("%s  ·  %d B", response.Duration.Round(time.Millisecond), response.Size)}
	if len(result.Assertions) > 0 {
		lines = append(lines, "", lipgloss.NewStyle().Bold(true).Render("Assertions"))
		for _, assertion := range result.Assertions {
			mark, color := "✓", green
			if !assertion.Passed {
				mark, color = "✗", red
			}
			line := lipgloss.NewStyle().Foreground(color).Render(mark) + " " + assertion.Expression
			if assertion.Message != "" {
				line += " — " + assertion.Message
			}
			lines = append(lines, line)
		}
	}
	lines = append(lines, "", lipgloss.NewStyle().Bold(true).Render("Response"), formatBody(response.Body, width))
	return strings.Join(lines, "\n")
}

func (m *Model) renderScenarioReport(width int) string {
	report := m.scenarioReport
	status, color := "PASSED", green
	if !report.Passed() {
		status, color = "FAILED", red
	}
	lines := []string{lipgloss.NewStyle().Bold(true).Render(report.Scenario.Name), "", lipgloss.NewStyle().Bold(true).Foreground(color).Render(status), fmt.Sprintf("%d steps  ·  %s", len(report.Steps), report.Duration.Round(time.Millisecond)), ""}
	for index, step := range report.Steps {
		mark, c := "✓", green
		if !step.Passed() {
			mark, c = "✗", red
		}
		code := "—"
		if step.Response != nil {
			code = fmt.Sprint(step.Response.StatusCode)
		}
		lines = append(lines, fmt.Sprintf("%s  %2d  %-*s %s", lipgloss.NewStyle().Foreground(c).Render(mark), index+1, max(8, width-18), truncate(step.Request.Ref(), max(8, width-18)), code))
		if step.Error != nil {
			lines = append(lines, "      "+step.Error.Error())
		}
	}
	return strings.Join(lines, "\n")
}

func (m *Model) renderHelp(width, height int) string {
	help := []string{"Arbor keyboard shortcuts", "", "Navigation", "  j / k, ↓ / ↑      Move selection", "  h / l, Tab         Change resource view", "  g / G               First / last item", "  1 / 2 / 3           Requests / scenarios / environments", "", "Actions", "  Enter or r           Run or select", "  e                    Edit in $EDITOR", "  Ctrl-r               Reload workspace", "  Ctrl-d / Ctrl-u      Scroll response", "", "Find and command", "  /                    Filter resources", "  :                    Command mode", "  :use <environment>   Switch environment", "  :run <reference>     Run by reference", "  ?                    Close help", "  q                    Quit", "", "While a request is running, Esc or Ctrl-c cancels it."}
	return lipgloss.NewStyle().Width(width).Height(height).Padding(1, 3).Render(strings.Join(help, "\n"))
}

func (m *Model) renderFooter(width int) string {
	left := m.message
	if left == "" {
		left = "Ready"
	}
	if m.running {
		left = "● " + left
	}
	right := "[enter] run  [e] edit  [/] filter  [:] command  [?] help"
	if m.mode == filterMode {
		left = "/" + m.input
		right = "[enter] apply  [esc] cancel"
	}
	if m.mode == commandMode {
		left = ":" + m.input
		right = "[enter] execute  [esc] cancel"
	}
	space := max(1, width-lipgloss.Width(left)-lipgloss.Width(right)-2)
	return lipgloss.NewStyle().Foreground(muted).Width(width).Render(" " + truncate(left, max(1, width-lipgloss.Width(right)-3)) + strings.Repeat(" ", space) + right)
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
		for len(line) > width {
			lines = append(lines, line[:width])
			line = line[width:]
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
