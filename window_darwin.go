//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>

// Wails creates a translucent window with an NSVisualEffectView behind the
// webview, but leaves the window opaque and the effect view on its default
// material. On modern macOS that default renders as an opaque sheet, so the
// desktop never shows through. Reconfiguring the existing effect view (rather
// than adding a second one) keeps a single blur layer.
//
// NSVisualEffectMaterialUnderWindowBackground is 21 and NSVisualEffectMaterialSidebar
// is 7; cgo cannot reliably resolve the AppKit enum constants, so the value is
// inlined. See tray_darwin.go for the same convention.
#define AR_MATERIAL_UNDER_WINDOW_BACKGROUND 21

static void agentRouterConfigureGlass(void) {
  dispatch_async(dispatch_get_main_queue(), ^{
    NSWindow *window = nil;
    for (NSWindow *candidate in NSApp.windows) {
      if (candidate.canBecomeMainWindow) {
        window = candidate;
        break;
      }
    }
    if (window == nil) return;

    // An opaque window is composited against its own backing, so the vibrancy
    // material has nothing to sample and the blur never appears.
    [window setOpaque:NO];
    // Keep the native window elevation; the web UI separately draws the
    // matching light-grey content frame.
    [window setHasShadow:YES];

    int found = 0;
    for (NSView *view in [[window contentView] subviews]) {
      if (![view isKindOfClass:[NSVisualEffectView class]]) continue;
      NSVisualEffectView *effect = (NSVisualEffectView *)view;
      effect.material = (NSVisualEffectMaterial)AR_MATERIAL_UNDER_WINDOW_BACKGROUND;
      effect.blendingMode = NSVisualEffectBlendingModeBehindWindow;
      effect.state = NSVisualEffectStateActive;
      found = 1;
      break;
    }
    (void)found;
  });
}
*/
import "C"

// configureGlassWindow makes the window a real translucent surface. It is
// idempotent: the first NSVisualEffectView found is reconfigured in place.
func configureGlassWindow() {
	C.agentRouterConfigureGlass()
}
