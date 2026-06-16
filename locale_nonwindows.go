//go:build !windows

package main

func osAffectedLocaleWindows() (bool, string) {
	return false, "KiCad"
}
