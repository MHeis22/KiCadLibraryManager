//go:build !darwin

package main

import "os"

// kicadConfigDir returns the base directory where KiCad stores its config.
// On Windows and Linux, os.UserConfigDir() is correct.
func kicadConfigDir() string {
	dir, _ := os.UserConfigDir()
	return dir
}
