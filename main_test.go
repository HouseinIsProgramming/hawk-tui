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

// --- script discovery tests ---

func TestDetectPackageManager(t *testing.T) {
	orig, _ := os.Getwd()
	defer os.Chdir(orig)

	dir := t.TempDir()
	os.Chdir(dir)

	// default: npm
	if pm := detectPackageManager(); pm != "npm" {
		t.Errorf("expected npm, got %s", pm)
	}

	// pnpm
	os.WriteFile(filepath.Join(dir, "pnpm-lock.yaml"), []byte(""), 0644)
	if pm := detectPackageManager(); pm != "pnpm" {
		t.Errorf("expected pnpm, got %s", pm)
	}
	os.Remove(filepath.Join(dir, "pnpm-lock.yaml"))

	// yarn
	os.WriteFile(filepath.Join(dir, "yarn.lock"), []byte(""), 0644)
	if pm := detectPackageManager(); pm != "yarn" {
		t.Errorf("expected yarn, got %s", pm)
	}
}

func TestDetectPackageManager_PnpmTakesPriority(t *testing.T) {
	orig, _ := os.Getwd()
	defer os.Chdir(orig)

	dir := t.TempDir()
	os.Chdir(dir)

	// both present: pnpm checked first
	os.WriteFile(filepath.Join(dir, "pnpm-lock.yaml"), []byte(""), 0644)
	os.WriteFile(filepath.Join(dir, "yarn.lock"), []byte(""), 0644)
	if pm := detectPackageManager(); pm != "pnpm" {
		t.Errorf("expected pnpm when both lock files exist, got %s", pm)
	}
}

func TestBuildWorkspaceCmd(t *testing.T) {
	tests := []struct {
		pm, pkg, dir, script, want string
	}{
		{"npm", "my-pkg", "packages/my-pkg", "build", "npm --prefix packages/my-pkg run build"},
		{"pnpm", "my-pkg", "packages/my-pkg", "test", "pnpm --filter my-pkg run test"},
		{"yarn", "my-pkg", "packages/my-pkg", "lint", "yarn workspace my-pkg run lint"},
	}
	for _, tt := range tests {
		got := buildWorkspaceCmd(tt.pm, tt.pkg, tt.dir, tt.script)
		if got != tt.want {
			t.Errorf("buildWorkspaceCmd(%s, %s, %s, %s) = %q, want %q",
				tt.pm, tt.pkg, tt.dir, tt.script, got, tt.want)
		}
	}
}

func TestDiscoverScripts_RootPackageJSON(t *testing.T) {
	orig, _ := os.Getwd()
	defer os.Chdir(orig)

	dir := t.TempDir()
	os.Chdir(dir)

	pkg := `{"name": "my-project", "scripts": {"build": "tsc", "test": "jest", "lint": "eslint ."}}`
	os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0644)

	scripts := discoverScripts()
	if len(scripts) != 3 {
		t.Fatalf("expected 3 scripts, got %d: %+v", len(scripts), scripts)
	}

	// sorted alphabetically
	expected := []string{"build", "lint", "test"}
	for i, want := range expected {
		if scripts[i].name != want {
			t.Errorf("scripts[%d].name = %s, want %s", i, scripts[i].name, want)
		}
	}

	// default package manager is npm
	for _, s := range scripts {
		if s.name == "build" && s.cmd != "npm run build" {
			t.Errorf("expected 'npm run build', got %q", s.cmd)
		}
	}
}

func TestDiscoverScripts_PnpmDetection(t *testing.T) {
	orig, _ := os.Getwd()
	defer os.Chdir(orig)

	dir := t.TempDir()
	os.Chdir(dir)

	os.WriteFile(filepath.Join(dir, "pnpm-lock.yaml"), []byte(""), 0644)
	pkg := `{"name": "my-project", "scripts": {"build": "tsc"}}`
	os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0644)

	scripts := discoverScripts()
	for _, s := range scripts {
		if s.name == "build" {
			if s.cmd != "pnpm run build" {
				t.Errorf("expected 'pnpm run build', got %q", s.cmd)
			}
			return
		}
	}
	t.Error("build script not found")
}

