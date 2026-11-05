#!/usr/bin/env bash
# Собирает wdtt-admin-panel.zip (исходники + бинарники + deploy-файлы).
set -euo pipefail
cd "$(dirname "$0")"
./build.sh >/dev/null
OUT="${1:-../wdtt-admin-panel.zip}"
T="$(mktemp -d)"; D="$T/wdtt-admin-panel"
mkdir -p "$D/bin"
cp -r cmd go.mod build.sh README.md "$D/"
cp bin/* "$D/bin/"
cp deploy/deploy.sh "$D/deploy.sh"
# Windows-файлы: CRLF; .ps1 с BOM, иначе PowerShell 5 ломает кириллицу
sed 's/$/\r/' deploy/deploy.bat > "$D/deploy.bat"
{ printf '\xef\xbb\xbf'; sed 's/$/\r/' deploy/deploy.ps1; } > "$D/deploy.ps1"
chmod +x "$D/deploy.sh" "$D/build.sh"
(cd "$D" && sha256sum bin/* deploy.sh > SHA256SUMS)
rm -f "$OUT"; (cd "$T" && zip -qr - wdtt-admin-panel) > "$OUT"
rm -rf "$T"; echo "$OUT"
