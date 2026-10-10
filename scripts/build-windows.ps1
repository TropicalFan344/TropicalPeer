# Builds the Windows app into release\TropicalPeer-<version>.exe.
#
#   powershell -ExecutionPolicy Bypass -File scripts\build-windows.ps1
#
# Needs Go and Node.js. Installs the Wails CLI if it's missing.
# The rendezvous server has its own script: build-server.ps1.
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent

function Step($text) { Write-Host "`n==> $text" -ForegroundColor Green }
function Check($what) { if ($LASTEXITCODE -ne 0) { throw "$what failed (see the output above)" } }
function Need($cmd, $hint) { if (-not (Get-Command $cmd -ErrorAction SilentlyContinue)) { throw "$cmd not found. $hint" } }

Need go 'Install Go from https://go.dev/dl/ (the version in go.mod or newer).'
Need npm 'Install Node.js 20 or newer from https://nodejs.org/.'
$env:PATH = "$(go env GOPATH)\bin;$env:PATH"
if (-not (Get-Command wails -ErrorAction SilentlyContinue)) {
  Step 'Installing the Wails CLI'
  go install github.com/wailsapp/wails/v2/cmd/wails@v2.16.0
  Check 'Installing Wails'
}

Push-Location $root
try {
  Step 'Checking the language files'
  go test ./locales
  Check 'The language check'

  $running = Get-Process TropicalPeer -ErrorAction SilentlyContinue | Where-Object { $_.Path -eq "$root\build\bin\TropicalPeer.exe" }
  if ($running) { throw 'TropicalPeer is running from build\bin. Close it first, then build again.' }

  Step 'Building the app (installs the interface''s packages the first time)'
  wails build -clean -platform windows/amd64
  Check 'The app build'

  $version = (Get-Content wails.json -Raw | ConvertFrom-Json).info.productVersion
  New-Item -ItemType Directory -Force release | Out-Null
  $exe = "release\TropicalPeer-$version.exe"
  Copy-Item build\bin\TropicalPeer.exe $exe -Force

  $mb = [math]::Round((Get-Item $exe).Length / 1MB, 1)
  Step "Done: $root\$exe ($mb MB)"
} finally {
  Pop-Location
}
