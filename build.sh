#!/usr/bin/env bash
set -euo pipefail

#
# Build agent-permissions into bin/.
#
# Produces a pure-Go, fully-static binary that runs on any Linux
# without libc version constraints.
#

REPO_DIR="$(cd "$(dirname "$0")" && pwd)"

if ! command -v go &>/dev/null; then
    echo "Error: go not found on PATH" >&2
    exit 1
fi

export CGO_ENABLED=0

mkdir -p "$REPO_DIR/bin"

VERSION="${AGENT_PERMISSIONS_VERSION:-dev}"
LDFLAGS="-X main.version=$VERSION"

echo "Building agent-permissions v${VERSION} ($(go version))..."

go build -C "$REPO_DIR" -ldflags "$LDFLAGS" \
    -o "$REPO_DIR/bin/agent-permissions" \
    ./cmd/agent-permissions

echo "✔ bin/agent-permissions"
