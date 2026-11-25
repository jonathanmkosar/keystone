# Starts Keystone in the background, with no window, unless something is
# already listening on the address. Windows runs it at sign-in through a
# shortcut in the Startup folder: docs/runbooks/start-at-sign-in.md.
#
#   powershell -NoProfile -WindowStyle Hidden -ExecutionPolicy Bypass -File tools\windows\start-keystone.ps1 -Address 127.0.0.1:9080
#
# The server runs from this checkout's bin\keystone.exe, with this checkout as
# its folder, so its data folder is the checkout's data\. Its output goes to
# %LOCALAPPDATA%\Keystone\logs, keeping the newest ten runs.
param([string]$Address = '127.0.0.1:7900')

$ErrorActionPreference = 'Stop'
$repo = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$exe = Join-Path $repo 'bin\keystone.exe'
$logs = Join-Path $env:LOCALAPPDATA 'Keystone\logs'
New-Item -ItemType Directory -Force $logs | Out-Null

$port = [int]($Address.Split(':')[-1])
if (Get-NetTCPConnection -State Listen -LocalPort $port -ErrorAction SilentlyContinue) {
    exit 0 # already running, or the port is taken: either way, start nothing
}

if (-not (Test-Path $exe)) {
    Push-Location $repo
    try { & go build -o bin\keystone.exe ./cmd/keystone } finally { Pop-Location }
    if (-not (Test-Path $exe)) { throw "Keystone could not be built at $exe" }
}

$stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
Start-Process -FilePath $exe -ArgumentList @('serve', '--address', $Address) -WorkingDirectory $repo -WindowStyle Hidden `
    -RedirectStandardOutput (Join-Path $logs "keystone-$stamp.log") `
    -RedirectStandardError (Join-Path $logs "keystone-$stamp.err.log")

# Keep the newest ten runs' logs.
Get-ChildItem $logs -Filter 'keystone-*.log' | Where-Object { $_.Name -notlike '*.err.log' } |
    Sort-Object LastWriteTime -Descending | Select-Object -Skip 10 | ForEach-Object {
        Remove-Item $_.FullName, ($_.FullName -replace '\.log$', '.err.log') -ErrorAction SilentlyContinue
    }
