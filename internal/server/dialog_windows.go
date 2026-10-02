//go:build windows

package server

import (
	"syscall"
	"unsafe"
)

// ShowErrorDialog shows a blocking error message box. The launcher runs with
// its console hidden, so this is the only visible channel for fatal startup
// problems (missing UI files, spawn failures).
func ShowErrorDialog(title, msg string) {
	u := syscall.NewLazyDLL("user32.dll")
	t, _ := syscall.UTF16PtrFromString(title)
	m, _ := syscall.UTF16PtrFromString(msg)
	const mbOk, mbIconErr = 0x00000000, 0x00000010
	_, _, _ = u.NewProc("MessageBoxW").Call(
		0,
		uintptr(unsafe.Pointer(m)),
		uintptr(unsafe.Pointer(t)),
		mbOk|mbIconErr,
	)
}
