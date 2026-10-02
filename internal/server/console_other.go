//go:build !windows

package server

// HideConsole is a no-op off-Windows.
func HideConsole() {}
