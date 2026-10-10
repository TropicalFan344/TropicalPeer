# Builds the rendezvous server for Linux, Windows and macOS (x64 and ARM64)
# into release\server\. The server is pure Go, so any machine builds them all.
#
#   powershell -ExecutionPolicy Bypass -File scripts\build-server.ps1
#
# Needs Go. build-server.sh is the macOS / Linux version.
$ErrorActionPreference = 'Stop'
if (-not (Get-Command go -ErrorAction SilentlyContinue)) { throw 'go not found. Install Go from https://go.dev/dl/ (the version in go.mod or newer).' }

Push-Location (Split-Path $PSScriptRoot -Parent)
$saved = @{ CGO_ENABLED = $env:CGO_ENABLED; GOOS = $env:GOOS; GOARCH = $env:GOARCH }
try {
  New-Item -ItemType Directory -Force release\server | Out-Null
  $env:CGO_ENABLED = '0'
  foreach ($target in 'linux/amd64', 'linux/arm64', 'windows/amd64', 'windows/arm64', 'darwin/amd64', 'darwin/arm64') {
    $os, $arch = $target -split '/'
    $out = "release\server\tropicalpeer-server-$($os -replace 'darwin', 'macos')-$arch"
    if ($os -eq 'windows') { $out += '.exe' }
    $env:GOOS = $os
    $env:GOARCH = $arch
    go build -trimpath -ldflags='-s -w' -o $out ./cmd/server
    if ($LASTEXITCODE -ne 0) { throw "Building $target failed (see the output above)" }
    Write-Host "Built $out"
  }
} finally {
  foreach ($k in $saved.Keys) {
    if ($null -eq $saved[$k]) { Remove-Item "env:$k" -ErrorAction SilentlyContinue } else { Set-Item "env:$k" $saved[$k] }
  }
  Pop-Location
}
