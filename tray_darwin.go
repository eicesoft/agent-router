//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>

static NSWindow *agentRouterMainWindow;

// Set from the tray "退出" item; read by OnBeforeClose to distinguish a real
// quit from a window-close (which should hide to tray instead). cgo exposes
// this global to Go as C.agentRouterQuitRequested.
int agentRouterQuitRequested = 0;

@interface AgentRouterTrayTarget : NSObject
@end

@implementation AgentRouterTrayTarget
- (void)showWindow:(id)sender {
  dispatch_async(dispatch_get_main_queue(), ^{
    [NSApp setActivationPolicy:NSApplicationActivationPolicyRegular];
    [NSApp unhide:nil];
    [NSApp activateIgnoringOtherApps:YES];
    [agentRouterMainWindow deminiaturize:nil];
    [agentRouterMainWindow makeKeyAndOrderFront:nil];
  });
}
- (void)quitApp:(id)sender {
  agentRouterQuitRequested = 1;
  [NSApp terminate:self];
}
@end

static NSStatusItem *agentRouterStatusItem;
static AgentRouterTrayTarget *agentRouterTrayTarget;

static void agentRouterCreateTrayIcon(const unsigned char *bytes, int length) {
  if (agentRouterStatusItem != nil) return;

  // Remember the application window before creating the status-item window.
  for (NSWindow *window in NSApp.windows) {
    if (window.canBecomeMainWindow) {
      agentRouterMainWindow = [window retain];
      break;
    }
  }

  // cgo compiles this file without ARC. Keep ownership across autorelease
  // pool drains; a static pointer alone does not retain the status item.
  // -1.0 is NSStatusItemVariableLength: the button hugs its content so the
  // item is only as wide as the 18pt icon instead of a fixed 44pt slot.
  agentRouterStatusItem = [[[NSStatusBar systemStatusBar]
    statusItemWithLength:-1.0] retain];
  agentRouterTrayTarget = [AgentRouterTrayTarget new];

  NSButton *button = agentRouterStatusItem.button;
  button.toolTip = @"Agent Router";
  // Use the same custom mark as the app icon without the legacy "AR" label.
  // The colored image is intentionally not treated as a monochrome template.
  button.title = @"";

  NSData *data = [NSData dataWithBytes:bytes length:length];
  NSImage *image = [[NSImage alloc] initWithData:data];
  image.size = NSMakeSize(18, 18);
  image.template = NO;
  button.image = image;
  button.imageScaling = NSImageScaleProportionallyDown;

  // Assigning the menu to the status item makes macOS highlight the icon
  // and track the menu natively on click (left or right).
  NSMenu *menu = [[NSMenu alloc] initWithTitle:@"AgentRouterTrayMenu"];
  NSMenuItem *openItem = [[NSMenuItem alloc]
    initWithTitle:@"打开窗口"
    action:@selector(showWindow:)
    keyEquivalent:@""];
  [openItem setTarget:agentRouterTrayTarget];
  [menu addItem:openItem];
  [openItem release];

  NSMenuItem *quitItem = [[NSMenuItem alloc]
    initWithTitle:@"退出"
    action:@selector(quitApp:)
    keyEquivalent:@""];
  [quitItem setTarget:agentRouterTrayTarget];
  [menu addItem:quitItem];
  [quitItem release];

  agentRouterStatusItem.menu = menu;
  [menu release];
}

static void agentRouterHideToTray(void) {
  dispatch_async(dispatch_get_main_queue(), ^{
    [agentRouterMainWindow orderOut:nil];
    // Accessory apps keep their status item while leaving the Dock and app switcher.
    [NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
  });
}

static void agentRouterInstallTrayIcon(const unsigned char *bytes, int length) {
  if ([NSThread isMainThread]) {
    agentRouterCreateTrayIcon(bytes, length);
    return;
  }
  dispatch_sync(dispatch_get_main_queue(), ^{
    agentRouterCreateTrayIcon(bytes, length);
  });
}
*/
import "C"

import (
	"context"
	_ "embed"
	"sync"
	"unsafe"
)

//go:embed build/appicon.png
var trayIcon []byte

var installTrayOnce sync.Once

// installTrayIcon adds a persistent macOS menu-bar icon. Its click handler
// restores the primary Wails window after the user has closed (hidden) it.
func installTrayIcon(ctx context.Context) {
	installTrayOnce.Do(func() {
		C.agentRouterInstallTrayIcon(
			(*C.uchar)(unsafe.Pointer(&trayIcon[0])),
			C.int(len(trayIcon)),
		)
	})
}

// hideToTray keeps the proxy and status item running without a Dock icon.
func hideToTray(ctx context.Context) {
	C.agentRouterHideToTray()
}

// quitRequested reports whether the tray "退出" item asked the app to quit.
// OnBeforeClose consults it so a tray quit is not swallowed as a hide.
func quitRequested() bool {
	return C.agentRouterQuitRequested != 0
}
