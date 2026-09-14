package main

import (
	"context"
	"embed"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app := NewApp()
	if err := wails.Run(&options.App{
		Title:            "Agent Router",
		Width:            1440,
		Height:           900,
		AssetServer:      &assetserver.Options{Assets: assets},
		BackgroundColour: &options.RGBA{R: 10, G: 15, B: 25, A: 1},
		OnStartup: func(ctx context.Context) {
			app.Startup(ctx)
		},
		// OnStartup runs before Wails enters the native macOS application loop.
		// Install the status item after the webview is ready so NSApplication has
		// finished launching and owns a live menu bar.
		OnDomReady: func(ctx context.Context) {
			installTrayIcon()
		},
		// Keep the proxy and menu-bar item alive while hiding the window and Dock icon.
		OnBeforeClose: func(ctx context.Context) bool {
			hideToTray()
			return true
		},
		OnShutdown: app.Shutdown,
		Bind:       []interface{}{app},
	}); err != nil {
		log.Fatal(err)
	}
}
