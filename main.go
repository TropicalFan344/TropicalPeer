// TropicalPeer desktop app.
//
// Set TROPICALPEER_DATA to a folder to run a second, independent copy on the
// same machine (useful for testing pairing and transfers locally).
package main

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"

	"TropicalPeer/internal/node"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	dataDir := os.Getenv("TROPICALPEER_DATA")
	if dataDir == "" {
		dataDir = node.DefaultDataDir()
	}
	handleAfterUpdate(dataDir, os.Args[1:])
	app := NewApp(dataDir)

	// A Mac app needs the standard menus: without Edit, Cmd+C/V/A don't
	// reach the text fields, and without the app menu there's no Cmd+Q.
	var appMenu *menu.Menu
	if runtime.GOOS == "darwin" {
		appMenu = menu.NewMenu()
		appMenu.Append(menu.AppMenu())
		appMenu.Append(menu.EditMenu())
		appMenu.Append(menu.WindowMenu())
	}

	err := wails.Run(&options.App{
		// One copy per profile: opening the app again brings the running
		// window forward. (Two copies with one identity would keep taking
		// the server registration from each other.) A different
		// TROPICALPEER_DATA is a different profile and may run alongside.
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               instanceID(dataDir),
			OnSecondInstanceLaunch: app.onSecondInstance,
		},
		Title:            "TropicalPeer",
		Width:            1040,
		Height:           680,
		MinWidth:         760,
		MinHeight:        520,
		AssetServer:      &assetserver.Options{Assets: assets},
		BackgroundColour: &options.RGBA{R: 14, G: 22, B: 19, A: 255},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		// Closing the window keeps the app running in the tray (the menu
		// bar on a Mac), so offers and transfers still arrive.
		HideWindowOnClose: true,
		Bind:              []any{app},
		Menu:              appMenu,
		// Files dropped from Explorer or Finder arrive in the frontend with their full
		// paths (see OnFileDrop in frontend/src/lib/drop.svelte.ts).
		DragAndDrop: &options.DragAndDrop{EnableFileDrop: true},
		Windows: &windows.Options{
			Theme: windows.SystemDefault,
		},
		Mac: &mac.Options{
			About: &mac.AboutInfo{Title: "TropicalPeer", Message: "Peer-to-peer file drop"},
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}

// instanceID names the single-instance lock after the profile folder.
func instanceID(dataDir string) string {
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		abs = dataDir
	}
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		abs = strings.ToLower(abs) // case-insensitive file systems
	}
	sum := sha256.Sum256([]byte(abs))
	return "app.tropicalpeer." + hex.EncodeToString(sum[:8])
}
