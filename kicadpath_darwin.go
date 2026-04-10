//go:build darwin

package main

import (
	"os"
	"path/filepath"
)

// kicadConfigDir returns the base directory where KiCad stores its config on macOS.
// KiCad uses ~/Library/Preferences, not ~/Library/Application Support
// (which is what os.UserConfigDir() returns on macOS).
// For KiCad 7+ which may use Application Support, we check both.
func kicadConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		configDir, _ := os.UserConfigDir()
		return configDir
	}

	prefsPath := filepath.Join(home, "Library", "Preferences")
	if _, err := os.Stat(filepath.Join(prefsPath, "kicad")); err == nil {
		return prefsPath
	}

	appSupportPath := filepath.Join(home, "Library", "Application Support")
	if _, err := os.Stat(filepath.Join(appSupportPath, "kicad")); err == nil {
		return appSupportPath
	}

	// Neither exists yet — default to Preferences (KiCad's historical macOS location)
	return prefsPath
}
