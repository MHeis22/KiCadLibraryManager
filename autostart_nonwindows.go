//go:build !windows

package main

import (
	"fmt"
)

// Define the standard tray icon for Mac/Linux.
// Note: The //go:embed directive was removed because build/appicon.png
// is currently missing. Add it back once you place your icon file.
var trayIcon []byte

// ToggleAutoStart provides a dummy implementation for Mac/Linux.
// Because we defined ToggleAutoStart in autostart_windows.go, we must provide
// a stub here so that Wails can generate the frontend bindings without failing.
func (a *App) ToggleAutoStart(enable bool) error {
	fmt.Println("--> AutoStart toggle is currently only implemented for Windows")
	return nil
}
