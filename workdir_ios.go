//go:build ios

package main

import (
	"os"
	"path/filepath"
)

// soloInitWorkDir points the process CWD somewhere writable before the
// engine touches disk.
//
// On iOS an app starts with CWD at the bundle root ("/"), which is
// read-only: every relative path the engine uses (sessions/, results/,
// mail tokens, proxy lists, solo-launch.log fallbacks) would fail with
// EROFS/EACCES. HOME is the per-app sandbox, so Documents/ is the right
// durable place for all of it — the same files the Windows build keeps
// beside its exe.
func soloInitWorkDir() {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		bootLogf("ios workdir: no home dir (%v) — keeping CWD", err)
		return
	}
	dir := filepath.Join(home, "Documents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		bootLogf("ios workdir: mkdir %s: %v — keeping CWD", dir, err)
		return
	}
	if wd, err := os.Getwd(); err == nil && wd == dir {
		return
	}
	if err := os.Chdir(dir); err != nil {
		bootLogf("ios workdir: chdir %s: %v", dir, err)
		return
	}
	bootLogf("ios workdir: CWD -> %s", dir)
}
