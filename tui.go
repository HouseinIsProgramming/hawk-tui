package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	name         string
	logFile      string
	pid          int
	lines        []string
	offset       int64 // track read position for incremental reads
	scrollOffset int   // lines from bottom (0 = bottom)
	follow       bool  // auto-scroll to bottom on new content
}

type model struct {
	tasks        []task
	activeTab    int
	mode         viewMode
	width        int
	height       int
	dir          string
	quitting     bool
	searching    bool
	searchBuf    string
	searchHits   []int // line indices matching search
	searchIdx    int   // current match index
	pendingG     bool  // waiting for second 'g' in 'gg'
	showHelp     bool
	copyingLines bool   // Y mode: waiting for line count input
	copyBuf      string // Y mode: digit buffer
	statusMsg    string // transient status shown in footer
}

type tickMsg time.Time
type clearStatusMsg struct{}

func tickCmd() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func clearStatusCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
		return clearStatusMsg{}
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
		if m.copyingLines {
			return m.updateCopyLines(msg)
		}
		if m.searching {
			return m.updateSearch(msg)
		}
		return m.updateNormal(msg)

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case clearStatusMsg:
		m.statusMsg = ""

	case tickMsg:
		m.refreshTasks()
		return m, tickCmd()
	}

	return m, nil
}

func (m model) updateNormal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// handle gg sequence
	if m.pendingG {
		m.pendingG = false
		if key == "g" {
			m.scrollToTop()
			return m, nil
		}
		// wasn't gg, fall through to normal handling
	}

	// dismiss help overlay
	if m.showHelp {
		if key == "?" || key == "escape" || key == "q" {
			m.showHelp = false
		}
		return m, nil
	}

	switch key {
	case "q", "ctrl+c":
		m.quitting = true
		return m, tea.Quit

	// help
	case "?":
		m.showHelp = true

	// tab switching
	case "tab":
		if len(m.tasks) > 0 {
			m.activeTab = (m.activeTab + 1) % len(m.tasks)
		}
	case "shift+tab":
		if len(m.tasks) > 0 {
			m.activeTab = (m.activeTab - 1 + len(m.tasks)) % len(m.tasks)
		}
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		idx, _ := strconv.Atoi(key)
		idx--
		if idx < len(m.tasks) {
			m.activeTab = idx
		}

	// view mode
	case "s":
		m.mode = splitView
	case "t":
		m.mode = tabView

	// scrolling
	case "j", "down":
		m.scroll(-1)
	case "k", "up":
		m.scroll(1)
	case "d", "ctrl+d":
		m.scroll(-m.contentHeight() / 2)
	case "u", "ctrl+u":
		m.scroll(m.contentHeight() / 2)
	case "f", "ctrl+f", "pgdown":
		m.scroll(-m.contentHeight())
	case "b", "ctrl+b", "pgup":
		m.scroll(m.contentHeight())
	case "G", "end", "cmd+down":
		m.scrollToBottom()
	case "g":
		m.pendingG = true
	case "home", "cmd+up":
		m.scrollToTop()

	// search
	case "/":
		m.searching = true
		m.searchBuf = ""
		m.searchHits = nil
		m.searchIdx = 0
	case "n":
		m.nextSearchHit()
	case "N":
		m.prevSearchHit()

	// copy
	case "y":
		lines := m.getVisibleLines()
		if len(lines) > 0 {
			if err := copyToClipboard(strings.Join(lines, "\n")); err != nil {
				m.statusMsg = "copy failed: " + err.Error()
			} else {
				m.statusMsg = fmt.Sprintf("copied %d lines", len(lines))
			}
			return m, clearStatusCmd()
		}
	case "Y":
		m.copyingLines = true
		m.copyBuf = ""
	}

	return m, nil
}

func (m model) updateSearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "enter":
		m.executeSearch()
		m.searching = false
	case "escape", "ctrl+c":
		m.searching = false
		m.searchBuf = ""
		m.searchHits = nil
	case "backspace":
		if len(m.searchBuf) > 0 {
			m.searchBuf = m.searchBuf[:len(m.searchBuf)-1]
		}
	default:
		if len(key) == 1 {
			m.searchBuf += key
		}
	}
	return m, nil
}

