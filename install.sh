#!/usr/bin/env bash
set -euo pipefail

REPO="housien/hawk-tui"
SKILL_DIR="$HOME/.claude/skills/hawk"
INSTALL_DIR="/usr/local/bin"

echo "hawk — Helps Agents Watch Kommands"
echo ""

# --- Install binary ---

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
  x86_64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
esac

if command -v go &>/dev/null; then
  echo "Building from source..."
  TMPDIR=$(mktemp -d)
  git clone --depth 1 "https://github.com/$REPO.git" "$TMPDIR/hawk-tui" 2>/dev/null
  cd "$TMPDIR/hawk-tui"
  go build -o hawk .
  echo "Installing hawk to $INSTALL_DIR (may require sudo)..."
  sudo cp hawk "$INSTALL_DIR/hawk"
  rm -rf "$TMPDIR"
else
  echo "Error: Go is required to build hawk."
  echo "Install Go from https://go.dev/dl/ and re-run this script."
  exit 1
fi

echo "Installed: $(hawk help 2>&1 | head -1)"
echo ""

# --- Install Claude Code skill ---

echo "Installing Claude Code skill..."
mkdir -p "$SKILL_DIR"
curl -fsSL "https://raw.githubusercontent.com/$REPO/main/skill/SKILL.md" -o "$SKILL_DIR/SKILL.md"
echo "Skill installed to $SKILL_DIR/SKILL.md"
echo ""

echo "Done! Add this to your CLAUDE.md for best results:"
echo ""
echo '  - ALWAYS use `hawk start <name> -- <command>` with `run_in_background: true`'
echo '    when running any shell command that may take more than a few seconds.'
echo ""
echo "Usage:"
echo "  hawk start test -- pnpm test-all    # start a task"
echo "  hawk list                           # see all tasks"
echo "  hawk tail test                      # follow live output"
echo "  hawk output test                    # view with less"
echo "  hawk                                # fzf interactive picker"
