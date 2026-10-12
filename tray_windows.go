package main

import _ "embed"

//go:embed build/windows/icon.ico
var trayIcon []byte

// toastIcon is shown on Windows notifications.
//
//go:embed build/appicon.png
var toastIcon []byte
