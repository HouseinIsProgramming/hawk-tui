#!/usr/bin/env bash
set -euo pipefail

REPO="houseinisprogramming/hawk-tui"
SKILL_DIR="$HOME/.claude/skills/hawk"
INSTALL_DIR="/usr/local/bin"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

info() { echo -e "${GREEN}→${NC} $1"; }
warn() { echo -e "${YELLOW}→${NC} $1"; }
error() { echo -e "${RED}✗${NC} $1" >&2; exit 1; }

detect_platform() {
  local os arch
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  arch="$(uname -m)"

  case "$arch" in
    arm64|aarch64) arch="arm64" ;;
    x86_64|amd64)  arch="amd64" ;;
    *) error "Unsupported architecture: $arch" ;;
  esac

  case "$os" in
    darwin) ;;
    linux)  ;;
    *) error "Unsupported OS: $os" ;;
  esac

  echo "${os}-${arch}"
}

download() {
  local url="$1" dest="$2"
  if command -v curl &>/dev/null; then
    curl -fsSL "$url" -o "$dest"
  elif command -v wget &>/dev/null; then
    wget -q "$url" -O "$dest"
  else
    error "Neither curl nor wget found"
  fi
}

install_binary() {
  local platform="$1"
  local download_url="https://github.com/${REPO}/releases/latest/download/hawk-${platform}"

  info "Downloading hawk-${platform}..."
  local tmp
  tmp="$(mktemp)"

  if download "$download_url" "$tmp"; then
    chmod +x "$tmp"
    info "Installing hawk to $INSTALL_DIR (may require sudo)..."
    sudo mv "$tmp" "$INSTALL_DIR/hawk"
    return 0
  fi

  rm -f "$tmp"
  return 1
}

install_from_source() {
  if ! command -v go &>/dev/null; then
    return 1
  fi

  warn "Building from source..."
  local tmpdir
  tmpdir="$(mktemp -d)"
  git clone --depth 1 "https://github.com/$REPO.git" "$tmpdir/hawk-tui" 2>/dev/null
  cd "$tmpdir/hawk-tui"
  go build -o hawk .
  info "Installing hawk to $INSTALL_DIR (may require sudo)..."
  sudo cp hawk "$INSTALL_DIR/hawk"
  rm -rf "$tmpdir"
}

main() {
  echo ""
  echo "  hawk — Helps Agents Watch Kommands"
  echo ""

  local platform
  platform="$(detect_platform)"
  info "Detected platform: $platform"

  if ! install_binary "$platform"; then
    warn "Binary download failed, falling back to source build..."
    install_from_source || error "Install failed. Install Go (https://go.dev/dl/) or check https://github.com/${REPO}/releases"
  fi

  info "Installed: $(hawk help 2>&1 | head -1)"
  echo ""

  # Install Claude Code skill
  info "Installing Claude Code skill..."
  mkdir -p "$SKILL_DIR"
  download "https://raw.githubusercontent.com/$REPO/main/skill/SKILL.md" "$SKILL_DIR/SKILL.md" \
    || warn "Could not download skill file"
  info "Skill installed to $SKILL_DIR/SKILL.md"

  echo ""
  info "Done! Add this to your CLAUDE.md for best results:"
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
}

main "$@"
