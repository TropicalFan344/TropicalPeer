#!/bin/sh
# Builds the Go side of the Android app into build/android/tropicalpeer.aar
# (macOS and Linux; mobile/build.ps1 is the Windows version).
#
# Needs the Android SDK with an NDK and a JDK. Installs gomobile if missing.
set -eu
cd "$(dirname "$0")/.."

if [ -z "${ANDROID_HOME:-}" ]; then
  for d in "$HOME/Library/Android/sdk" "$HOME/Android/Sdk"; do
    [ -d "$d" ] && ANDROID_HOME="$d" && break
  done
fi
[ -n "${ANDROID_HOME:-}" ] || { echo 'Android SDK not found. Install Android Studio, or set ANDROID_HOME.' >&2; exit 1; }
export ANDROID_HOME
if [ -z "${ANDROID_NDK_HOME:-}" ]; then
  ANDROID_NDK_HOME=$(ls -d "$ANDROID_HOME"/ndk/*/ 2>/dev/null | sort -V | tail -n 1 | sed 's:/$::')
  [ -n "$ANDROID_NDK_HOME" ] || { echo "No NDK under $ANDROID_HOME/ndk. Install one with: sdkmanager \"ndk;27.2.12479018\"" >&2; exit 1; }
fi
export ANDROID_NDK_HOME

export PATH="$(go env GOPATH)/bin:$PATH"
if ! command -v gomobile >/dev/null 2>&1; then
  go install golang.org/x/mobile/cmd/gomobile@latest
  gomobile init
fi

mkdir -p build/android
# arm64 for current phones, arm for older 32-bit ones, amd64 for the emulator
# (release APKs leave the emulator one out; see frontend/android/app/build.gradle).
# The app's version, from the Android project, shows in Settings and the log.
version=$(sed -n 's/.*versionName "\(.*\)".*/\1/p' frontend/android/app/build.gradle)
gomobile bind -target='android/arm,android/arm64,android/amd64' -androidapi 24 -javapkg app.tropicalpeer \
  -ldflags "-X TropicalPeer/internal/appcore.Version=$version" -o build/android/tropicalpeer.aar ./mobile
echo 'Built build/android/tropicalpeer.aar'