func (m model) updateCopyLines(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "enter":
		m.copyingLines = false
		if m.copyBuf == "" {
			return m, nil
		}
		n, err := strconv.Atoi(m.copyBuf)
		if err != nil || n <= 0 {
			m.statusMsg = "invalid number"
			m.copyBuf = ""
			return m, clearStatusCmd()
		}
		lines := m.getLastNLines(n)
		if len(lines) > 0 {
			if err := copyToClipboard(strings.Join(lines, "\n")); err != nil {
				m.statusMsg = "copy failed: " + err.Error()
			} else {
				m.statusMsg = fmt.Sprintf("copied %d lines", len(lines))
			}
		}
		m.copyBuf = ""
		return m, clearStatusCmd()
	case "escape", "ctrl+c":
		m.copyingLines = false
		m.copyBuf = ""
	case "backspace":
		if len(m.copyBuf) > 0 {
			m.copyBuf = m.copyBuf[:len(m.copyBuf)-1]
		}
	default:
		if len(key) == 1 && key[0] >= '0' && key[0] <= '9' {
			m.copyBuf += key
		}
	}
	return m, nil
}

func (m model) getVisibleLines() []string {
	if len(m.tasks) == 0 {
		return nil
	}
	t := m.tasks[m.activeTab]
	lines := t.lines
	height := m.contentHeight()

	end := len(lines) - t.scrollOffset
	if end < 0 {
		end = 0
	}
	start := end - height
	if start < 0 {
		start = 0
	}
	if end > len(lines) {
		end = len(lines)
	}
	return lines[start:end]
}

func (m model) getLastNLines(n int) []string {
	if len(m.tasks) == 0 {
		return nil
	}
	lines := m.tasks[m.activeTab].lines
	if n >= len(lines) {
		return lines
	}
	return lines[len(lines)-n:]
}

func copyToClipboard(text string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("pbcopy")
	case "linux":
		cmd = exec.Command("xclip", "-selection", "clipboard")
	default:
		return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}

func (m *model) scroll(delta int) {
	if len(m.tasks) == 0 {
		return
	}
	t := &m.tasks[m.activeTab]
	t.scrollOffset += delta
	maxOffset := max(0, len(t.lines)-m.contentHeight())
	t.scrollOffset = max(0, min(t.scrollOffset, maxOffset))
	t.follow = t.scrollOffset == 0
}

func (m *model) scrollToBottom() {
	if len(m.tasks) == 0 {
		return
	}
	t := &m.tasks[m.activeTab]
	t.scrollOffset = 0
	t.follow = true
}

func (m *model) scrollToTop() {
	if len(m.tasks) == 0 {
		return
	}
	t := &m.tasks[m.activeTab]
	t.scrollOffset = max(0, len(t.lines)-m.contentHeight())
	t.follow = false
}

func (m model) contentHeight() int {
	h := m.height - 3
	if h < 1 {
		return 1
	}
	return h
}

func (m *model) executeSearch() {
	if len(m.tasks) == 0 || m.searchBuf == "" {
		return
	}
	t := &m.tasks[m.activeTab]
	m.searchHits = nil
	query := strings.ToLower(m.searchBuf)
	for i, line := range t.lines {
		if strings.Contains(strings.ToLower(line), query) {
			m.searchHits = append(m.searchHits, i)
		}
	}
	if len(m.searchHits) > 0 {
		m.searchIdx = len(m.searchHits) - 1 // start at last match
		m.jumpToSearchHit()
	}
}

func (m *model) nextSearchHit() {
	if len(m.searchHits) == 0 {
		return
	}
	m.searchIdx = (m.searchIdx + 1) % len(m.searchHits)
	m.jumpToSearchHit()
}

