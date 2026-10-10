#!/bin/sh
# Builds the rendezvous server for Linux, Windows and macOS (x64 and ARM64)
# into release/server/. The server is pure Go, so any machine builds them all.
#
#   sh scripts/build-server.sh
#
# Needs Go. build-server.ps1 is the Windows version.
set -eu
cd "$(dirname "$0")/.."
command -v go >/dev/null 2>&1 || { echo 'go not found. Install Go from https://go.dev/dl/ (the version in go.mod or newer).' >&2; exit 1; }

mkdir -p release/server
for target in linux/amd64 linux/arm64 windows/amd64 windows/arm64 darwin/amd64 darwin/arm64; do
  os=${target%/*}
  arch=${target#*/}
  name=$(echo "$os" | sed 's/darwin/macos/')
  out="release/server/tropicalpeer-server-$name-$arch"
  [ "$os" = windows ] && out="$out.exe"
  CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags='-s -w' -o "$out" ./cmd/server
  echo "Built $out"
done
