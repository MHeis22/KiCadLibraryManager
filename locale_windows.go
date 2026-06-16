//go:build windows

package main

import (
	"os/exec"
	"strings"
	"syscall"
)

func osAffectedLocaleWindows() (bool, string) {
	cmd := exec.Command("reg", "query", "HKCU\\Control Panel\\International", "/v", "LocaleName")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err == nil {
		val := strings.ToLower(string(out))
		if affected, name := getKiCadTranslatedName(extractLocaleFromReg(val)); affected {
			return true, name
		}
	}
	return false, "KiCad"
}
