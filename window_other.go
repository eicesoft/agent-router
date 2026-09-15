//go:build !darwin

package main

// configureGlassWindow is a no-op off macOS. Windows gets its blur from the
// Wails BackdropType option instead, and other platforms have no equivalent
// native vibrancy layer to configure.
func configureGlassWindow() {}