func TestDiscoverScripts_NxTargets(t *testing.T) {
	orig, _ := os.Getwd()
	defer os.Chdir(orig)

	dir := t.TempDir()
	os.Chdir(dir)

	os.WriteFile(filepath.Join(dir, "nx.json"), []byte("{}"), 0644)

	appDir := filepath.Join(dir, "apps", "my-app")
	os.MkdirAll(appDir, 0755)
	proj := `{"name": "my-app", "targets": {"build": {"executor": "@nx/webpack:webpack"}, "serve": {"executor": "@nx/webpack:dev-server"}}}`
	os.WriteFile(filepath.Join(appDir, "project.json"), []byte(proj), 0644)

	scripts := discoverScripts()
	found := map[string]bool{}
	for _, s := range scripts {
		found[s.name] = true
		if s.name == "my-app:build" && s.cmd != "npx nx run my-app:build" {
			t.Errorf("expected 'npx nx run my-app:build', got %q", s.cmd)
		}
	}
	if !found["my-app:build"] {
		t.Error("expected to find my-app:build")
	}
	if !found["my-app:serve"] {
		t.Error("expected to find my-app:serve")
	}
}

func TestDiscoverScripts_NxOverridesPackageJSON(t *testing.T) {
	orig, _ := os.Getwd()
	defer os.Chdir(orig)

	dir := t.TempDir()
	os.Chdir(dir)

	os.WriteFile(filepath.Join(dir, "nx.json"), []byte("{}"), 0644)

	appDir := filepath.Join(dir, "apps", "my-app")
	os.MkdirAll(appDir, 0755)

	// both define "my-app:build"
	pkg := `{"name": "my-app", "scripts": {"build": "tsc", "test": "jest"}}`
	os.WriteFile(filepath.Join(appDir, "package.json"), []byte(pkg), 0644)
	proj := `{"name": "my-app", "targets": {"build": {"executor": "@nx/webpack:webpack"}}}`
	os.WriteFile(filepath.Join(appDir, "project.json"), []byte(proj), 0644)

	scripts := discoverScripts()
	for _, s := range scripts {
		if s.name == "my-app:build" {
			// nx target should win
			if !strings.HasPrefix(s.cmd, "npx nx run") {
				t.Errorf("expected nx command for my-app:build, got %q", s.cmd)
			}
			if !strings.Contains(s.source, "project.json") {
				t.Errorf("expected project.json source, got %q", s.source)
			}
			return
		}
	}
	t.Error("my-app:build not found")
}

func TestDiscoverScripts_SkipsNodeModules(t *testing.T) {
	orig, _ := os.Getwd()
	defer os.Chdir(orig)

	dir := t.TempDir()
	os.Chdir(dir)

	nmDir := filepath.Join(dir, "node_modules", "some-pkg")
	os.MkdirAll(nmDir, 0755)
	pkg := `{"name": "some-pkg", "scripts": {"build": "tsc"}}`
	os.WriteFile(filepath.Join(nmDir, "package.json"), []byte(pkg), 0644)

	scripts := discoverScripts()
	for _, s := range scripts {
		if strings.Contains(s.name, "some-pkg") {
			t.Errorf("should not discover scripts from node_modules, found %q", s.name)
		}
	}
}

func TestDiscoverScripts_SkipsDist(t *testing.T) {
	orig, _ := os.Getwd()
	defer os.Chdir(orig)

	dir := t.TempDir()
	os.Chdir(dir)

	distDir := filepath.Join(dir, "dist", "pkg")
	os.MkdirAll(distDir, 0755)
	pkg := `{"name": "dist-pkg", "scripts": {"build": "tsc"}}`
	os.WriteFile(filepath.Join(distDir, "package.json"), []byte(pkg), 0644)

	scripts := discoverScripts()
	for _, s := range scripts {
		if strings.Contains(s.name, "dist-pkg") {
			t.Errorf("should not discover scripts from dist, found %q", s.name)
		}
	}
}

