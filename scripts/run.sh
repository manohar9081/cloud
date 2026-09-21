#!/usr/bin/env bash
# clouds launcher for Linux / macOS — builds the binary automatically on first run.
set -e
cd "$(dirname "$0")/.."

if [ ! -x clouds ]; then
    if ! command -v go >/dev/null; then
        echo "[clouds] Go is required to build ./clouds: https://go.dev/dl"
        echo "[clouds] install with:  sudo apt install golang-go   |   brew install go"
        exit 1
    fi
    echo "[clouds] first run: building ./clouds..."
    go build -trimpath -ldflags "-s -w" -o clouds .
fi

exec ./clouds "$@"
