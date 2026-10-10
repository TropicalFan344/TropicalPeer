#!/bin/sh
# Builds the Android app into release/TropicalPeer-<version>.apk
# (macOS and Linux; build-android.ps1 is the Windows version).
#
#   sh scripts/build-android.sh                # full build
#   sh scripts/build-android.sh --skip-engine  # only the interface changed (frontend/src)
#   sh scripts/build-android.sh --install      # also install on the phone connected over USB
#
# Steps: check the language files, build the Go engine library, build the
# interface, copy it into the Android project, build and sign the APK.
set -eu
cd "$(dirname "$0")/.."
root=$(pwd)

skip_engine=0
install=0
for arg in "$@"; do
  case "$arg" in
    --skip-engine) skip_engine=1 ;;
    --install) install=1 ;;
    *) echo "Unknown option: $arg" >&2; exit 1 ;;
  esac
done

step() { printf '\n\033[32m==> %s\033[0m\n' "$1"; }
need() { command -v "$1" >/dev/null 2>&1 || { echo "$1 not found. $2" >&2; exit 1; }; }

need go 'Install Go from https://go.dev/dl/ (the version in go.mod or newer).'
need npm 'Install Node.js 20 or newer from https://nodejs.org/.'

# Gradle needs JDK 21 to 24. Android Studio ships one; use it if nothing is set.
if [ -z "${JAVA_HOME:-}" ]; then
  for d in "/Applications/Android Studio.app/Contents/jbr/Contents/Home" "$HOME/android-studio/jbr" /opt/android-studio/jbr; do
    [ -x "$d/bin/java" ] && JAVA_HOME="$d" && break
  done
fi
[ -n "${JAVA_HOME:-}" ] || { echo 'No JDK found. Install JDK 21 (or Android Studio), or set JAVA_HOME.' >&2; exit 1; }
export JAVA_HOME
export PATH="$JAVA_HOME/bin:$PATH"
echo "JDK: $JAVA_HOME"

if [ -z "${ANDROID_HOME:-}" ]; then
  for d in "$HOME/Library/Android/sdk" "$HOME/Android/Sdk"; do
    [ -d "$d" ] && ANDROID_HOME="$d" && break
  done
fi
[ -n "${ANDROID_HOME:-}" ] || { echo 'Android SDK not found. Install Android Studio, or set ANDROID_HOME.' >&2; exit 1; }
export ANDROID_HOME

step 'Checking the language files'
go test ./locales

if [ "$skip_engine" = 0 ]; then
  step 'Building the Go engine (a few minutes)'
  sh mobile/build.sh
fi

step 'Building the interface'
cd frontend
[ -d node_modules ] || npm ci
npm run build
npx cap sync android
cd ..

step 'Building and signing the APK'
cd frontend/android
[ -f local.properties ] || echo "sdk.dir=$ANDROID_HOME" > local.properties
chmod +x gradlew
./gradlew assembleRelease --console=plain -q
./gradlew --stop -q >/dev/null 2>&1 || true
cd "$root"

version=$(sed -n 's/.*versionName "\(.*\)".*/\1/p' frontend/android/app/build.gradle)
mkdir -p release
apk="release/TropicalPeer-$version.apk"
cp frontend/android/app/build/outputs/apk/release/app-release.apk "$apk"
step "Done: $root/$apk ($(du -h "$apk" | cut -f1))"

if [ "$install" = 1 ]; then
  step 'Installing on the connected phone'
  "$ANDROID_HOME/platform-tools/adb" install -r "$apk"
fi
