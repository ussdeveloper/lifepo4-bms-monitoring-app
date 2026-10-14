#!/usr/bin/env bash
# Cross-builds the Windows application from Linux (for example from WSL).
#
#   ./scripts/build-linux.sh
#
# The result is a Windows executable with the same icon resource as the
# Windows build: dist/lifepo4-bms-monitor.exe
#
# Requirements:
#   * Go 1.24 or newer (https://go.dev/dl/)
#   * optional: rsrc to regenerate the icon resource (go install github.com/akavel/rsrc@latest)
#
# The build uses CGO_ENABLED=0 so no cross compiler is needed: the WebView2 and
# Bluetooth bindings are pure syscall bindings on Windows.

set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

if ! command -v go >/dev/null 2>&1; then
  cat <<'MESSAGE'
Go is not installed in this environment.
Install the toolchain, for example:

  curl -LO https://go.dev/dl/go1.26.0.linux-amd64.tar.gz
  sudo rm -rf /usr/local/go && sudo tar -C /usr/local -xzf go1.26.0.linux-amd64.tar.gz
  export PATH=$PATH:/usr/local/go/bin

MESSAGE
  exit 1
fi

echo "go version: $(go version)"

syso="icon_windows_amd64.syso"
if command -v rsrc >/dev/null 2>&1; then
  echo "Refreshing the icon resource ..."
  rsrc -ico assets/icon.ico -o "$syso"
elif [ -f "$syso" ]; then
  echo "rsrc not available, using the checked-in $syso"
else
  echo "Warning: neither rsrc nor $syso are available, the executable will have the default icon" >&2
fi

mkdir -p dist

echo "Cross-compiling dist/lifepo4-bms-monitor.exe ..."
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
  go build -trimpath -ldflags "-H=windowsgui -s -w" -o dist/lifepo4-bms-monitor.exe .

# The package targets Windows only, so its tests cannot be executed here (they
# run as Windows binaries). Vet still type-checks the whole Windows build.
echo "Type-checking the Windows target ..."
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go vet ./... || {
  echo "go vet reported problems" >&2
  exit 1
}

echo "Done: dist/lifepo4-bms-monitor.exe"
echo "Run 'go test ./...' on Windows to execute the unit tests."
