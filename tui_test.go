package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRefreshTasks_FindsRunning(t *testing.T) {
	dir := logDir()
	os.MkdirAll(dir, 0755)

	// start a real process
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()

	ts := time.Now().Format("2006-01-02_15-04-05")
	name := "tui-refresh-" + strconv.FormatInt(time.Now().UnixNano(), 36)

	pidFile := filepath.Join(dir, ts+"-"+name+".pid")
	logFile := filepath.Join(dir, ts+"-"+name+".log")
	os.WriteFile(pidFile, []byte(strconv.Itoa(cmd.Process.Pid)), 0644)
	os.WriteFile(logFile, []byte("line1\nline2\nline3\n"), 0644)
	defer os.Remove(pidFile)
	defer os.Remove(logFile)

	m := model{dir: dir}
	m.refreshTasks()

	found := false
	for _, task := range m.tasks {
		if task.name == name {
			found = true
			if task.pid != cmd.Process.Pid {
				t.Errorf("expected PID %d, got %d", cmd.Process.Pid, task.pid)
			}
			if len(task.lines) == 0 {
				t.Error("expected log lines to be read")
			}
			break
		}
	}
	if !found {
		t.Errorf("expected to find task %q in refresh", name)
	}
}

func TestRefreshTasks_RemovesDead(t *testing.T) {
	dir := logDir()
	os.MkdirAll(dir, 0755)

	// create a dead process
	cmd := exec.Command("true")
	cmd.Start()
	cmd.Wait()

	ts := time.Now().Format("2006-01-02_15-04-05")
	name := "tui-dead-" + strconv.FormatInt(time.Now().UnixNano(), 36)

	pidFile := filepath.Join(dir, ts+"-"+name+".pid")
	logFile := filepath.Join(dir, ts+"-"+name+".log")
	os.WriteFile(pidFile, []byte(strconv.Itoa(cmd.Process.Pid)), 0644)
	os.WriteFile(logFile, []byte("output\n"), 0644)
	defer os.Remove(pidFile)
	defer os.Remove(logFile)

	m := model{dir: dir}
	m.refreshTasks()

	for _, task := range m.tasks {
		if task.name == name {
			t.Errorf("dead task %q should not appear", name)
		}
	}
}

func TestRefreshTasks_IncrementalRead(t *testing.T) {
	dir := logDir()
	os.MkdirAll(dir, 0755)

	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()

	ts := time.Now().Format("2006-01-02_15-04-05")
	name := "tui-incr-" + strconv.FormatInt(time.Now().UnixNano(), 36)

	pidFile := filepath.Join(dir, ts+"-"+name+".pid")
	logFile := filepath.Join(dir, ts+"-"+name+".log")
	os.WriteFile(pidFile, []byte(strconv.Itoa(cmd.Process.Pid)), 0644)
	os.WriteFile(logFile, []byte("line1\n"), 0644)
	defer os.Remove(pidFile)
	defer os.Remove(logFile)

	m := model{dir: dir}
	m.refreshTasks()

	// append more content
	f, _ := os.OpenFile(logFile, os.O_APPEND|os.O_WRONLY, 0644)
	f.WriteString("line2\nline3\n")
	f.Close()

	m.refreshTasks()

	var found *task
	for i := range m.tasks {
		if m.tasks[i].name == name {
			found = &m.tasks[i]
			break
		}
	}
	if found == nil {
		t.Fatal("task not found")
	}

	content := strings.Join(found.lines, "\n")
	if !strings.Contains(content, "line1") || !strings.Contains(content, "line3") {
		t.Errorf("expected incremental read to contain all lines, got: %q", content)
	}
}

func TestRenderLogLines(t *testing.T) {
	m := model{width: 80, height: 24}

	// empty
	out := m.renderLogLines(nil, 80, 10, 0)
	if !strings.Contains(out, "no output") {
		t.Error("expected 'no output' for empty lines")
	}

	// more lines than height, scrollOffset=0 (at bottom)
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = "line " + strconv.Itoa(i)
	}
	out = m.renderLogLines(lines, 80, 5, 0)
	rendered := strings.Split(out, "\n")
	if len(rendered) != 5 {
		t.Errorf("expected 5 visible lines, got %d", len(rendered))
	}
	// should show last 5 (lines 15-19)
	if !strings.Contains(rendered[0], "line 15") {
		t.Errorf("expected to start at line 15, got: %s", rendered[0])
	}

	// with scrollOffset=10, should show lines 5-9
	out = m.renderLogLines(lines, 80, 5, 10)
	rendered = strings.Split(out, "\n")
	if !strings.Contains(rendered[0], "line 5") {
		t.Errorf("expected to start at line 5 with offset 10, got: %s", rendered[0])
	}

	// truncate wide lines
	wideLine := strings.Repeat("x", 100)
	out = m.renderLogLines([]string{wideLine}, 40, 5, 0)
	if len(out) != 40 {
		t.Errorf("expected truncated to 40 chars, got %d", len(out))
	}
}

func TestViewModeToggle(t *testing.T) {
	m := newModel()

	if m.mode != tabView {
		t.Error("expected initial mode to be tabView")
	}

	// simulate 's' key
	m.mode = splitView
	if m.mode != splitView {
		t.Error("expected splitView after toggle")
	}

	m.mode = tabView
	if m.mode != tabView {
		t.Error("expected tabView after toggle back")
	}
}

func TestActiveTabWraps(t *testing.T) {
	m := model{
		tasks: []task{
			{name: "a"}, {name: "b"}, {name: "c"},
		},
		activeTab: 2,
	}

	// wrap forward
	m.activeTab = (m.activeTab + 1) % len(m.tasks)
	if m.activeTab != 0 {
		t.Errorf("expected wrap to 0, got %d", m.activeTab)
	}

	// wrap backward
	m.activeTab = (m.activeTab - 1 + len(m.tasks)) % len(m.tasks)
	if m.activeTab != 2 {
		t.Errorf("expected wrap to 2, got %d", m.activeTab)
	}
}
