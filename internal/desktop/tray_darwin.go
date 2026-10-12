package desktop

/*
#include <stdlib.h>
void trayStart(const void *png, int len, const char *open, const char *quit);
void traySetLabels(const char *open, const char *quit);
*/
import "C"

import "unsafe"

var trayH trayHandlers

//export trayOpenClicked
func trayOpenClicked() { trayH.onOpen() }

//export trayQuitClicked
func trayQuitClicked() { trayH.onQuit() }

// StartTray puts the app's icon in the menu bar, with a menu to open or quit
// the app. icon is a PNG drawn in black, used as a template image so it
// follows the menu bar's light or dark look.
func StartTray(icon []byte, labels TrayLabels, open, quit func()) {
	trayH = trayHandlers{open: open, quit: quit}
	png := C.CBytes(icon)
	defer C.free(png)
	o, q := C.CString(labels.Open), C.CString(labels.Quit)
	defer C.free(unsafe.Pointer(o))
	defer C.free(unsafe.Pointer(q))
	C.trayStart(png, C.int(len(icon)), o, q)
}

// SetTrayLabels changes the menu's language.
func SetTrayLabels(labels TrayLabels) {
	o, q := C.CString(labels.Open), C.CString(labels.Quit)
	defer C.free(unsafe.Pointer(o))
	defer C.free(unsafe.Pointer(q))
	C.traySetLabels(o, q)
}

// StopTray does nothing: macOS removes the icon when the app exits.
func StopTray() {}
