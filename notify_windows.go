package main

import (
	_ "embed"
	"html"
	"log"
	"os"
	"path/filepath"
	"strings"

	"git.sr.ht/~jackmordaunt/go-toast/v2"
)

// Windows toasts, through the library Wails itself uses. Not through
// Wails' own notifications: those name the app after its file
// ("TropicalPeer-0.1.11.exe") and register it anew for every version.

const (
	toastAppID = "TropicalPeer"
	// toastGUID names the app's notification callback to Windows; fixed so
	// every version shares one registration.
	toastGUID = "{B76908D7-EE5C-4AB1-BD1B-FC04859F2ED9}"
)

//go:embed build/appicon.png
var toastIcon []byte

func (a *App) initNotes() {
	data := toast.AppData{AppID: toastAppID, GUID: toastGUID}
	if dir, err := os.UserCacheDir(); err == nil {
		icon := filepath.Join(dir, "TropicalPeer", "notification.png")
		if os.MkdirAll(filepath.Dir(icon), 0o755) == nil && os.WriteFile(icon, toastIcon, 0o644) == nil {
			data.IconPath = icon
		}
	}
	if err := toast.SetAppData(data); err != nil {
		log.Printf("notifications: %v", err)
	}
	toast.SetActivationCallback(func(args string, _ []toast.UserData) {
		id, action, _ := strings.Cut(args, "|")
		go a.onNote(id, action)
	})
}

func (a *App) sendNote(n note) error {
	t := toast.Notification{
		AppID:               toastAppID,
		Title:               cdata(n.title),
		Body:                cdata(n.body),
		ActivationArguments: html.EscapeString(n.id + "|"),
	}
	if len(n.actions) > 0 {
		t.Duration = toast.Long // an offer waits for an answer
	}
	for _, act := range n.actions {
		t.Actions = append(t.Actions, toast.Action{
			Type:      toast.Foreground,
			Content:   html.EscapeString(act.title),
			Arguments: html.EscapeString(n.id + "|" + act.id),
		})
	}
	return t.Push()
}

// removeNote can't take a toast back: the library has no call for it. An
// offer answered in the app leaves its toast in the Action Center, where
// its buttons then do nothing.
func (a *App) removeNote(string) {}

// notesLanguageChanged: each toast carries its own button labels.
func (a *App) notesLanguageChanged() {}

// cdata keeps text from ending the CDATA section it is placed in.
func cdata(s string) string { return strings.ReplaceAll(s, "]]>", "]] >") }
