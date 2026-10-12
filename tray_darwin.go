package main

import _ "embed"

// trayIcon is a template image: macOS colours it to suit the menu bar.
//
//go:embed build/darwin/tray.png
var trayIcon []byte

var toastIcon []byte // Windows only
