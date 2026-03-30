# hawk

### **H**elps **A**gents **W**atch **K**ommands and Hawk-tui spits that shit (logs) out, you know what I am sayin' ?

A task log manager for humans and AI agents. Run long-running commands with automatic log capture — both you and your AI can see what's happening.

```
┌─ hawk list ──────────────────────────────────────┐
│ ● test-all          running   2m 13s        4.2KB│
│ ✓ codegen-myapp      done      5m ago        1.1KB│
│ ✗ lint-all          failed    12m ago       8.3KB│
└──────────────────────────────────────────────────┘
```

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/houseinisprogramming/hawk-tui/main/install.sh | bash
```

Installs the `hawk` binary to `/usr/local/bin/` and the Claude Code skill to `~/.claude/skills/hawk/`. Pre-built binaries for macOS and Linux (arm64/amd64).

<details>
<summary>From source</summary>

```bash
git clone https://github.com/houseinisprogramming/hawk-tui.git
cd hawk-tui
go build -o hawk .
sudo cp hawk /usr/local/bin/

# Claude Code skill
mkdir -p ~/.claude/skills/hawk
cp skill/SKILL.md ~/.claude/skills/hawk/SKILL.md
```
</details>

## Commands

```bash
hawk start <name> -- <cmd>  # start command with logging (idempotent)
hawk list                   # list logs for current project
hawk output <name> [lines]  # view output (less -R or last N lines)
hawk tail <name>            # follow log in real time
hawk stop <name>            # stop a running task
hawk clean [hours]          # remove logs older than N hours (default 24)
hawk                        # interactive fzf picker
hawk -s                     # discover & run scripts from package.json / nx
hawk -w                     # TUI: watch all running tasks
```

## TUI watch mode (`hawk -w`)

Full terminal UI — tab/split views, vim-style scrolling, search, clipboard copy.

| Key | Action |
|---|---|
| `j`/`k` | Scroll down/up |
| `d`/`u`, `f`/`b` | Half / full page |
| `G` / `gg` | Bottom / top |
| `Tab`, `1`–`9` | Switch tasks |
| `t` / `s` | Tab / split view |
| `/`, `n`/`N` | Search, next/prev match |
| `y` | Copy visible lines |
| `Y` | Copy last N lines |
| `?` | Help |

## Claude Code integration

The included skill teaches Claude to use hawk automatically. Add this to your `CLAUDE.md`:

```markdown
- ALWAYS use `hawk start <name> -- <command>` with `run_in_background: true`
  when running any shell command that may take more than a few seconds
  (tests, builds, lints, codegen). Never run these directly with Bash —
  always go through hawk. After completion, use `hawk output <name>` to
  check results.
```

## License

MIT
