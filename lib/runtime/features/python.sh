#!/usr/bin/env bash
set -euo pipefail
VERSION="${FEATURE_VERSION:-3}"
echo "[feature/python] installing python $VERSION"
apt-get update -qq
apt-get install -y -qq "python${VERSION}" "python${VERSION}-venv" "python${VERSION}-pip" >/dev/null
python${VERSION} --version
