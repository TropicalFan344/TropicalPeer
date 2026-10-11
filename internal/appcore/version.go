package appcore

// Version is the app's version, stamped in at build time by the build
// scripts (-ldflags "-X TropicalPeer/internal/appcore.Version=0.1.7").
// It shows in Settings and in the log, so it's clear what a device runs.
var Version = "dev"
