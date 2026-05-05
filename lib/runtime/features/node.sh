#!/usr/bin/env bash
set -euo pipefail
VERSION="${FEATURE_VERSION:-lts}"
echo "[feature/node] installing node $VERSION"
curl -fsSL https://raw.githubusercontent.com/tj/n/master/bin/n -o /usr/local/bin/n
chmod +x /usr/local/bin/n
n install "$VERSION"
node --version
