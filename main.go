package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const logRoot = "/tmp/hawk-logs"

func main() {
	if len(os.Args) < 2 {
		runInteractive()
		return
	}

	// handle flag-style first argument: -w, -s, -sb, -k, etc.
	if arg := os.Args[1]; strings.HasPrefix(arg, "-") {
		switch arg {
		case "--watch":
			runTUI()
			return
		case "--scripts":
			runScriptPicker(false)
			return
		case "--kill":
			runKillPicker()
			return
		}
		if !strings.HasPrefix(arg, "--") {
			flags := arg[1:]
			has := func(c rune) bool { return strings.ContainsRune(flags, c) }
			if has('w') {
				runTUI()
				return
			}
			if has('k') {
				runKillPicker()
				return
			}
			if has('s') {
				runScriptPicker(has('b'))
				return
			}
		}
	}

	switch os.Args[1] {
	case "start":
		cmdStart(os.Args[2:])
	case "list":
		cmdList()
	case "output":
		cmdOutput(os.Args[2:])
	case "tail":
		cmdTail(os.Args[2:])
	case "stop":
		cmdStop(os.Args[2:])
	case "clean":
		cmdClean(os.Args[2:])
	case "help", "--help", "-h":
		cmdHelp()
	default:
		fmt.Fprintf(os.Stderr, "hawk: unknown command %q\n\n", os.Args[1])
		cmdHelp()
		os.Exit(1)
	}
}

func cmdStart(args []string) {
	// parse -b flag (only before --)
	bg := false
	var cleaned []string
	pastSep := false
	for _, a := range args {
		if a == "--" {
			pastSep = true
			cleaned = append(cleaned, a)
			continue
		}
		if !pastSep && (a == "-b" || a == "--background" || a == "--bg") {
			bg = true
			continue
		}
		cleaned = append(cleaned, a)
	}
	args = cleaned

	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: hawk start [-b] <name> -- <command>")
		os.Exit(1)
	}

	name := args[0]
	rest := args[1:]

	// skip -- separator
	if len(rest) > 0 && rest[0] == "--" {
		rest = rest[1:]
	}

	if len(rest) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: hawk start [-b] <name> -- <command>")
		os.Exit(1)
	}

	// check if a task with this name is already running
	if pid, ok := isRunning(name); ok {
		fmt.Fprintf(os.Stderr, "hawk: %q is already running (PID %d)\n", name, pid)
		fmt.Fprintf(os.Stderr, "hawk: use `hawk output %s` to check status\n", name)
		fmt.Fprintf(os.Stderr, "hawk: use `hawk stop %s` to stop it first\n", name)
		return
	}

	dir := logDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "hawk: %v\n", err)
		os.Exit(1)
	}

	ts := time.Now().Format("2006-01-02_15-04-05")
	logFile := filepath.Join(dir, ts+"-"+name+".log")
	pidFile := filepath.Join(dir, ts+"-"+name+".pid")

	f, err := os.Create(logFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hawk: %v\n", err)
		os.Exit(1)
	}

	command := strings.Join(rest, " ")
	cmd := exec.Command("sh", "-c", command)

	if bg {
		// background: output goes to log file only, detach process
		cmd.Stdout = f
		cmd.Stderr = f
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

		if err := cmd.Start(); err != nil {
			f.Close()
			fmt.Fprintf(os.Stderr, "hawk: failed to start: %v\n", err)
			os.Exit(1)
		}

		os.WriteFile(pidFile, []byte(strconv.Itoa(cmd.Process.Pid)), 0644)
		fmt.Fprintf(os.Stderr, "hawk: started %q in background (PID %d)\n", name, cmd.Process.Pid)
		fmt.Fprintf(os.Stderr, "hawk: log → %s\n", logFile)
		fmt.Fprintf(os.Stderr, "hawk: watch → hawk tail %s | hawk -w\n", name)
		cmd.Process.Release()
		return
	}

	// foreground: tee to both stdout/stderr and log file
	cmd.Stdin = os.Stdin
	multiOut := io.MultiWriter(os.Stdout, f)
	multiErr := io.MultiWriter(os.Stderr, f)
	cmd.Stdout = multiOut
	cmd.Stderr = multiErr

	if err := cmd.Start(); err != nil {
		f.Close()
		fmt.Fprintf(os.Stderr, "hawk: failed to start: %v\n", err)
		os.Exit(1)
	}

	// write PID file
	os.WriteFile(pidFile, []byte(strconv.Itoa(cmd.Process.Pid)), 0644)

	fmt.Fprintf(os.Stderr, "hawk: started %q (PID %d)\n", name, cmd.Process.Pid)
	fmt.Fprintf(os.Stderr, "hawk: log → %s\n", logFile)
	fmt.Fprintf(os.Stderr, "hawk: watch → hawk tail %s\n", name)

	// forward signals to child
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		cmd.Process.Signal(sig)
	}()

	err = cmd.Wait()
	f.Close()
	os.Remove(pidFile)

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			fmt.Fprintf(os.Stderr, "hawk: %q exited with code %d\n", name, exitErr.ExitCode())
			os.Exit(exitErr.ExitCode())
		}
		fmt.Fprintf(os.Stderr, "hawk: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "hawk: %q finished successfully\n", name)
}

