//go:build !darwin || !cgo

package main

// macActivate and macDeactivate are no-ops on non-macOS platforms.
func macActivate()   {}
func macDeactivate() {}
