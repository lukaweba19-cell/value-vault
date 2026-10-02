// Command xbox-checker is the SOLO Checker desktop app. This file is only
// wiring: HTTP routes and window bootstrap live here; all logic lives under
// internal/*.
package main

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	stdhttp "net/http"

	"xbox-checker/internal/core"
	"xbox-checker/internal/r2store"
	"xbox-checker/internal/server"
)

// nativeUIExe is the Flutter client that sits next to this executable in a
// native install. nativeUILock is the localhost port the native client holds
// for as long as its window is alive; the backend follows that lifetime.
const (
	apiAddr      = "127.0.0.1:2718"
	viewerAddr   = "127.0.0.1:2719"
	nativeUILock = "127.0.0.1:27180"
	nativeUIExe  = "solo_checker.exe"
)

func main() {
	server.HideConsole()
	bootLogf("launch app=%s", r2store.AppVersion)
	r2store.AfterUpdateCleanup()
	go core.PullQuotaFromCloud()

	// Another process may already answer 2718 — an earlier copy of this
	// exe, or an old client that has not updated yet. The Flutter UI talks
	// to its engine in-process over dart:ffi, so the port is irrelevant to
	// it: keep going. The old behaviour (exit silently when the port was
	// busy) is what made launches look like they "instantly closed".
	if tcpUp(apiAddr) {
		bootLogf("%s already served by another copy — continuing", apiAddr)
	}

	// Listeners are best-effort: when the port is taken by someone else the
	// FFI-backed UI still works without them.
	go func() {
		_ = stdhttp.ListenAndServe(apiAddr, newAPIMux())
	}()
	go func() {
		_ = stdhttp.ListenAndServe(viewerAddr, newViewerMux())
	}()

	// Old single-exe installs restart here after their self-update: pull the
	// native client files from R2 once if they are missing beside this exe.
	ensureClientBundle()

	// Never fall through to a headless process with no window: if the UI
	// files are not complete after bootstrap, say so visibly and exit so
	// the port is not squatted without an interface behind it.
	if !clientFilesReady() {
		bootLogf("client files incomplete after bootstrap — aborting")
		server.ShowErrorDialog("SOLO Checker",
			"The SOLO Checker window files could not be prepared:\n\n"+
				"- "+nativeUIExe+"\n- xbox_checker.dll\n- data\\flutter_assets\n\n"+
				"Re-download the release bundle, then check solo-launch.log next to the exe for details.")
		return
	}

	time.Sleep(200 * time.Millisecond)

	// Bootstrap: the Flutter client is the only UI. Launch it and follow
	// its lifetime; every failure path reports visibly instead of exiting
	// silently.
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
	// If the native window we started handed off to another instance
	// (single-instance lock), keep serving until that one closes.
	for tcpUp(nativeUILock) {
		time.Sleep(2 * time.Second)
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

func tcpUp(addr string) bool {
	c, err := net.DialTimeout("tcp", addr, 400*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}