func cmdList() {
	dir := logDir()
	entries := listLogs(dir)

	if len(entries) == 0 {
		fmt.Printf("hawk: no logs for %s\n", projectName())
		return
	}

	fmt.Printf("hawk: logs for %s\n\n", projectName())

	for _, e := range entries {
		status := "done"
		if e.running {
			status = "running"
		}
		fmt.Printf("  %-25s %-10s %-10s %s\n", e.name, status, e.age, formatBytes(e.size))
	}
}

func cmdOutput(args []string) {
	if len(args) == 0 {
		cmdList()
		return
	}

	name := args[0]
	lines := 100
	if len(args) > 1 {
		if n, err := strconv.Atoi(args[1]); err == nil {
			lines = n
		}
	}

	logFile := findLog(name)
	if logFile == "" {
		fmt.Fprintf(os.Stderr, "hawk: no log for %q\n", name)
		cmdList()
		os.Exit(1)
	}

	if isTerminal() {
		// interactive: pipe through less -R for color + scrolling
		lessCmd := exec.Command("less", "-R", "+G", logFile)
		lessCmd.Stdout = os.Stdout
		lessCmd.Stderr = os.Stderr
		lessCmd.Stdin = os.Stdin
		lessCmd.Run()
	} else {
		// piped (e.g. Claude reading output): plain tail
		fmt.Printf("=== hawk: %s (last %d lines) ===\n", name, lines)
		fmt.Printf("file: %s\n\n", logFile)
		tailFile(logFile, lines)
	}
}

func cmdTail(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: hawk tail <name>")
		os.Exit(1)
	}

	name := args[0]
	logFile := findLog(name)
	if logFile == "" {
		fmt.Fprintf(os.Stderr, "hawk: no log for %q\n", name)
		cmdList()
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "hawk: tailing %s (Ctrl+C to stop)\n", logFile)

	cmd := exec.Command("tail", "-f", logFile)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cmd.Process.Kill()
	}()

	cmd.Run()
}

func cmdStop(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: hawk stop <name>")
		os.Exit(1)
	}

	name := args[0]
	pidFile := findPidFile(name)
	if pidFile == "" {
		fmt.Fprintf(os.Stderr, "hawk: no running task %q\n", name)
		os.Exit(1)
	}

	data, err := os.ReadFile(pidFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hawk: %v\n", err)
		os.Exit(1)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		fmt.Fprintf(os.Stderr, "hawk: invalid PID in %s\n", pidFile)
		os.Exit(1)
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hawk: %v\n", err)
		os.Exit(1)
	}

	if err := proc.Signal(syscall.SIGTERM); err != nil {
		fmt.Fprintf(os.Stderr, "hawk: %s (PID %d) already finished\n", name, pid)
		os.Remove(pidFile)
		return
	}

	fmt.Printf("hawk: stopped %s (PID %d)\n", name, pid)
	os.Remove(pidFile)
}

