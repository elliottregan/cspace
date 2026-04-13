#!/usr/bin/env bash
# Install cspace — Portable CLI for managing Claude Code devcontainer instances
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/elliottregan/cspace/main/install.sh | bash
#
# Environment:
#   CSPACE_HOME  — Override install directory (default: ~/.cspace)
#   VERSION      — Install a specific version (default: latest)
set -euo pipefail

REPO="elliottregan/cspace"
INSTALL_DIR="${CSPACE_HOME:-$HOME/.cspace}"
BIN_DIR="$INSTALL_DIR/bin"

# --- Detect OS and architecture ---

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
    x86_64)         ARCH="amd64" ;;
    aarch64|arm64)  ARCH="arm64" ;;
    *)
        echo "Unsupported architecture: $ARCH" >&2
        exit 1
        ;;
esac

case "$OS" in
    darwin|linux) ;;
    *)
        echo "Unsupported OS: $OS" >&2
        exit 1
        ;;
esac

echo "Detected platform: ${OS}/${ARCH}"

# --- Determine version to install ---

if [ -z "${VERSION:-}" ]; then
    echo "Fetching latest release..."
    VERSION=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" \
        | grep '"tag_name"' \
        | sed -E 's/.*"([^"]+)".*/\1/')

    if [ -z "$VERSION" ]; then
        echo "Error: Could not determine latest version." >&2
        echo "Set VERSION=vX.Y.Z to install a specific version." >&2
        exit 1
    fi
fi

echo "Installing cspace ${VERSION}..."

# --- Download binary and checksums ---

ASSET_NAME="cspace-${OS}-${ARCH}"
DOWNLOAD_URL="https://github.com/$REPO/releases/download/$VERSION/$ASSET_NAME"
CHECKSUM_URL="https://github.com/$REPO/releases/download/$VERSION/checksums.txt"

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

echo "Downloading ${ASSET_NAME}..."
if ! curl -fsSL -o "$TMPDIR/$ASSET_NAME" "$DOWNLOAD_URL"; then
    echo "Error: Failed to download $DOWNLOAD_URL" >&2
    echo "" >&2
    echo "Available at: https://github.com/$REPO/releases/tag/$VERSION" >&2
    exit 1
fi

# --- Verify checksum ---

if curl -fsSL -o "$TMPDIR/checksums.txt" "$CHECKSUM_URL" 2>/dev/null; then
    echo "Verifying checksum..."
    (
        cd "$TMPDIR"
        if command -v shasum &>/dev/null; then
            grep -F "  ${ASSET_NAME}" checksums.txt | shasum -a 256 --check --quiet
            echo "Checksum verified."
        elif command -v sha256sum &>/dev/null; then
            grep -F "  ${ASSET_NAME}" checksums.txt | sha256sum --check --quiet
            echo "Checksum verified."
        else
            echo "Warning: No checksum tool found, skipping verification." >&2
        fi
    )
else
    echo "Warning: Could not download checksums, skipping verification." >&2
fi

# --- Install binary ---

mkdir -p "$BIN_DIR"

# Migration: detect old git-clone installation
if [ -d "$INSTALL_DIR/.git" ]; then
    echo ""
    echo "Detected existing git-clone installation at $INSTALL_DIR"
    echo "Migrating to binary installation..."
    echo ""
    echo "Your configuration files (.cspace.json, .cspace.local.json) are in"
    echo "your project directories and will not be affected."
    echo ""
    # Remove the git repo but preserve any user data
    rm -rf "$INSTALL_DIR/.git"
    rm -f "$INSTALL_DIR/bin/cspace"  # old bash script
    echo "Migration complete. Old git-clone files removed."
fi

cp "$TMPDIR/$ASSET_NAME" "$BIN_DIR/cspace"
chmod +x "$BIN_DIR/cspace"

# macOS requires binaries to be signed. Cross-compiled binaries from CI have
# no signature, so apply an ad-hoc signature to satisfy Gatekeeper.
if [ "$OS" = "darwin" ] && command -v codesign &>/dev/null; then
    codesign -s - "$BIN_DIR/cspace" 2>/dev/null || true
fi

# --- Add to PATH ---

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

# --- Install zsh completions ---

if [ "$SHELL_NAME" = "zsh" ]; then
    COMP_DIR="${ZDOTDIR:-$HOME}/.zsh/completions"
    mkdir -p "$COMP_DIR"
    # Generate completions from the binary
    if "$BIN_DIR/cspace" completion zsh > "$COMP_DIR/_cspace" 2>/dev/null; then
        echo "Installed zsh completions"
    fi
fi

# --- Verify installation ---

INSTALLED_VERSION=$("$BIN_DIR/cspace" version 2>/dev/null || echo "unknown")

echo ""
echo "cspace installed successfully!"
echo "  Binary:  $BIN_DIR/cspace"
echo "  Version: $INSTALLED_VERSION"
echo ""
echo "To get started:"
echo "  1. Restart your shell or run: source $RC_FILE"
echo "  2. cd into a project and run: cspace init"
echo "  3. Launch an instance: cspace up"
echo ""
echo "Prerequisites:"
echo "  - Docker (with Docker Compose v2)"
echo "  - A GitHub token (GH_TOKEN) for autonomous agents"
echo ""
echo "To update later: cspace self-update"
