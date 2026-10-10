#!/bin/sh
# Builds the macOS app (Apple silicon and Intel in one) into
# release/TropicalPeer-<version>.dmg.
#
#   sh scripts/build-mac.sh
#
# Needs macOS 13 or newer with the Xcode command line tools
# (xcode-select --install), Go and Node.js. Installs the Wails CLI if missing.
# The rendezvous server has its own script: build-server.sh.
set -eu
cd "$(dirname "$0")/.."

step() { printf '\n\033[32m==> %s\033[0m\n' "$1"; }
need() { command -v "$1" >/dev/null 2>&1 || { echo "$1 not found. $2" >&2; exit 1; }; }

need xcode-select 'Install the Xcode command line tools: xcode-select --install'
xcode-select -p >/dev/null 2>&1 || { echo 'Install the Xcode command line tools: xcode-select --install' >&2; exit 1; }
need go 'Install Go from https://go.dev/dl/ (the version in go.mod or newer).'
need npm 'Install Node.js 20 or newer from https://nodejs.org/.'
export PATH="$(go env GOPATH)/bin:$PATH"
if ! command -v wails >/dev/null 2>&1; then
  step 'Installing the Wails CLI'
  go install github.com/wailsapp/wails/v2/cmd/wails@v2.16.0
fi

step 'Checking the language files'
go test ./locales

step "Building the app (installs the interface's packages the first time)"
wails build -clean -platform darwin/universal

app=build/bin/TropicalPeer.app
# Not notarized: sign ad hoc so macOS sees one consistent app. The first
# launch on another Mac needs right-click > Open (see README).
codesign --force --deep --sign - "$app"

version=$(sed -n 's/.*"productVersion": *"\([^"]*\)".*/\1/p' wails.json)
mkdir -p release
dmg="release/TropicalPeer-$version.dmg"

step 'Making the disk image'
stage=$(mktemp -d)
cp -R "$app" "$stage/"
ln -s /Applications "$stage/Applications"
rm -f "$dmg"
hdiutil create -volname TropicalPeer -srcfolder "$stage" -ov -format UDZO "$dmg" >/dev/null
rm -rf "$stage"

step "Done: $(pwd)/$dmg ($(du -h "$dmg" | awk '{print $1}'))"
