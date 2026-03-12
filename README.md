# hawk

### **H**elps **A**gents **W**atch **K**ommands and Hawk-tui spits that shit (logs) out, you know what I am sayin' ?

A lightweight task log manager designed for both humans and AI agents (Claude Code). Run long-running commands with automatic log capture — both you and your AI can see what's happening.

```
┌─ hawk list ──────────────────────────────────────┐
│ ● test-all          running   2m 13s        4.2KB│
│ ✓ codegen-myapp      done      5m ago        1.1KB│
│ ✗ lint-all          failed    12m ago       8.3KB│
└──────────────────────────────────────────────────┘
```

## Why?

Claude Code's `run_in_background` is great — your agent runs a build, you keep working, it gets notified when it's done. But:

- **Logs vanish** into Claude's conversation context, cluttering your chat history
- **You can't watch** a running build from your own terminal
- **Logs don't persist** — once the conversation scrolls or compresses, they're gone
- **tmux-based workarounds** tie you to a specific terminal setup and not everyone uses tmux

hawk fixes all of this. It wraps `run_in_background` with a simple CLI that both Claude and you can use:

- **Agent** runs `hawk start test -- pnpm test-all` — gets auto-notified on completion, checks results with `hawk output test`
- **Human** runs `hawk tail test` from any terminal to watch live, or `hawk` for an interactive fzf picker
- **Logs persist** in `/tmp/hawk-logs/<project>/` as readable, timestamped files — not buried in chat
- **No tmux**, no special terminal setup, just a Go binary on your PATH

One CLI. Two audiences. Zero dependencies beyond Go.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/houseinisprogramming/hawk-tui/main/install.sh | bash
```

This downloads a pre-built binary for your platform and installs:
1. The `hawk` binary to `/usr/local/bin/`
2. The Claude Code skill to `~/.claude/skills/hawk/`

Pre-built binaries are available for macOS (arm64, amd64) and Linux (arm64, amd64). Falls back to building from source if no binary is available.

### From source

```bash
git clone https://github.com/houseinisprogramming/hawk-tui.git
cd hawk-tui
go build -o hawk .
sudo cp hawk /usr/local/bin/

# Install Claude Code skill
mkdir -p ~/.claude/skills/hawk
cp skill/SKILL.md ~/.claude/skills/hawk/SKILL.md
```

## Usage

### Start a task

```bash
hawk start test-all -- pnpm test-all
hawk start build -- npm run build
hawk start lint -- eslint src/
```

Output is tee'd to both stdout and a log file at `/tmp/hawk-logs/<project>/`.

### Watch live output

```bash
hawk tail test-all
```

### View output (with less + colors)

```bash
hawk output test-all        # opens in less -R (interactive)
hawk output test-all 50     # last 50 lines (when piped)
```

### List tasks

```bash
hawk list
```

```
hawk: logs for my-project

  test-all                  running    2m ago     4.2KB
  build                     done       15m ago    1.1KB
  lint                      done       1h ago     890B
```

### Interactive picker (fzf)

```bash
hawk
```

Opens fzf with a preview pane showing the last 30 lines of each log. Select one to `tail -f` it.

Requires [fzf](https://github.com/junegunn/fzf). Falls back to `hawk list` if not installed.

### Stop a task

```bash
hawk stop test-all
```

### Clean old logs

```bash
hawk clean        # remove logs older than 24h
hawk clean 48     # remove logs older than 48h
```

## How it works

```
hawk start test -- pnpm test
         │
         ├── spawns: sh -c "pnpm test"
         ├── tees output to: /tmp/hawk-logs/<project>/<timestamp>-test.log
         ├── writes PID to:  /tmp/hawk-logs/<project>/<timestamp>-test.pid
         └── streams to stdout
```

- **Project detection**: Uses `git rev-parse --show-toplevel` to group logs by repo
- **Log naming**: `YYYY-MM-DD_HH-mm-ss-<name>.log` — human readable and lexicographically sortable
- **TTY detection**: `hawk output` uses `less -R` in terminals, plain `tail` when piped

## Claude Code integration

The included skill teaches Claude Code to use hawk automatically. After installing:

1. Claude uses `hawk start <name> -- <cmd>` with `run_in_background: true`
2. Claude gets auto-notified when the task finishes
3. Claude checks results with `hawk output <name>`
4. You can watch live with `hawk tail <name>` from your terminal

For best results, add this to your `CLAUDE.md`:

```markdown
- ALWAYS use `hawk start <name> -- <command>` with `run_in_background: true`
  when running any shell command that may take more than a few seconds
  (tests, builds, lints, codegen). Never run these directly with Bash —
  always go through hawk. After completion, use `hawk output <name>` to
  check results.
```

## Log location

```
/tmp/hawk-logs/
└── <project-name>/
    ├── 2026-03-11_14-30-05-test-all.log
    ├── 2026-03-11_14-25-00-build.log
    └── 2026-03-11_14-20-00-lint.log
```

## License

MIT