func (m *model) prevSearchHit() {
	if len(m.searchHits) == 0 {
		return
	}
	m.searchIdx = (m.searchIdx - 1 + len(m.searchHits)) % len(m.searchHits)
	m.jumpToSearchHit()
}

func (m *model) jumpToSearchHit() {
	if len(m.tasks) == 0 || len(m.searchHits) == 0 {
		return
	}
	t := &m.tasks[m.activeTab]
	lineIdx := m.searchHits[m.searchIdx]
	// scroll so the hit is in the middle of the viewport
	fromBottom := len(t.lines) - lineIdx - m.contentHeight()/2
	t.scrollOffset = max(0, fromBottom)
	t.follow = false
}

func (m *model) refreshTasks() {
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
			t := task{name: name, logFile: logFile, pid: pid, follow: true}
			m.readNewLines(&t, logFile)
			m.tasks = append(m.tasks, t)
		}
	}

	var alive []task
	for _, t := range m.tasks {
		if seen[t.name] {
			alive = append(alive, t)
		}
	}
	m.tasks = alive

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
	n, _ := f.Read(buf)
	if n > 0 {
		newContent := string(buf[:n])
		newLines := strings.Split(newContent, "\n")

		if len(t.lines) > 0 && len(newLines) > 0 {
			t.lines[len(t.lines)-1] += newLines[0]
			t.lines = append(t.lines, newLines[1:]...)
		} else {
			t.lines = append(t.lines, newLines...)
		}

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

	if m.showHelp {
		return m.renderHelp()
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

	searchHighlightStyle = lipgloss.NewStyle().
				Background(lipgloss.Color("#f0883e")).
				Foreground(lipgloss.Color("#000"))

	searchBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#f0883e")).
			Bold(true)

	statusStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#7ee787")).
			Bold(true)
)

func (m model) renderHelp() string {
	title := activeTabStyle.Render(" hawk keybindings ")
	keyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#7ee787")).Bold(true).Width(16)
	descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#ccc"))

	bindings := []struct{ key, desc string }{
		{"", "── Navigation ──"},
		{"j / ↓", "Scroll down"},
		{"k / ↑", "Scroll up"},
		{"d / Ctrl+d", "Half page down"},
		{"u / Ctrl+u", "Half page up"},
		{"f / Ctrl+f", "Full page down"},
		{"b / Ctrl+b", "Full page up"},
		{"G / End", "Go to bottom (follow)"},
		{"gg / Home", "Go to top"},
		{"", "── Tabs ──"},
		{"Tab", "Next task"},
		{"Shift+Tab", "Previous task"},
		{"1-9", "Jump to task"},
		{"", "── Views ──"},
		{"t", "Tab view"},
		{"s", "Split view"},
		{"", "── Search ──"},
		{"/", "Start search"},
		{"n", "Next match"},
		{"N", "Previous match"},
		{"Enter", "Confirm search"},
		{"Escape", "Cancel search"},
		{"", "── Copy ──"},
		{"y", "Copy visible lines"},
		{"Y", "Copy last N lines"},
		{"", "── Other ──"},
		{"?", "Toggle this help"},
		{"q / Ctrl+c", "Quit"},
	}

	var lines []string
	for _, b := range bindings {
		if b.key == "" {
			lines = append(lines, headerStyle.Render(b.desc))
		} else {
			lines = append(lines, keyStyle.Render(b.key)+descStyle.Render(b.desc))
		}
	}

	content := title + "\n\n" + strings.Join(lines, "\n") + "\n\n" + helpStyle.Render("press ? or Esc to close")
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, content)
}

func (m model) renderEmpty() string {
	msg := headerStyle.Render("hawk: no running tasks in " + projectName())
	help := helpStyle.Render("waiting for tasks... (q to quit)")
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
		msg+"\n"+help)
}

func (m model) renderTab() string {
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
	header := tabBar + "  " + modeIndicator

	// footer
	footer := m.renderFooter()

	contentHeight := m.height - 3
	if contentHeight < 1 {
		contentHeight = 1
	}

	t := m.tasks[m.activeTab]
	content := m.renderLogLines(t.lines, m.width, contentHeight, t.scrollOffset)

	return header + "\n" + content + "\n" + footer
}

