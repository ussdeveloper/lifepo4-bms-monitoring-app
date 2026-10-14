# Building

## Prerequisites

| Component | Needed for | Notes |
|---|---|---|
| Go 1.24+ | desktop application | <https://go.dev/dl/> |
| WebView2 runtime | running the application | preinstalled on Windows 11 and current Windows 10 |
| Python 3.9+ with `bleak` | the command line tool | `python -m pip install -r cli/requirements.txt` |
| `rsrc` (optional) | refreshing the embedded icon | `go install github.com/akavel/rsrc@latest` |

## Windows

```powershell
# desktop application (icon is embedded, unit tests are run)
powershell -ExecutionPolicy Bypass -File scripts\build-windows.ps1

# additionally build the standalone Python CLI executable
powershell -ExecutionPolicy Bypass -File scripts\build-windows.ps1 -Cli

# quick build without touching the icon resource
powershell -ExecutionPolicy Bypass -File scripts\build-windows.ps1 -SkipIcon
```

The result is `dist\lifepo4-bms-monitor.exe`: a single file, GUI subsystem
(no console window), with the application icon in its resources so Explorer, the
task bar and the Alt+Tab switcher show it.

For development, a plain `go build .` works as well — the checked-in
`icon_windows_amd64.syso` already contains the icon.

## Linux / WSL

The application runs on Windows only, but it can be cross-compiled from Linux
(for example from WSL) because the WebView2 and Bluetooth bindings are pure
syscall bindings:

```bash
./scripts/build-linux.sh
```

The script:

1. checks for a Go toolchain and prints installation instructions if missing,
2. regenerates `icon_windows_amd64.syso` when `rsrc` is available (otherwise the
   checked-in resource is used),
3. cross-compiles with `GOOS=windows GOARCH=amd64 CGO_ENABLED=0` and
   `-ldflags "-H=windowsgui -s -w"`,
4. type-checks the Windows target with `go vet`.

The unit tests are Windows binaries, so they cannot be executed inside WSL — run
`go test ./...` on Windows (the Windows build script does it automatically).

Installing Go inside WSL without root:

```bash
mkdir -p ~/tools && cd ~/tools
curl -fsSLO https://go.dev/dl/go1.26.0.linux-amd64.tar.gz
tar -xzf go1.26.0.linux-amd64.tar.gz
export PATH=$HOME/tools/go/bin:$PATH
```

## Icon

`assets/make_icon.py` draws the icon with Pillow (rounded square with a teal to
purple gradient and a white inset mark, matching the logo in the interface) and
writes `assets/icon.ico` with sizes from 16 to 256 px.

```powershell
.venv\Scripts\python.exe assets\make_icon.py
& "$env:USERPROFILE\go\bin\rsrc.exe" -ico assets\icon.ico -o icon_windows_amd64.syso
```

The `.syso` file is picked up automatically by the Go linker for
`GOOS=windows GOARCH=amd64` builds and ignored by every other target, which is
why Linux builds can run the tests as well.

## Tests

```powershell
go test ./...                      # protocol, store, window and classification tests
python -m pytest cli/tests -q      # command line tool tests
```

The Go tests include real captured BMS frames, so a broken checksum or offset is
caught without hardware. Bluetooth itself is not exercised in tests.

## Release checklist

1. Update `CHANGELOG.md` and the `appVersion` constant in `main.go`.
2. Run both test suites.
3. `scripts\build-windows.ps1` (Windows) and `scripts/build-linux.sh` (WSL
   cross-check) — the binaries should be byte-comparable in behaviour, not size.
4. Attach `dist\lifepo4-bms-monitor.exe` to the GitHub release.