func cmdClean(args []string) {
	maxAge := 24 * time.Hour
	if len(args) > 0 {
		if hours, err := strconv.Atoi(args[0]); err == nil {
			maxAge = time.Duration(hours) * time.Hour
		}
	}

	dir := logDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		fmt.Println("hawk: nothing to clean")
		return
	}

	count := 0
	now := time.Now()
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) > maxAge {
			os.Remove(filepath.Join(dir, e.Name()))
			count++
		}
	}

	fmt.Printf("hawk: cleaned %d files older than %s\n", count, maxAge)
}

func runInteractive() {
	dir := logDir()
	entries := listLogs(dir)

	if len(entries) == 0 {
		fmt.Printf("hawk: no logs for %s\n", projectName())
		return
	}

	// check if fzf is available
	fzfPath, err := exec.LookPath("fzf")
	if err != nil {
		cmdList()
		fmt.Println("\nInstall fzf for interactive selection, or use: hawk tail <name>")
		return
	}

	// build input for fzf
	var lines []string
	for _, e := range entries {
		status := "done"
		if e.running {
			status = "running"
		}
		lines = append(lines, fmt.Sprintf("%-25s %-10s %-10s %s", e.name, status, e.age, e.file))
	}

	input := strings.Join(lines, "\n")

	cmd := exec.Command(fzfPath,
		"--preview", "tail -30 {-1}",
		"--preview-window=right:60%",
		"--header", projectName()+" logs — enter to tail -f",
	)
	cmd.Stdin = strings.NewReader(input)
	cmd.Stderr = os.Stderr

	out, err := cmd.Output()
	if err != nil {
		return // user cancelled
	}

	// extract file path (last field)
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) == 0 {
		return
	}
	selected := fields[len(fields)-1]

	// tail -f the selected file
	tailCmd := exec.Command("tail", "-f", selected)
	tailCmd.Stdout = os.Stdout
	tailCmd.Stderr = os.Stderr

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		tailCmd.Process.Kill()
	}()

	tailCmd.Run()
}

func runKillPicker() {
	dir := logDir()
	entries := listLogs(dir)

	var running []logEntry
	for _, e := range entries {
		if e.running {
			running = append(running, e)
		}
	}

	if len(running) == 0 {
		fmt.Println("hawk: no running tasks")
		return
	}

	fzfPath, err := exec.LookPath("fzf")
	if err != nil {
		fmt.Println("hawk: running tasks")
		for _, e := range running {
			fmt.Printf("  %-25s %-10s %s\n", e.name, e.age, formatBytes(e.size))
		}
		fmt.Println("\nInstall fzf for interactive kill, or use: hawk stop <name>")
		return
	}

	var lines []string
	for _, e := range running {
		lines = append(lines, fmt.Sprintf("%-25s running  %-10s %s", e.name, e.age, e.file))
	}
	input := strings.Join(lines, "\n")

	cmd := exec.Command(fzfPath,
		"--preview", "tail -30 {-1}",
		"--preview-window=right:60%",
		"--header", projectName()+" — select task to stop",
	)
	cmd.Stdin = strings.NewReader(input)
	cmd.Stderr = os.Stderr

	out, err := cmd.Output()
	if err != nil {
		return
	}

	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) == 0 {
		return
	}
	cmdStop([]string{fields[0]})
}

// --- script discovery & picker ---

type scriptEntry struct {
	name   string
	cmd    string
	source string
}

