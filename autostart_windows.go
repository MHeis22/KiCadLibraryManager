//go:build windows

package main

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
)

// We define the tray icon here for windows to easily swap it
//
//go:embed build/windows/icon.ico
var trayIcon []byte

// ToggleAutoStart creates a pure-go startup batch script for Windows.
func (a *App) ToggleAutoStart(enable bool) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}

	// Fetch %USERPROFILE%\AppData\Roaming
	appData, err := os.UserConfigDir()
	if err != nil {
		return err
	}

	// Target the Windows Startup folder
	startupDir := filepath.Join(appData, "Microsoft", "Windows", "Start Menu", "Programs", "Startup")

	// Ensure the directory exists
	os.MkdirAll(startupDir, os.ModePerm)
	shortcutPath := filepath.Join(startupDir, "KiCadLibMgr.bat")

	if enable {
		// Create a simple batch script to launch the app silently
		content := fmt.Sprintf("@echo off\nstart \"\" \"%s\"", exe)
		err = os.WriteFile(shortcutPath, []byte(content), 0644)
	} else {
		// Remove the script to disable autostart
		err = os.Remove(shortcutPath)
		if os.IsNotExist(err) {
			err = nil // Ignore if it's already removed
		}
	}

	if err == nil {
		conf := LoadConfig()
		conf.AutoStart = enable
		SaveConfig(conf)
		fmt.Println("--> AutoStart set to:", enable)
	}

	return err
}
