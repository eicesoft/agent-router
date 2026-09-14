//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>

static NSWindow *agentRouterMainWindow;

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
  agentRouterStatusItem = [[[NSStatusBar systemStatusBar]
    statusItemWithLength:44.0] retain];
  agentRouterTrayTarget = [AgentRouterTrayTarget new];

  NSButton *button = agentRouterStatusItem.button;
  button.target = agentRouterTrayTarget;
  button.action = @selector(showWindow:);
  button.toolTip = @"Agent Router — 点击显示窗口";
  // Use the same custom mark as the app icon without the legacy "AR" label.
  // The colored image is intentionally not treated as a monochrome template.
  button.title = @"";

  NSData *data = [NSData dataWithBytes:bytes length:length];
  NSImage *image = [[NSImage alloc] initWithData:data];
  image.size = NSMakeSize(18, 18);
  image.template = NO;
  button.image = image;
  button.imageScaling = NSImageScaleProportionallyDown;
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
	_ "embed"
	"sync"
	"unsafe"
)

//go:embed build/appicon.png
var trayIcon []byte

var installTrayOnce sync.Once

// installTrayIcon adds a persistent macOS menu-bar icon. Its click handler
// restores the primary Wails window after the user has closed (hidden) it.
func installTrayIcon() {
	installTrayOnce.Do(func() {
		C.agentRouterInstallTrayIcon(
			(*C.uchar)(unsafe.Pointer(&trayIcon[0])),
			C.int(len(trayIcon)),
		)
	})
}

// hideToTray keeps the proxy and status item running without a Dock icon.
func hideToTray() {
	C.agentRouterHideToTray()
}