func TestDiscoverScripts_Empty(t *testing.T) {
	orig, _ := os.Getwd()
	defer os.Chdir(orig)

	dir := t.TempDir()
	os.Chdir(dir)

	scripts := discoverScripts()
	if len(scripts) != 0 {
		t.Errorf("expected 0 scripts in empty dir, got %d", len(scripts))
	}
}

func TestDiscoverScripts_EmptyScriptsField(t *testing.T) {
	orig, _ := os.Getwd()
	defer os.Chdir(orig)

	dir := t.TempDir()
	os.Chdir(dir)

	// package.json with no scripts field
	pkg := `{"name": "no-scripts"}`
	os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0644)

	scripts := discoverScripts()
	if len(scripts) != 0 {
		t.Errorf("expected 0 scripts when scripts field is empty, got %d", len(scripts))
	}
}

func TestDiscoverScripts_MalformedJSON(t *testing.T) {
	orig, _ := os.Getwd()
	defer os.Chdir(orig)

	dir := t.TempDir()
	os.Chdir(dir)

	os.WriteFile(filepath.Join(dir, "package.json"), []byte("{invalid json"), 0644)

	// should not panic, just return empty
	scripts := discoverScripts()
	if len(scripts) != 0 {
		t.Errorf("expected 0 scripts for malformed JSON, got %d", len(scripts))
	}
}

func TestDiscoverScripts_NestedWorkspace(t *testing.T) {
	orig, _ := os.Getwd()
	defer os.Chdir(orig)

	dir := t.TempDir()
	os.Chdir(dir)

	// root
	root := `{"name": "monorepo", "scripts": {"build:all": "nx run-many -t build"}}`
	os.WriteFile(filepath.Join(dir, "package.json"), []byte(root), 0644)

	// workspace package
	pkgDir := filepath.Join(dir, "packages", "utils")
	os.MkdirAll(pkgDir, 0755)
	pkg := `{"name": "@monorepo/utils", "scripts": {"build": "tsc", "test": "jest"}}`
	os.WriteFile(filepath.Join(pkgDir, "package.json"), []byte(pkg), 0644)

	scripts := discoverScripts()
	found := map[string]bool{}
	for _, s := range scripts {
		found[s.name] = true
	}

	if !found["build:all"] {
		t.Error("expected root script build:all")
	}
	if !found["@monorepo/utils:build"] {
		t.Error("expected workspace script @monorepo/utils:build")
	}
	if !found["@monorepo/utils:test"] {
		t.Error("expected workspace script @monorepo/utils:test")
	}
}

func TestDiscoverScripts_WorkspaceCmdWithoutNx(t *testing.T) {
	orig, _ := os.Getwd()
	defer os.Chdir(orig)

	dir := t.TempDir()
	os.Chdir(dir)

	// no nx.json — workspace commands should use package manager directly
	os.WriteFile(filepath.Join(dir, "pnpm-lock.yaml"), []byte(""), 0644)

	pkgDir := filepath.Join(dir, "packages", "lib")
	os.MkdirAll(pkgDir, 0755)
	pkg := `{"name": "my-lib", "scripts": {"build": "tsc"}}`
	os.WriteFile(filepath.Join(pkgDir, "package.json"), []byte(pkg), 0644)

	scripts := discoverScripts()
	for _, s := range scripts {
		if s.name == "my-lib:build" {
			if s.cmd != "pnpm --filter my-lib run build" {
				t.Errorf("expected pnpm filter command, got %q", s.cmd)
			}
			return
		}
	}
	t.Error("my-lib:build not found")
}

