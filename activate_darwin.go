//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#include <Cocoa/Cocoa.h>

// Must be dispatched to main thread; dispatch_async ensures this even when
// called from a background goroutine (e.g. the file watcher).
void bringAppToFront() {
    dispatch_async(dispatch_get_main_queue(), ^{
        [NSApp setActivationPolicy:NSApplicationActivationPolicyRegular];
        [NSApp activateIgnoringOtherApps:YES];
    });
}

void sendAppToBackground() {
    dispatch_async(dispatch_get_main_queue(), ^{
        [NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
    });
}
*/
import "C"

// macActivate makes the app a regular foreground app so its window is
// fully interactive (keyboard focus, etc.).  Call before window.Show().
func macActivate() {
	C.bringAppToFront()
}

// macDeactivate returns the app to accessory (background/tray-only) mode.
// Call after window.Hide().
func macDeactivate() {
	C.sendAppToBackground()
}
