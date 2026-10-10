# Builds the Android app into release\TropicalPeer-<version>.apk.
#
#   powershell -File scripts\build-android.ps1                # full build
#   powershell -File scripts\build-android.ps1 -SkipEngine    # only the interface changed (frontend\src)
#   powershell -File scripts\build-android.ps1 -Install       # also install on the phone connected over USB
#
# Steps: check the language files, build the Go engine library, build the
# interface, copy it into the Android project, build and sign the APK.
param(
  [switch]$SkipEngine,
  [switch]$Install
)
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent

function Step($text) { Write-Host "`n==> $text" -ForegroundColor Green }
function Check($what) { if ($LASTEXITCODE -ne 0) { throw "$what failed (see the output above)" } }

# Capacitor needs JDK 21 or newer, with jlink (not the JetBrains runtime).
if (-not $env:JAVA_HOME -or -not (Test-Path "$env:JAVA_HOME\bin\jlink.exe")) {
  $jdk = Get-ChildItem "$env:USERPROFILE\.jdks" -Directory -ErrorAction SilentlyContinue |
    Where-Object { (Test-Path "$($_.FullName)\bin\jlink.exe") -and $_.Name -match '(\d+)\.' -and [int]$Matches[1] -ge 21 } |
    # Gradle 8.14 supports JDK 21 to 24; prefer those over anything newer.
    Sort-Object @{ e = { [int]($_.Name -replace '^\D*(\d+).*$', '$1') -le 24 } }, Name -Descending | Select-Object -First 1
  if (-not $jdk) { throw "Install JDK 21 or newer (Android Studio: File > Settings > Build Tools > Gradle > Download JDK)" }
  $env:JAVA_HOME = $jdk.FullName
}
$sdk = if ($env:ANDROID_HOME) { $env:ANDROID_HOME } else { "$env:LOCALAPPDATA\Android\Sdk" }
Write-Host "JDK: $env:JAVA_HOME"

Push-Location $root
try {
  Step 'Checking the language files'
  go test ./locales
  Check 'The language check'

  if (-not $SkipEngine) {
    Step 'Building the Go engine (a few minutes)'
    # A separate JDK 17 is fine here; build.ps1 finds one itself.
    $saved = $env:JAVA_HOME
    $env:JAVA_HOME = $null
    powershell -NoProfile -ExecutionPolicy Bypass -File mobile\build.ps1
    $code = $LASTEXITCODE
    $env:JAVA_HOME = $saved
    if ($code -ne 0) { throw "The engine build failed (see the output above)" }
  }

  Step 'Building the interface'
  Push-Location frontend
  if (-not (Test-Path node_modules)) { npm ci; Check 'Installing the interface''s packages' }
  npm run build
  Check 'The interface build'
  npx cap sync android
  Check 'Copying the interface into the Android project'
  Pop-Location

  Step 'Building and signing the APK'
  Push-Location frontend\android
  if (-not (Test-Path local.properties)) { "sdk.dir=$($sdk -replace '\\','/')" | Out-File -Encoding ascii local.properties }
  .\gradlew.bat assembleRelease --console=plain -q
  Check 'The APK build'
  # Don't leave Gradle's background process running after the script ends.
  .\gradlew.bat --stop -q | Out-Null
  Pop-Location

  $version = (Select-String -Path frontend\android\app\build.gradle -Pattern 'versionName "(.+)"').Matches[0].Groups[1].Value
  New-Item -ItemType Directory -Force release | Out-Null
  $apk = "release\TropicalPeer-$version.apk"
  Copy-Item frontend\android\app\build\outputs\apk\release\app-release.apk $apk -Force
  $mb = [math]::Round((Get-Item $apk).Length / 1MB, 1)
  Step "Done: $root\$apk ($mb MB)"

  if ($Install) {
    Step 'Installing on the connected phone'
    & "$sdk\platform-tools\adb.exe" install -r $apk
    Check 'Installing (if it says the signatures differ, uninstall the old copy first)'
  }
} finally {
  Pop-Location
}