func discoverScripts() []scriptEntry {
	pm := detectPackageManager()
	hasNx := fileExists("nx.json")
	pkgScripts := map[string]scriptEntry{}
	nxTargets := map[string]scriptEntry{}

	filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", ".git", "dist", ".next", ".cache", "build":
				return filepath.SkipDir
			}
			return nil
		}

		dir := filepath.Dir(path)

		switch d.Name() {
		case "package.json":
			data, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			var pkg struct {
				Name    string            `json:"name"`
				Scripts map[string]string `json:"scripts"`
			}
			if json.Unmarshal(data, &pkg) != nil {
				return nil
			}
			isRoot := dir == "."
			pkgName := pkg.Name
			if pkgName == "" {
				pkgName = filepath.Base(dir)
			}
			for script := range pkg.Scripts {
				var name, cmd string
				if isRoot {
					name = script
					cmd = pm + " run " + script
				} else {
					name = pkgName + ":" + script
					if hasNx {
						cmd = "npx nx run " + pkgName + ":" + script
					} else {
						cmd = buildWorkspaceCmd(pm, pkgName, dir, script)
					}
				}
				pkgScripts[name] = scriptEntry{name: name, cmd: cmd, source: path}
			}

		case "project.json":
			if !hasNx {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			var proj struct {
				Name    string                 `json:"name"`
				Targets map[string]any `json:"targets"`
			}
			if json.Unmarshal(data, &proj) != nil {
				return nil
			}
			projName := proj.Name
			if projName == "" {
				projName = filepath.Base(dir)
			}
			for target := range proj.Targets {
				name := projName + ":" + target
				cmd := "npx nx run " + projName + ":" + target
				nxTargets[name] = scriptEntry{name: name, cmd: cmd, source: path}
			}
		}

		return nil
	})

	// merge: nx targets override package.json scripts with same name
	merged := map[string]scriptEntry{}
	for k, v := range pkgScripts {
		merged[k] = v
	}
	for k, v := range nxTargets {
		merged[k] = v
	}

	entries := make([]scriptEntry, 0, len(merged))
	for _, v := range merged {
		entries = append(entries, v)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].name < entries[j].name
	})
	return entries
}

func runScriptPicker(bg bool) {
	scripts := discoverScripts()
	if len(scripts) == 0 {
		fmt.Println("hawk: no scripts found (looked for package.json and project.json)")
		return
	}

	fzfPath, err := exec.LookPath("fzf")
	if err != nil {
		fmt.Println("hawk: available scripts")
		for _, s := range scripts {
			fmt.Printf("  %-35s %s\n", s.name, s.cmd)
		}
		fmt.Println("\nInstall fzf for interactive selection")
		return
	}

	var lines []string
	for _, s := range scripts {
		lines = append(lines, fmt.Sprintf("%s\t%s\t%s", s.name, s.cmd, s.source))
	}
	input := strings.Join(lines, "\n")

	cmd := exec.Command(fzfPath,
		"--delimiter=\t",
		"--with-nth=1,3",
		"--nth=1",
		"--preview", "cat {3}",
		"--preview-window=right:50%",
		"--header", projectName()+" scripts — enter to run with hawk",
	)
	cmd.Stdin = strings.NewReader(input)
	cmd.Stderr = os.Stderr

	out, err := cmd.Output()
	if err != nil {
		return // user cancelled
	}

	parts := strings.Split(strings.TrimSpace(string(out)), "\t")
	if len(parts) < 2 {
		return
	}
	name := strings.TrimSpace(parts[0])
	command := strings.TrimSpace(parts[1])

	// sanitize name for hawk file naming
	hawkName := strings.NewReplacer(":", "-", "/", "-", "@", "").Replace(name)

	startArgs := []string{hawkName, "--", command}
	if bg {
		startArgs = []string{"-b", hawkName, "--", command}
	}
	cmdStart(startArgs)
}

func detectPackageManager() string {
	if fileExists("pnpm-lock.yaml") {
		return "pnpm"
	}
	if fileExists("yarn.lock") {
		return "yarn"
	}
	return "npm"
}

