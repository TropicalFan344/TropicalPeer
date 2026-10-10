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
*/
import "C"

// FlashWindow bounces the Dock icon until the app is brought forward, the Mac
// counterpart of flashing the taskbar button on Windows.
func FlashWindow() { C.bounceDock() }
