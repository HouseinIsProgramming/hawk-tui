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

func TestIsRunning_NoProcess(t *testing.T) {
	// isRunning should return false when no PID file exists for the name
	// Use a name that definitely doesn't exist
	_, ok := isRunning("nonexistent-task-" + strconv.FormatInt(time.Now().UnixNano(), 36))
	if ok {
		t.Error("expected isRunning to return false for nonexistent task")
	}
}

func TestIsRunning_WithLiveProcess(t *testing.T) {
	dir := logDir()
	os.MkdirAll(dir, 0755)

	// start a real process
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()

	// write a PID file matching hawk's naming convention
	ts := time.Now().Format("2006-01-02_15-04-05")
	name := "test-live-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	pidFile := filepath.Join(dir, ts+"-"+name+".pid")
	os.WriteFile(pidFile, []byte(strconv.Itoa(cmd.Process.Pid)), 0644)
	defer os.Remove(pidFile)

	pid, ok := isRunning(name)
	if !ok {
		t.Error("expected isRunning to return true for live process")
	}
	if pid != cmd.Process.Pid {
		t.Errorf("expected PID %d, got %d", cmd.Process.Pid, pid)
	}
}

func TestIsRunning_WithDeadProcess(t *testing.T) {
	dir := logDir()
	os.MkdirAll(dir, 0755)

	// start and immediately kill a process to get a dead PID
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	cmd.Wait()
	deadPid := cmd.Process.Pid

	ts := time.Now().Format("2006-01-02_15-04-05")
	name := "test-dead-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	pidFile := filepath.Join(dir, ts+"-"+name+".pid")
	os.WriteFile(pidFile, []byte(strconv.Itoa(deadPid)), 0644)
	defer os.Remove(pidFile)

	_, ok := isRunning(name)
	if ok {
		t.Error("expected isRunning to return false for dead process")
	}
}

func TestCmdStart_Idempotent(t *testing.T) {
	// build the binary
	binary := filepath.Join(t.TempDir(), "hawk-test")
	build := exec.Command("go", "build", "-o", binary, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %s\n%s", err, out)
	}

	// start a long-running task
	cmd1 := exec.Command(binary, "start", "idempotent-test", "--", "sleep", "30")
	if err := cmd1.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cmd1.Process.Kill()
		cmd1.Wait()
	}()

	// give it time to write PID file
	time.Sleep(500 * time.Millisecond)

	// attempt duplicate start — should exit 0 with "already running" message
	cmd2 := exec.Command(binary, "start", "idempotent-test", "--", "sleep", "30")
	out, err := cmd2.CombinedOutput()
	if err != nil {
		t.Errorf("duplicate start should exit 0, got: %v", err)
	}

	output := string(out)
	if !strings.Contains(output, "already running") {
		t.Errorf("expected 'already running' message, got: %s", output)
	}

	// cleanup
	stop := exec.Command(binary, "stop", "idempotent-test")
	stop.Run()
}

func TestFindPidFile(t *testing.T) {
	dir := logDir()
	os.MkdirAll(dir, 0755)

	name := "test-find-" + strconv.FormatInt(time.Now().UnixNano(), 36)

	// no file should return empty
	if f := findPidFile(name); f != "" {
		t.Errorf("expected empty, got %s", f)
	}

	// create a pid file
	ts := time.Now().Format("2006-01-02_15-04-05")
	pidFile := filepath.Join(dir, ts+"-"+name+".pid")
	os.WriteFile(pidFile, []byte("12345"), 0644)
	defer os.Remove(pidFile)

	found := findPidFile(name)
	if found != pidFile {
		t.Errorf("expected %s, got %s", pidFile, found)
	}
}

func TestFindLog(t *testing.T) {
	dir := logDir()
	os.MkdirAll(dir, 0755)

	name := "test-log-" + strconv.FormatInt(time.Now().UnixNano(), 36)

	// no file should return empty
	if f := findLog(name); f != "" {
		t.Errorf("expected empty, got %s", f)
	}

	// create a log file
	ts := time.Now().Format("2006-01-02_15-04-05")
	logFile := filepath.Join(dir, ts+"-"+name+".log")
	os.WriteFile(logFile, []byte("test output"), 0644)
	defer os.Remove(logFile)

	found := findLog(name)
	if found != logFile {
		t.Errorf("expected %s, got %s", logFile, found)
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		input    int64
		expected string
	}{
		{0, "0B"},
		{512, "512B"},
		{1024, "1.0KB"},
		{1536, "1.5KB"},
		{1048576, "1.0MB"},
		{1572864, "1.5MB"},
	}
	for _, tt := range tests {
		got := formatBytes(tt.input)
		if got != tt.expected {
			t.Errorf("formatBytes(%d) = %s, want %s", tt.input, got, tt.expected)
		}
	}
}
