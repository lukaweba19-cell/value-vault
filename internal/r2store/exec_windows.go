//go:build windows

package r2store

import (
	"os/exec"
	"syscall"
)

// hiddenCmd starts a helper process without flashing a console window —
// the caller may be a GUI process (the Flutter window) that has no console
// of its own.
func hiddenCmd(name string, args ...string) *exec.Cmd {
	c := exec.Command(name, args...)
	c.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
	return c
}
