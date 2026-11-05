#!/usr/bin/env bash
# Пересборка бинарников из исходников (нужен Go 1.24+).
set -euo pipefail
cd "$(dirname "$0")"
mkdir -p bin
for arch in amd64 arm64; do
  CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath -ldflags "-s -w" -o "bin/wdtt-admin-linux-$arch" ./cmd/wdtt-admin
  echo "bin/wdtt-admin-linux-$arch"
done
