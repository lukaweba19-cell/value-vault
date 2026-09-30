//go:build windows

package gui

import (
	"runtime"
	"syscall"
	"unsafe"

	webview2 "github.com/jchv/go-webview2"
)

var mainWV webview2.WebView

func OpenAppWindow(u string) bool {
	return OpenAppWindowTitled("Xbox Full Capture + Keyword Inboxer + Promos / Codes & Epic Games / Fortnite Full Capture @solodolosouls", u, 1360, 820)
}

func OpenAppWindowTitled(title, u string, width, height int) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title:  title,
			Width:  uint(width),
			Height: uint(height),
			IconId: 2,
			Center: true,
		},
	})
	if w == nil {
		return false
	}
	defer w.Destroy()
	if title != "Valid Viewer" {
		mainWV = w
	}
	setDarkTitleBar(w.Window())
	w.SetSize(width, height, webview2.HintNone)
	w.Navigate(u)
	w.Run()
	return true
}

var (
	dwmapi                    = syscall.NewLazyDLL("dwmapi.dll")
	procDwmSetWindowAttribute = dwmapi.NewProc("DwmSetWindowAttribute")
)

const (
	dwmwaUseImmersiveDarkMode        = 20
	dwmwaUseImmersiveDarkModePre20H1 = 19
)

// setDarkTitleBar opts the window frame into Windows dark-mode title bars so
// the native chrome matches the dark webview UI. Windows 10 20H1+ uses
// attribute 20; older builds used 19, so fall back if 20 is rejected.
func setDarkTitleBar(hwnd unsafe.Pointer) {
	if hwnd == nil {
		return
	}
	h := uintptr(hwnd)
	if h == 0 {
		return
	}
	on := uint32(1)
	if hr, _, _ := procDwmSetWindowAttribute.Call(h, uintptr(dwmwaUseImmersiveDarkMode), uintptr(unsafe.Pointer(&on)), unsafe.Sizeof(on)); hr != 0 {
		_, _, _ = procDwmSetWindowAttribute.Call(h, uintptr(dwmwaUseImmersiveDarkModePre20H1), uintptr(unsafe.Pointer(&on)), unsafe.Sizeof(on))
	}
}

// HideConsole hides the Windows console window when present.
func HideConsole() {
	k := syscall.NewLazyDLL("kernel32.dll")
	u := syscall.NewLazyDLL("user32.dll")
	hwnd, _, _ := k.NewProc("GetConsoleWindow").Call()
	if hwnd != 0 {
		u.NewProc("ShowWindow").Call(hwnd, 0)
	}
}
