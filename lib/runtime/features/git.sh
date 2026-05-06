#!/usr/bin/env bash
set -euo pipefail
echo "[feature/git] installing latest git"
apt-get update -qq
apt-get install -y -qq git >/dev/null
git --version
