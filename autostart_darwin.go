//go:build darwin
// +build darwin

package main

import _ "embed"

//go:embed build/appicon.png
var trayIcon []byte
