// Persistent launcher diagnostics.
//
// The console is hidden on Windows, so every decision the launcher makes is
// appended to solo-launch.log beside the exe (falling back to the temp dir
// when the install directory is not writable). When the app "does nothing",
// that file says exactly which branch it took.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

var launchLogFile string

func bootLogf(format string, args ...any) {
	msg := fmt.Sprintf("%s %s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, args...))
	if launchLogFile == "" {
		launchLogFile = resolveLaunchLog()
	}
	f, err := os.OpenFile(launchLogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err == nil {
		_, _ = f.WriteString(msg)
		_ = f.Close()
	}
	_, _ = os.Stderr.WriteString(msg)
}

func resolveLaunchLog() string {
	if dir := exeDir(); dir != "" {
		p := filepath.Join(dir, "solo-launch.log")
		if f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			_ = f.Close()
			return p
		}
	}
	return filepath.Join(os.TempDir(), "solo-launch.log")
}