func (m model) renderSplit() string {
	n := len(m.tasks)
	if n == 0 {
		return m.renderEmpty()
	}

	footer := m.renderFooter()
	availableHeight := m.height - 2

	paneHeight := availableHeight / n
	if paneHeight < 3 {
		paneHeight = 3
	}

	var panes []string
	for i, t := range m.tasks {
		label := fmt.Sprintf(" %s (PID %d) ", t.name, t.pid)
		contentH := paneHeight - 2
		if contentH < 1 {
			contentH = 1
		}
		content := m.renderLogLines(t.lines, m.width-4, contentH, t.scrollOffset)

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

	return strings.Join(panes, "\n") + "\n" + footer
}

func (m model) renderFooter() string {
	if m.searching {
		return searchBarStyle.Render("/") + m.searchBuf + "█"
	}
	if m.copyingLines {
		return searchBarStyle.Render("copy last N lines: ") + m.copyBuf + "█"
	}

	var parts []string

	if m.mode == tabView {
		parts = append(parts, "tab:cycle  1-9:select  s:split")
	} else {
		parts = append(parts, "tab:cycle  1-9:select  t:tab")
	}
	parts = append(parts, "j/k:scroll  /:search  G:end  gg:top  ?:help  q:quit")

	if len(m.tasks) > 0 {
		t := m.tasks[m.activeTab]
		if !t.follow {
			parts = append(parts, headerStyle.Render(fmt.Sprintf("[scroll +%d]", t.scrollOffset)))
		}
	}

	if len(m.searchHits) > 0 {
		parts = append(parts, searchBarStyle.Render(
			fmt.Sprintf("[%d/%d: %q]", m.searchIdx+1, len(m.searchHits), m.searchBuf)))
	}

	if m.statusMsg != "" {
		parts = append(parts, statusStyle.Render(m.statusMsg))
	}

	return helpStyle.Render(strings.Join(parts, "  "))
}

func (m model) renderLogLines(lines []string, width, height, scrollOffset int) string {
	if len(lines) == 0 {
		return headerStyle.Render("(no output yet)")
	}

	// calculate visible window
	end := len(lines) - scrollOffset
	if end < 0 {
		end = 0
	}
	start := end - height
	if start < 0 {
		start = 0
	}
	if end > len(lines) {
		end = len(lines)
	}
	visible := lines[start:end]

	// build search hit set for highlighting
	hitSet := make(map[int]bool)
	for _, idx := range m.searchHits {
		hitSet[idx] = true
	}
	activeHitLine := -1
	if len(m.searchHits) > 0 {
		activeHitLine = m.searchHits[m.searchIdx]
	}

	var result []string
	for i, line := range visible {
		if len(line) > width && width > 0 {
			line = line[:width]
		}
		absIdx := start + i
		if hitSet[absIdx] {
			if absIdx == activeHitLine {
				line = highlightSearch(line, m.searchBuf, true)
			} else {
				line = highlightSearch(line, m.searchBuf, false)
			}
		}
		result = append(result, line)
	}

	return strings.Join(result, "\n")
}

func highlightSearch(line, query string, active bool) string {
	if query == "" {
		return line
	}
	lower := strings.ToLower(line)
	queryLower := strings.ToLower(query)
	idx := strings.Index(lower, queryLower)
	if idx < 0 {
		return line
	}

	before := line[:idx]
	match := line[idx : idx+len(query)]
	after := line[idx+len(query):]

	style := searchHighlightStyle
	if !active {
		style = lipgloss.NewStyle().Background(lipgloss.Color("#444")).Foreground(lipgloss.Color("#fff"))
	}

	return before + style.Render(match) + after
}

func runTUI() {
	p := tea.NewProgram(newModel(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "hawk: TUI error: %v\n", err)
		os.Exit(1)
	}
}
