// Command xbox-checker is the SOLO Checker desktop app. This file is only
// wiring: the launcher prepares the native client files and follows the
// Flutter window's lifetime. There is no HTTP server, no listener and no
// port anywhere in this process — the Flutter UI talks to the Go engine
// in-process over dart:ffi.
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"xbox-checker/internal/core"
	"xbox-checker/internal/r2store"
	"xbox-checker/internal/server"
)

// nativeUIExe is the Flutter client that sits next to this executable in a
// native install.
const nativeUIExe = "solo_checker.exe"

func main() {
	server.HideConsole()
	bootLogf("launch app=%s", r2store.AppVersion)
	r2store.AfterUpdateCleanup()
	go core.PullQuotaFromCloud()

	// Old single-exe installs restart here after their self-update: pull the
	// native client files from R2 once if they are missing beside this exe.
	ensureClientBundle()

	// Never fall through to a headless process with no window: if the UI
	// files are not complete after bootstrap, say so visibly and exit.
	if !clientFilesReady() {
		bootLogf("client files incomplete after bootstrap — aborting")
		server.ShowErrorDialog("SOLO Checker",
			"The SOLO Checker window files could not be prepared:\n\n"+
				"- "+nativeUIExe+"\n- xbox_checker.dll\n- data\\flutter_assets\n\n"+
				"Re-download the release bundle, then check solo-launch.log next to the exe for details.")
		return
	}

	time.Sleep(200 * time.Millisecond)

	// The Flutter client is the only UI: launch it and follow its lifetime.
	// Single-instance handling lives inside the Flutter app (OS mutex), so
	// there is no port probing here either.
	exe := nativeUIPath()
	if exe == "" {
		bootLogf("clientFilesReady passed but %s vanished — aborting", nativeUIExe)
		server.ShowErrorDialog("SOLO Checker", nativeUIExe+" is missing from the install folder.")
		return
	}
	cmd := exec.Command(exe)
	cmd.Dir = filepath.Dir(exe)
	if err := cmd.Start(); err != nil {
		bootLogf("start %s: %v", exe, err)
		server.ShowErrorDialog("SOLO Checker",
			"Could not start the SOLO Checker window:\n\n"+err.Error())
		return
	}
	bootLogf("started %s pid=%d", exe, cmd.Process.Pid)
	_, _ = cmd.Process.Wait()
	if cmd.ProcessState != nil {
		bootLogf("window exited with code %d", cmd.ProcessState.ExitCode())
	}
}

// nativeUIPath returns the path of the bundled native client, if present.
func nativeUIPath() string {
	self, err := os.Executable()
	if err != nil {
		return ""
	}
	p := filepath.Join(filepath.Dir(self), nativeUIExe)
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}
