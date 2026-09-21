#!/usr/bin/env bash
# Explicit build for Linux / macOS.
cd "$(dirname "$0")/.."
go build -trimpath -ldflags "-s -w" -o clouds . && echo "built ./clouds"