func TestDiscoverScripts_WorkspaceCmdYarn(t *testing.T) {
	orig, _ := os.Getwd()
	defer os.Chdir(orig)

	dir := t.TempDir()
	os.Chdir(dir)

	os.WriteFile(filepath.Join(dir, "yarn.lock"), []byte(""), 0644)

	pkgDir := filepath.Join(dir, "packages", "lib")
	os.MkdirAll(pkgDir, 0755)
	pkg := `{"name": "my-lib", "scripts": {"build": "tsc"}}`
	os.WriteFile(filepath.Join(pkgDir, "package.json"), []byte(pkg), 0644)

	scripts := discoverScripts()
	for _, s := range scripts {
		if s.name == "my-lib:build" {
			if s.cmd != "yarn workspace my-lib run build" {
				t.Errorf("expected yarn workspace command, got %q", s.cmd)
			}
			return
		}
	}
	t.Error("my-lib:build not found")
}

func TestDiscoverScripts_WorkspaceCmdNpm(t *testing.T) {
	orig, _ := os.Getwd()
	defer os.Chdir(orig)

	dir := t.TempDir()
	os.Chdir(dir)

	pkgDir := filepath.Join(dir, "packages", "lib")
	os.MkdirAll(pkgDir, 0755)
	pkg := `{"name": "my-lib", "scripts": {"build": "tsc"}}`
	os.WriteFile(filepath.Join(pkgDir, "package.json"), []byte(pkg), 0644)

	scripts := discoverScripts()
	for _, s := range scripts {
		if s.name == "my-lib:build" {
			if s.cmd != "npm --prefix packages/lib run build" {
				t.Errorf("expected npm prefix command, got %q", s.cmd)
			}
			return
		}
	}
	t.Error("my-lib:build not found")
}

func TestDiscoverScripts_NxProjectWithoutName(t *testing.T) {
	orig, _ := os.Getwd()
	defer os.Chdir(orig)

	dir := t.TempDir()
	os.Chdir(dir)

	os.WriteFile(filepath.Join(dir, "nx.json"), []byte("{}"), 0644)

	appDir := filepath.Join(dir, "apps", "unnamed-app")
	os.MkdirAll(appDir, 0755)

	// no "name" field — should fall back to directory name
	proj := `{"targets": {"build": {}, "test": {}}}`
	os.WriteFile(filepath.Join(appDir, "project.json"), []byte(proj), 0644)

	scripts := discoverScripts()
	found := map[string]bool{}
	for _, s := range scripts {
		found[s.name] = true
	}

	if !found["unnamed-app:build"] {
		t.Error("expected unnamed-app:build (fallback to dir name)")
	}
	if !found["unnamed-app:test"] {
		t.Error("expected unnamed-app:test")
	}
}

func TestDiscoverScripts_PackageJSONWithoutName(t *testing.T) {
	orig, _ := os.Getwd()
	defer os.Chdir(orig)

	dir := t.TempDir()
	os.Chdir(dir)

	// non-root package.json without "name" — should use dir name
	pkgDir := filepath.Join(dir, "packages", "my-lib")
	os.MkdirAll(pkgDir, 0755)
	pkg := `{"scripts": {"build": "tsc"}}`
	os.WriteFile(filepath.Join(pkgDir, "package.json"), []byte(pkg), 0644)

	scripts := discoverScripts()
	for _, s := range scripts {
		if s.name == "my-lib:build" {
			return // found it with dir name fallback
		}
	}
	t.Error("expected my-lib:build (fallback to dir name)")
}

