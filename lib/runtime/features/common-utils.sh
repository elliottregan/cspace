#!/usr/bin/env bash
set -euo pipefail
echo "[feature/common-utils] installing baseline utilities"
apt-get update -qq
apt-get install -y -qq curl wget git sudo less procps zsh >/dev/null
