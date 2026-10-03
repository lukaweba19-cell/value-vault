// Command xbox-checker is the SOLO Checker desktop app. This file is only
// wiring: helper.exe (this binary) prepares the native client files and
// follows the Flutter window's lifetime. There is no HTTP server, no
// listener and no port anywhere in this process — the Flutter UI talks to
// the Go engine in-process over dart:ffi.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"xbox-checker/internal/core"
	"xbox-checker/internal/keys"
	"xbox-checker/internal/r2store"
	"xbox-checker/internal/server"
)

// nativeUIExe is the Flutter client that sits next to this executable in a
// native install. It carries the app's public name; this launcher is the
// inconspicuous helper beside it.
const nativeUIExe = "xbox-checker.exe"

// legacyUIExe is the window's pre-1.0.17 name. It is still honoured as a
// fallback so installs that update while offline keep a launchable window.
const legacyUIExe = "solo_checker.exe"

// ensureLauncherName keeps this launcher at its canonical path (helper.exe).
// Installations updated from ≤1.0.16 carry the launcher at
// xbox-checker.exe — which from 1.0.17 on is the *window's* name — so
// running from there would make main() spawn itself. Windows allows
// renaming a running image, so relocate first; the old path is then free
// for the bundle extraction to place the window.
func ensureLauncherName() error {
	self, err := os.Executable()
	if err != nil {
		return nil
	}
	base := filepath.Base(self)
	if strings.EqualFold(base, keys.ExeKey) {
		return nil
	}
	if !strings.EqualFold(base, nativeUIExe) {
		return nil // dev layout (e.g. `go run`) — leave it alone
	}
	dst := filepath.Join(filepath.Dir(self), keys.ExeKey)
	_ = os.Remove(dst) // stale canonical copy; we are the running image
	if err := os.Rename(self, dst); err != nil {
		return fmt.Errorf("could not rename %s to %s: %w", base, keys.ExeKey, err)
	}
	bootLogf("launcher relocated: %s -> %s", base, keys.ExeKey)
	return nil
}

func main() {
	server.HideConsole()

	if err := ensureLauncherName(); err != nil {
		bootLogf("launcher rename failed: %v", err)
		server.ShowErrorDialog("SOLO Checker",
			"The launcher file could not be prepared:\n\n"+err.Error()+
				"\n\nClose every SOLO Checker window and delete "+nativeUIExe+
				", then start "+keys.ExeKey+" — or re-download the release bundle.")
		return
	}

	// Started by the update watcher after the window exited: apply the
	// staged launcher swap and leave — no window, no bootstrap. The swap
	// itself restarts the app through the existing helper script.
	if len(os.Args) > 1 && os.Args[1] == "--finish-update" {
		bootLogf("finish-update pass")
		if msg := r2store.FinishPendingUpdate(); msg != "" {
			bootLogf("update: %s", msg)
		}
		return
	}

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
				"- "+nativeUIExe+"\n- solo.dll\n- data\\flutter_assets\n\n"+
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

	// The update prompt inside the window stages the new launcher and exits;
	// swap it in now that the window is gone, then restart via the helper.
	if msg := r2store.FinishPendingUpdate(); msg != "" {
		bootLogf("update: %s", msg)
	}
}

// nativeUIPath returns the path of the bundled native client, if present.
// The pre-1.0.17 name is accepted as a fallback so an install that updated
// while offline (bundle not fetched yet) still launches.
func nativeUIPath() string {
	self, err := os.Executable()
	if err != nil {
		return ""
	}
	dir := filepath.Dir(self)
	for _, n := range []string{nativeUIExe, legacyUIExe} {
		p := filepath.Join(dir, n)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}
