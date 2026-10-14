# Builds the Windows desktop application (and optionally the Python CLI).
#
#   powershell -ExecutionPolicy Bypass -File scripts\build-windows.ps1
#   powershell -ExecutionPolicy Bypass -File scripts\build-windows.ps1 -Cli
#
# Output: dist\lifepo4-bms-monitor.exe  (single file, GUI subsystem, icon embedded)

param(
    [switch]$Cli,
    [switch]$SkipIcon
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

$iconPath = Join-Path $root 'assets\icon.ico'
$sysoPath = Join-Path $root 'icon_windows_amd64.syso'

if (-not $SkipIcon) {
    if (-not (Test-Path $iconPath)) {
        Write-Host 'Generating assets\icon.ico ...'
        & (Join-Path $root '.venv\Scripts\python.exe') (Join-Path $root 'assets\make_icon.py')
    }
    $rsrc = Join-Path $env:USERPROFILE 'go\bin\rsrc.exe'
    if (Test-Path $rsrc) {
        Write-Host 'Embedding the icon into the executable ...'
        & $rsrc -ico $iconPath -o $sysoPath
    } else {
        Write-Warning 'rsrc.exe not found; the checked-in icon resource is used instead. Install it with: go install github.com/akavel/rsrc@latest'
    }
}

if (-not (Test-Path (Join-Path $root 'dist'))) {
    New-Item -ItemType Directory -Path (Join-Path $root 'dist') | Out-Null
}

Write-Host 'Building dist\lifepo4-bms-monitor.exe ...'
$env:GOOS = 'windows'
$env:GOARCH = 'amd64'
go build -trimpath -ldflags '-H=windowsgui -s -w' -o 'dist\lifepo4-bms-monitor.exe' .
if ($LASTEXITCODE -ne 0) { throw "go build failed with exit code $LASTEXITCODE" }

Write-Host 'Running unit tests ...'
go test ./...
if ($LASTEXITCODE -ne 0) { throw "go test failed with exit code $LASTEXITCODE" }

if ($Cli) {
    Write-Host 'Building dist\lifepo4-bms-cli.exe (Python CLI) ...'
    & (Join-Path $root '.venv\Scripts\python.exe') -m PyInstaller --noconfirm --onefile `
        --name lifepo4-bms-cli --distpath dist --workpath build --specpath build `
        (Join-Path $root 'cli\humsienk_cli.py')
    if ($LASTEXITCODE -ne 0) { throw "PyInstaller failed with exit code $LASTEXITCODE" }
}

Write-Host 'Done.'
