//go:build !windows && !darwin

package desktopapp

import "errors"

func (a *App) initNotes()            {}
func (a *App) sendNote(note) error   { return errors.New("no notifications here") }
func (a *App) removeNote(string)     {}
func (a *App) notesLanguageChanged() {}
