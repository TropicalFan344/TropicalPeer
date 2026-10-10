# Builds the Go side of the Android app into build\android\tropicalpeer.aar.
#
# Needs the Android SDK with an NDK and a JDK. Installs gomobile if missing.
# Run from anywhere:  powershell -File mobile\build.ps1
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent

if (-not $env:ANDROID_HOME) { $env:ANDROID_HOME = Join-Path $env:LOCALAPPDATA 'Android\Sdk' }
if (-not $env:ANDROID_NDK_HOME) {
  $ndk = Get-ChildItem (Join-Path $env:ANDROID_HOME 'ndk') -Directory -ErrorAction SilentlyContinue |
    Sort-Object Name -Descending | Select-Object -First 1
  if (-not $ndk) { throw "No NDK under $env:ANDROID_HOME\ndk. Install one with: sdkmanager `"ndk;27.2.12479018`"" }
  $env:ANDROID_NDK_HOME = $ndk.FullName
}
if (-not $env:JAVA_HOME) {
  $jdk = Get-ChildItem (Join-Path $env:USERPROFILE '.jdks') -Directory -Filter '*17*' -ErrorAction SilentlyContinue |
    Select-Object -First 1
  if ($jdk) { $env:JAVA_HOME = $jdk.FullName }
}
if ($env:JAVA_HOME) { $env:PATH = "$env:JAVA_HOME\bin;$env:PATH" }
$env:PATH = "$(go env GOPATH)\bin;$env:PATH"
if (-not (Get-Command gomobile -ErrorAction SilentlyContinue)) {
  go install golang.org/x/mobile/cmd/gomobile@latest
  if ($LASTEXITCODE -ne 0) { throw "Installing gomobile failed" }
  gomobile init
}

New-Item -ItemType Directory -Force (Join-Path $root 'build\android') | Out-Null
Push-Location $root
try {
  # arm64 for current phones, arm for older 32-bit ones, amd64 for the emulator
  # (release APKs leave the emulator one out; see frontend/android/app/build.gradle).
  gomobile bind -target='android/arm,android/arm64,android/amd64' -androidapi 24 -javapkg app.tropicalpeer `
    -o build\android\tropicalpeer.aar ./mobile
  if ($LASTEXITCODE -ne 0) { throw "gomobile bind failed" }
  Write-Host "Built build\android\tropicalpeer.aar"
} finally {
  Pop-Location
}
