//go:build !windows

package gui

// OpenAppWindowTitled is unavailable off-Windows; callers fall back to a browser.
func OpenAppWindowTitled(title, u string, width, height int) bool { return false }

// HideConsole is a no-op off-Windows.
func HideConsole() {}
