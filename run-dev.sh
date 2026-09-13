#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT"

export PATH="/usr/local/go/bin:${PATH:-}"

if ! command -v go >/dev/null 2>&1; then
  echo "error: go not found in PATH" >&2
  exit 1
fi

if [[ "$(id -u)" -ne 0 ]]; then
  exec sudo -E env "PATH=$PATH" go run .
fi

exec go run .
