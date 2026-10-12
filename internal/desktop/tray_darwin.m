#import <Cocoa/Cocoa.h>
#include "_cgo_export.h"

// The menu bar icon. Everything here runs on the main thread; the C entry
// points copy their arguments before handing over to it.

@interface TPTrayTarget : NSObject
@end

@implementation TPTrayTarget
- (void)openApp:(id)sender {
	// Closing the window hides the whole app (Wails' HideWindowOnClose).
	[NSApp unhide:nil];
	[NSApp activateIgnoringOtherApps:YES];
	trayOpenClicked();
}
- (void)quitApp:(id)sender {
	trayQuitClicked();
}
@end

static NSStatusItem *statusItem;
static NSMenuItem *openItem, *quitItem;
static TPTrayTarget *target;

void trayStart(const void *png, int len, const char *open, const char *quit) {
	@autoreleasepool {
		NSData *data = [NSData dataWithBytes:png length:len];
		NSString *o = [NSString stringWithUTF8String:open];
		NSString *q = [NSString stringWithUTF8String:quit];
		dispatch_async(dispatch_get_main_queue(), ^{
			if (statusItem != nil) return;
			target = [[TPTrayTarget alloc] init];
			statusItem = [[[NSStatusBar systemStatusBar] statusItemWithLength:NSSquareStatusItemLength] retain];
			NSImage *image = [[NSImage alloc] initWithData:data];
			image.size = NSMakeSize(18, 18);
			image.template = YES;
			statusItem.button.image = image;
			statusItem.button.toolTip = @"TropicalPeer";
			[image release];

			NSMenu *menu = [[NSMenu alloc] init];
			openItem = [[NSMenuItem alloc] initWithTitle:o action:@selector(openApp:) keyEquivalent:@""];
			openItem.target = target;
			[menu addItem:openItem];
			[menu addItem:[NSMenuItem separatorItem]];
			quitItem = [[NSMenuItem alloc] initWithTitle:q action:@selector(quitApp:) keyEquivalent:@""];
			quitItem.target = target;
			[menu addItem:quitItem];
			statusItem.menu = menu;
			[menu release];
		});
	}
}

void traySetLabels(const char *open, const char *quit) {
	@autoreleasepool {
		NSString *o = [NSString stringWithUTF8String:open];
		NSString *q = [NSString stringWithUTF8String:quit];
		dispatch_async(dispatch_get_main_queue(), ^{
			// Before the icon exists the labels are set when it is made.
			openItem.title = o;
			quitItem.title = q;
		});
	}
}