func buildWorkspaceCmd(pm, pkgName, dir, script string) string {
	switch pm {
	case "pnpm":
		return "pnpm --filter " + pkgName + " run " + script
	case "yarn":
		return "yarn workspace " + pkgName + " run " + script
	default:
		return "npm --prefix " + dir + " run " + script
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func cmdHelp() {
	fmt.Println(`hawk — task log manager

Usage:
  hawk                           Interactive log selector (fzf)
  hawk -w                        TUI: watch all running tasks
  hawk -s                        Run a script from package.json / nx (fzf)
  hawk -sb                       Run a script in the background
  hawk -k                        Interactive task killer (fzf)
  hawk start <name> -- <cmd>     Start command with logging
  hawk start -b <name> -- <cmd>  Start command in background
  hawk list                      List logs for current project
  hawk output <name> [lines]     Show last N lines (default 100)
  hawk tail <name>               Follow log in real time
  hawk stop <name>               Stop a running task
  hawk clean [hours]             Remove old logs (default 24h)
  hawk help                      Show this help

Log location: /tmp/hawk-logs/<project>/
Setup: go install github.com/houseinisprogramming/hawk-tui@latest`)
}

// --- helpers ---

func projectName() string {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		cwd, _ := os.Getwd()
		return filepath.Base(cwd)
	}
	return filepath.Base(strings.TrimSpace(string(out)))
}

func logDir() string {
	return filepath.Join(logRoot, projectName())
}

type logEntry struct {
	name    string
	file    string
	size    int64
	age     string
	running bool
}

func listLogs(dir string) []logEntry {
	matches, err := filepath.Glob(filepath.Join(dir, "*.log"))
	if err != nil || len(matches) == 0 {
		return nil
	}

	// sort newest first (lexicographic descending works because of timestamp format)
	sort.Sort(sort.Reverse(sort.StringSlice(matches)))

	var entries []logEntry
	now := time.Now()

	for _, f := range matches {
		base := strings.TrimSuffix(filepath.Base(f), ".log")

		// parse name: everything after the timestamp prefix (YYYY-MM-DD_HH-mm-ss-)
		name := base
		if len(base) > 20 && base[19] == '-' {
			name = base[20:]
		}

		// parse timestamp for age
		age := ""
		if len(base) >= 19 {
			if t, err := time.ParseInLocation("2006-01-02_15-04-05", base[:19], time.Local); err == nil {
				d := now.Sub(t)
				switch {
				case d < time.Minute:
					age = fmt.Sprintf("%ds ago", int(d.Seconds()))
				case d < time.Hour:
					age = fmt.Sprintf("%dm ago", int(d.Minutes()))
				case d < 24*time.Hour:
					age = fmt.Sprintf("%dh ago", int(d.Hours()))
				default:
					age = fmt.Sprintf("%dd ago", int(d.Hours()/24))
				}
			}
		}

		// check if running
		pidFile := strings.TrimSuffix(f, ".log") + ".pid"
		running := false
		if data, err := os.ReadFile(pidFile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
				proc, err := os.FindProcess(pid)
				if err == nil && proc.Signal(syscall.Signal(0)) == nil {
					running = true
				}
			}
		}

		info, _ := os.Stat(f)
		size := int64(0)
		if info != nil {
			size = info.Size()
		}

		entries = append(entries, logEntry{
			name:    name,
			file:    f,
			size:    size,
			age:     age,
			running: running,
		})
	}

	return entries
}

func findLog(name string) string {
	dir := logDir()
	matches, _ := filepath.Glob(filepath.Join(dir, "*-"+name+".log"))
	if len(matches) == 0 {
		return ""
	}
	// sort descending, return newest
	sort.Sort(sort.Reverse(sort.StringSlice(matches)))
	return matches[0]
}

func isRunning(name string) (int, bool) {
	pidFile := findPidFile(name)
	if pidFile == "" {
		return 0, false
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return 0, false
	}
	if proc.Signal(syscall.Signal(0)) != nil {
		return 0, false
	}
	return pid, true
}

func findPidFile(name string) string {
	dir := logDir()
	matches, _ := filepath.Glob(filepath.Join(dir, "*-"+name+".pid"))
	if len(matches) == 0 {
		return ""
	}
	sort.Sort(sort.Reverse(sort.StringSlice(matches)))
	return matches[0]
}

func isTerminal() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func tailFile(path string, lines int) {
	cmd := exec.Command("tail", "-n", strconv.Itoa(lines), path)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Run()
}

func formatBytes(b int64) string {
	switch {
	case b < 1024:
		return fmt.Sprintf("%dB", b)
	case b < 1024*1024:
		return fmt.Sprintf("%.1fKB", float64(b)/1024)
	default:
		return fmt.Sprintf("%.1fMB", float64(b)/(1024*1024))
	}
}
