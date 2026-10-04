//go:build !ios

package main

// soloInitWorkDir is a no-op outside iOS: the desktop builds already run
// with a writable working directory (the install dir next to the exe, or
// the checkout when run from a terminal), and changing it would break the
// paths existing Windows installs rely on.
func soloInitWorkDir() {}
