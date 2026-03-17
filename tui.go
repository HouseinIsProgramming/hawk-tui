package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type viewMode int

const (
	tabView viewMode = iota
	splitView
)

type task struct {
	name    string
	logFile string
	pid     int
	lines   []string
	offset  int64 // track read position for incremental reads
}

type model struct {
	tasks     []task
	activeTab int
	mode      viewMode
	width     int
	height    int
	dir       string
	quitting  bool
}

type tickMsg time.Time

func tickCmd() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func newModel() model {
	return model{
		dir:  logDir(),
		mode: tabView,
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(tickCmd(), tea.WindowSize())
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "tab":
			if len(m.tasks) > 0 {
				m.activeTab = (m.activeTab + 1) % len(m.tasks)
			}
		case "shift+tab":
			if len(m.tasks) > 0 {
				m.activeTab = (m.activeTab - 1 + len(m.tasks)) % len(m.tasks)
			}
		case "s":
			m.mode = splitView
		case "t":
			m.mode = tabView
		case "1", "2", "3", "4", "5", "6", "7", "8", "9":
			idx, _ := strconv.Atoi(msg.String())
			idx-- // 1-indexed to 0-indexed
			if idx < len(m.tasks) {
				m.activeTab = idx
			}
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tickMsg:
		m.refreshTasks()
		return m, tickCmd()
	}

	return m, nil
}

func (m *model) refreshTasks() {
	// find all running tasks
	matches, _ := filepath.Glob(filepath.Join(m.dir, "*.pid"))
	seen := make(map[string]bool)

	for _, pidFile := range matches {
		data, err := os.ReadFile(pidFile)
		if err != nil {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil {
			continue
		}
		proc, err := os.FindProcess(pid)
		if err != nil || proc.Signal(syscall.Signal(0)) != nil {
			continue
		}

		base := strings.TrimSuffix(filepath.Base(pidFile), ".pid")
		name := base
		if len(base) > 20 && base[19] == '-' {
			name = base[20:]
		}
		seen[name] = true

		logFile := strings.TrimSuffix(pidFile, ".pid") + ".log"

		// find or create task
		found := false
		for i := range m.tasks {
			if m.tasks[i].name == name {
				m.tasks[i].pid = pid
				m.readNewLines(&m.tasks[i], logFile)
				found = true
				break
			}
		}
		if !found {
			t := task{name: name, logFile: logFile, pid: pid}
			m.readNewLines(&t, logFile)
			m.tasks = append(m.tasks, t)
		}
	}

	// remove tasks that are no longer running
	var alive []task
	for _, t := range m.tasks {
		if seen[t.name] {
			alive = append(alive, t)
		}
	}
	m.tasks = alive

	// fix activeTab if out of bounds
	if m.activeTab >= len(m.tasks) {
		m.activeTab = max(0, len(m.tasks)-1)
	}
}

func (m *model) readNewLines(t *task, logFile string) {
	f, err := os.Open(logFile)
	if err != nil {
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return
	}

	if info.Size() <= t.offset {
		return
	}

	f.Seek(t.offset, 0)
	buf := make([]byte, info.Size()-t.offset)
	n, err := f.Read(buf)
	if n > 0 {
		newContent := string(buf[:n])
		newLines := strings.Split(newContent, "\n")

		// merge with existing: if last existing line was partial, append to it
		if len(t.lines) > 0 && len(newLines) > 0 {
			t.lines[len(t.lines)-1] += newLines[0]
			t.lines = append(t.lines, newLines[1:]...)
		} else {
			t.lines = append(t.lines, newLines...)
		}

		// keep last 1000 lines max
		if len(t.lines) > 1000 {
			t.lines = t.lines[len(t.lines)-1000:]
		}

		t.offset = info.Size()
	}
}

func (m model) View() string {
	if m.quitting {
		return ""
	}

	if m.width == 0 || m.height == 0 {
		return "loading..."
	}

	if len(m.tasks) == 0 {
		return m.renderEmpty()
	}

	switch m.mode {
	case splitView:
		return m.renderSplit()
	default:
		return m.renderTab()
	}
}

var (
	activeTabStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#000")).
			Background(lipgloss.Color("#7ee787"))

	inactiveTabStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#888"))

	headerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#666"))

	borderStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#444"))

	activeBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("#7ee787"))

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#666"))
)

func (m model) renderEmpty() string {
	msg := headerStyle.Render("hawk: no running tasks in " + projectName())
	help := helpStyle.Render("waiting for tasks... (q to quit)")
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
		msg+"\n"+help)
}

func (m model) renderTab() string {
	// tab bar
	var tabs []string
	for i, t := range m.tasks {
		label := fmt.Sprintf(" %d:%s (PID %d) ", i+1, t.name, t.pid)
		if i == m.activeTab {
			tabs = append(tabs, activeTabStyle.Render(label))
		} else {
			tabs = append(tabs, inactiveTabStyle.Render(label))
		}
	}
	tabBar := strings.Join(tabs, " ")

	modeIndicator := headerStyle.Render("[tab view]")
	help := helpStyle.Render("tab:cycle  1-9:select  s:split  q:quit")
	header := tabBar + "  " + modeIndicator
	footer := help

	// content area
	contentHeight := m.height - 3 // header + footer + padding
	if contentHeight < 1 {
		contentHeight = 1
	}

	t := m.tasks[m.activeTab]
	content := m.renderLogLines(t.lines, m.width, contentHeight)

	return header + "\n" + content + "\n" + footer
}

func (m model) renderSplit() string {
	n := len(m.tasks)
	if n == 0 {
		return m.renderEmpty()
	}

	help := helpStyle.Render("[split view]  tab:cycle  1-9:select  t:tab view  q:quit")
	availableHeight := m.height - 2 // footer + padding

	// divide height among tasks
	paneHeight := availableHeight / n
	if paneHeight < 3 {
		paneHeight = 3
	}

	var panes []string
	for i, t := range m.tasks {
		label := fmt.Sprintf(" %s (PID %d) ", t.name, t.pid)
		contentH := paneHeight - 2 // border takes 2 lines
		if contentH < 1 {
			contentH = 1
		}
		content := m.renderLogLines(t.lines, m.width-4, contentH)

		var pane string
		if i == m.activeTab {
			pane = activeBorderStyle.
				Width(m.width - 2).
				Height(contentH).
				Render(lipgloss.NewStyle().Bold(true).Render(label) + "\n" + content)
		} else {
			pane = borderStyle.
				Width(m.width - 2).
				Height(contentH).
				Render(lipgloss.NewStyle().Foreground(lipgloss.Color("#888")).Render(label) + "\n" + content)
		}
		panes = append(panes, pane)
	}

	return strings.Join(panes, "\n") + "\n" + help
}

func (m model) renderLogLines(lines []string, width, height int) string {
	if len(lines) == 0 {
		return headerStyle.Render("(no output yet)")
	}

	// take last `height` lines
	start := len(lines) - height
	if start < 0 {
		start = 0
	}
	visible := lines[start:]

	// truncate lines to width
	var result []string
	for _, line := range visible {
		if len(line) > width && width > 0 {
			line = line[:width]
		}
		result = append(result, line)
	}

	return strings.Join(result, "\n")
}

func runTUI() {
	p := tea.NewProgram(newModel(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "hawk: TUI error: %v\n", err)
		os.Exit(1)
	}
}
