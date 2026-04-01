#!/usr/bin/env bash
# Install cspace — Portable CLI for managing Claude Code devcontainer instances
set -euo pipefail

INSTALL_DIR="${CSPACE_HOME:-$HOME/.cspace}"
REPO="https://github.com/elliottregan/cspace.git"

echo "Installing cspace to $INSTALL_DIR..."

if [ -d "$INSTALL_DIR/.git" ]; then
    echo "Existing installation found. Updating..."
    git -C "$INSTALL_DIR" pull --ff-only
else
    git clone "$REPO" "$INSTALL_DIR"
fi

chmod +x "$INSTALL_DIR/bin/cspace"

# Detect shell and add to PATH
SHELL_NAME=$(basename "${SHELL:-/bin/bash}")
RC_FILE=""
case "$SHELL_NAME" in
    zsh)  RC_FILE="$HOME/.zshrc" ;;
    bash) RC_FILE="$HOME/.bashrc" ;;
    *)    RC_FILE="$HOME/.profile" ;;
esac

PATH_LINE='export PATH="$HOME/.cspace/bin:$PATH"'
if [ -f "$RC_FILE" ] && grep -qF '.cspace/bin' "$RC_FILE"; then
    echo "PATH already configured in $RC_FILE"
else
    echo "" >> "$RC_FILE"
    echo "# cspace CLI" >> "$RC_FILE"
    echo "$PATH_LINE" >> "$RC_FILE"
    echo "Added cspace to PATH in $RC_FILE"
fi

# Install zsh completions if applicable
if [ "$SHELL_NAME" = "zsh" ]; then
    COMP_DIR="${ZDOTDIR:-$HOME}/.zsh/completions"
    mkdir -p "$COMP_DIR"
    ln -sf "$INSTALL_DIR/completions/cspace.zsh" "$COMP_DIR/_cspace"
    echo "Installed zsh completions"
fi

echo ""
echo "cspace installed successfully!"
echo ""
echo "To get started:"
echo "  1. Restart your shell or run: source $RC_FILE"
echo "  2. cd into a project and run: cspace init"
echo "  3. Launch an instance: cspace up"
echo ""
echo "Prerequisites:"
echo "  - Docker (with Docker Compose v2)"
echo "  - jq (brew install jq / apt install jq)"
echo "  - gum (optional, for interactive TUI: brew install gum)"
echo "  - gh (optional, for GitHub integration: brew install gh)"
