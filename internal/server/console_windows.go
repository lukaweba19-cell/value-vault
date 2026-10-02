//go:build windows

package server

import "syscall"

// HideConsole hides the Windows console window when present.
func HideConsole() {
	k := syscall.NewLazyDLL("kernel32.dll")
	u := syscall.NewLazyDLL("user32.dll")
	hwnd, _, _ := k.NewProc("GetConsoleWindow").Call()
	if hwnd != 0 {
		u.NewProc("ShowWindow").Call(hwnd, 0)
	}
}
