#!/usr/bin/env python3
"""Check tray lifetime and close/reopen behavior in a macOS event loop.

Requires macOS, Xcode command-line tools, and a logged-in GUI session.
Run: python3 scripts/check-tray-lifetime.py
"""
import pathlib
import subprocess
import sys
import tempfile

if sys.platform != "darwin":
    raise SystemExit("This check requires macOS and a logged-in GUI session.")

root = pathlib.Path(__file__).resolve().parents[1]
source = (root / "tray_darwin.go").read_text().split("/*", 1)[1].split("*/", 1)[0]
source = "\n".join(line for line in source.splitlines() if not line.startswith("#cgo"))
source += r'''
#import <objc/runtime.h>
static BOOL released = NO;
@interface TrayLifetimeProbe : NSObject
@end
@implementation TrayLifetimeProbe
- (void)dealloc { released = YES; [super dealloc]; }
@end
int main() {
  [NSApplication sharedApplication];
  dispatch_async(dispatch_get_main_queue(), ^{
    [NSApp setActivationPolicy:NSApplicationActivationPolicyRegular];
    NSWindow *window = [[NSWindow alloc] initWithContentRect:NSMakeRect(0, 0, 200, 100)
      styleMask:NSWindowStyleMaskTitled backing:NSBackingStoreBuffered defer:NO];
    [window makeKeyAndOrderFront:nil];
    agentRouterCreateTrayIcon(NULL, 0);
    TrayLifetimeProbe *probe = [TrayLifetimeProbe new];
    objc_setAssociatedObject(agentRouterStatusItem, "probe", probe, OBJC_ASSOCIATION_RETAIN_NONATOMIC);
    [probe release];
    dispatch_after(dispatch_time(DISPATCH_TIME_NOW, NSEC_PER_SEC), dispatch_get_main_queue(), ^{
      printf("status item survives application event loop: %s\n", released ? "FAIL" : "PASS");
      if (released) exit(1);
      if (agentRouterMainWindow != window) exit(2);
      agentRouterHideToTray();
      dispatch_after(dispatch_time(DISPATCH_TIME_NOW, NSEC_PER_SEC), dispatch_get_main_queue(), ^{
        BOOL hidden = !window.visible && NSApp.activationPolicy == NSApplicationActivationPolicyAccessory
          && agentRouterStatusItem.visible;
        printf("close hides window and Dock, preserves tray: %s\n", hidden ? "PASS" : "FAIL");
        if (!hidden) exit(3);
        [agentRouterTrayTarget showWindow:nil];
        dispatch_after(dispatch_time(DISPATCH_TIME_NOW, NSEC_PER_SEC), dispatch_get_main_queue(), ^{
          BOOL restored = window.visible && NSApp.activationPolicy == NSApplicationActivationPolicyRegular;
          printf("tray restores window and Dock: %s\n", restored ? "PASS" : "FAIL");
          exit(restored ? 0 : 4);
        });
      });
    });
  });
  [NSApp run];
}
'''
with tempfile.TemporaryDirectory(prefix="agent-router-tray-") as directory:
    source_path = pathlib.Path(directory) / "probe.m"
    binary = pathlib.Path(directory) / "probe"
    source_path.write_text(source)
    subprocess.run(["clang", "-fblocks", "-framework", "Cocoa", str(source_path), "-o", str(binary)], check=True)
    result = subprocess.run([str(binary)], timeout=10)
    raise SystemExit(result.returncode)
