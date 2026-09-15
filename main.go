package main

import (
	"context"
	"embed"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app := NewApp()
	if err := wails.Run(&options.App{
		Title:       "Agent Router",
		Width:       1440,
		Height:      900,
		AssetServer: &assetserver.Options{Assets: assets},
		// Fully transparent window background so the OS blur/vibrancy layer
		// shows through; the UI paints its own translucent surface.
		BackgroundColour: &options.RGBA{R: 10, G: 15, B: 25, A: 0},
		Mac: &mac.Options{
			// FullSizeContent makes the webview cover the title bar area, and
			// TitlebarAppearsTransparent drops its opaque fill, so the glass
			// surface reaches the very top edge instead of stopping under a
			// white native title bar. The traffic lights stay visible on top.
			TitleBar: &mac.TitleBar{
				TitlebarAppearsTransparent: true,
				HideTitle:                  true,
				FullSizeContent:            true,
				HideToolbarSeparator:       true,
			},
			WindowIsTranslucent:  true,
			WebviewIsTransparent: true,
		},
		Windows: &windows.Options{
			WindowIsTranslucent: true,
			BackdropType:        windows.Acrylic,
		},
		OnStartup: func(ctx context.Context) {
			// Wails has already created the window by the time OnStartup runs,
			// so the vibrancy layer can be reconfigured before the UI paints.
			configureGlassWindow()
			app.Startup(ctx)
		},
		// OnStartup runs before Wails enters the native macOS application loop.
		// Install the status item after the webview is ready so NSApplication has
		// finished launching and owns a live menu bar.
		OnDomReady: func(ctx context.Context) {
			installTrayIcon(ctx)
		},
		// Keep the proxy and menu-bar item alive while hiding the window and Dock icon.
		// A real quit requested from the tray "退出" item (or Cmd+Q) is allowed to
		// proceed so the app actually terminates instead of hiding to tray forever.
		OnBeforeClose: func(ctx context.Context) bool {
			if quitRequested() {
				return false
			}
			hideToTray(ctx)
			return true
		},
		OnShutdown: app.Shutdown,
		Bind:       []interface{}{app},
	}); err != nil {
		log.Fatal(err)
	}
}
