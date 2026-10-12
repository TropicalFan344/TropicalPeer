package desktop

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>

static void bounceDock(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		if (![NSApp isActive]) [NSApp requestUserAttention:NSCriticalRequest];
	});
}

static int appActive(void) {
	return [NSApp isActive] && ![NSApp isHidden];
}
*/
import "C"

// FlashWindow bounces the Dock icon until the app is brought forward, the Mac
// counterpart of flashing the taskbar button on Windows.
func FlashWindow() { C.bounceDock() }

// WindowHidden is false on a Mac: a hidden app still bounces its Dock icon.
func WindowHidden() bool { return false }

// AppInFront reports whether the app is the one in use.
func AppInFront() bool { return C.appActive() != 0 }
