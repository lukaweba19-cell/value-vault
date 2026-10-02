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
	r2store.AfterUpdateCleanup()
	go core.PullQuotaFromCloud()

	// Single instance: if the API already answers, another copy of the
	// program is running — never serve twice.
	if tcpUp(apiAddr) {
		return
	}

	mux := newAPIMux()

	go func() {
		_ = stdhttp.ListenAndServe(apiAddr, mux)
	}()

	viewerMux := newViewerMux()

	go func() {
		_ = stdhttp.ListenAndServe(viewerAddr, viewerMux)
	}()

	// Old single-exe installs restart here after their self-update: pull the
	// native client files from R2 once if they are missing beside this exe.
	ensureClientBundle()

	time.Sleep(200 * time.Millisecond)

	// Bootstrap: the Flutter client is the only UI. Launch it when it is
	// bundled next to this exe and follow its lifetime; otherwise keep
	// serving the API so a client can connect later.
	if exe := nativeUIPath(); exe != "" {
		cmd := exec.Command(exe)
		cmd.Dir = filepath.Dir(exe)
		if err := cmd.Start(); err == nil {
			_, _ = cmd.Process.Wait()
			// If the native window we started handed off to another instance
			// (single-instance lock), keep serving until that one closes.
			for tcpUp(nativeUILock) {
				time.Sleep(2 * time.Second)
			}
			return
		}
	}
	select {}
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
