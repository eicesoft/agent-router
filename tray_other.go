//go:build !darwin && !windows

package main

import "context"

// installTrayIcon, hideToTray and quitRequested are provided per platform:
// tray_darwin.go and tray_windows.go own the real tray implementations. Other
// operating systems get no-op stubs so main.go builds everywhere.
func installTrayIcon(ctx context.Context) {}

func hideToTray(ctx context.Context) {}

func quitRequested() bool { return false }