func TestDiscoverScripts_NxWorkspacePackageJSONUsesNxRunner(t *testing.T) {
	orig, _ := os.Getwd()
	defer os.Chdir(orig)

	dir := t.TempDir()
	os.Chdir(dir)

	// nx.json present — workspace package.json scripts should use nx runner
	os.WriteFile(filepath.Join(dir, "nx.json"), []byte("{}"), 0644)

	pkgDir := filepath.Join(dir, "packages", "lib")
	os.MkdirAll(pkgDir, 0755)
	pkg := `{"name": "my-lib", "scripts": {"build": "tsc"}}`
	os.WriteFile(filepath.Join(pkgDir, "package.json"), []byte(pkg), 0644)

	scripts := discoverScripts()
	for _, s := range scripts {
		if s.name == "my-lib:build" {
			if s.cmd != "npx nx run my-lib:build" {
				t.Errorf("expected nx runner for workspace script in nx workspace, got %q", s.cmd)
			}
			return
		}
	}
	t.Error("my-lib:build not found")
}

func TestDiscoverScripts_SourceTracking(t *testing.T) {
	orig, _ := os.Getwd()
	defer os.Chdir(orig)

	dir := t.TempDir()
	os.Chdir(dir)

	os.WriteFile(filepath.Join(dir, "nx.json"), []byte("{}"), 0644)

	// root package.json
	pkg := `{"name": "root", "scripts": {"build": "tsc"}}`
	os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0644)

	// project.json
	appDir := filepath.Join(dir, "apps", "web")
	os.MkdirAll(appDir, 0755)
	proj := `{"name": "web", "targets": {"serve": {}}}`
	os.WriteFile(filepath.Join(appDir, "project.json"), []byte(proj), 0644)

	scripts := discoverScripts()
	for _, s := range scripts {
		if s.name == "build" && s.source != "package.json" {
			t.Errorf("root script source = %q, want package.json", s.source)
		}
		if s.name == "web:serve" && s.source != filepath.Join("apps", "web", "project.json") {
			t.Errorf("nx target source = %q, want apps/web/project.json", s.source)
		}
	}
}

func TestDiscoverScripts_MultipleNxProjects(t *testing.T) {
	orig, _ := os.Getwd()
	defer os.Chdir(orig)

	dir := t.TempDir()
	os.Chdir(dir)

	os.WriteFile(filepath.Join(dir, "nx.json"), []byte("{}"), 0644)

	// two nx projects
	for _, app := range []string{"frontend", "backend"} {
		appDir := filepath.Join(dir, "apps", app)
		os.MkdirAll(appDir, 0755)
		proj := `{"name": "` + app + `", "targets": {"build": {}, "test": {}, "lint": {}}}`
		os.WriteFile(filepath.Join(appDir, "project.json"), []byte(proj), 0644)
	}

	scripts := discoverScripts()
	found := map[string]bool{}
	for _, s := range scripts {
		found[s.name] = true
	}

	for _, want := range []string{
		"frontend:build", "frontend:test", "frontend:lint",
		"backend:build", "backend:test", "backend:lint",
	} {
		if !found[want] {
			t.Errorf("expected %s in discovered scripts", want)
		}
	}

	if len(scripts) != 6 {
		t.Errorf("expected 6 scripts, got %d", len(scripts))
	}
}

func TestDiscoverScripts_SortOrder(t *testing.T) {
	orig, _ := os.Getwd()
	defer os.Chdir(orig)

	dir := t.TempDir()
	os.Chdir(dir)

	pkg := `{"name": "proj", "scripts": {"zebra": "z", "alpha": "a", "middle": "m"}}`
	os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0644)

	scripts := discoverScripts()
	if len(scripts) != 3 {
		t.Fatalf("expected 3, got %d", len(scripts))
	}
	if scripts[0].name != "alpha" || scripts[1].name != "middle" || scripts[2].name != "zebra" {
		t.Errorf("scripts not sorted: %s, %s, %s", scripts[0].name, scripts[1].name, scripts[2].name)
	}
}

func TestFileExists(t *testing.T) {
	dir := t.TempDir()

	existing := filepath.Join(dir, "exists.txt")
	os.WriteFile(existing, []byte("hi"), 0644)

	if !fileExists(existing) {
		t.Error("expected fileExists to return true for existing file")
	}
	if fileExists(filepath.Join(dir, "nope.txt")) {
		t.Error("expected fileExists to return false for missing file")
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
