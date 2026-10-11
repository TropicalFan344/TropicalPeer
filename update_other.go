//go:build !windows && !darwin

package main

import "errors"

// desktopUpdater: there is no Linux desktop release to update from.
type desktopUpdater struct{ app *App }

func (u desktopUpdater) AssetName(string) string { return "" }
func (u desktopUpdater) Dir() string             { return "" }
func (u desktopUpdater) InstallOnQuit() bool     { return false }
func (u desktopUpdater) Install(string) error    { return errors.New("updates aren't available here") }
func (u desktopUpdater) Relaunch() error         { return errors.New("updates aren't available here") }

func cleanUpAfterUpdate(string) {}
func waitForExit(int)           {}
