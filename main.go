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
	"xbox-checker/internal/editor"
	"xbox-checker/internal/gui"
	"xbox-checker/internal/mail"
	"xbox-checker/internal/proxy"
	"xbox-checker/internal/r2store"
	"xbox-checker/internal/ui"
	"xbox-checker/internal/viewer"
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
	gui.HideConsole()
	r2store.AfterUpdateCleanup()
	go core.PullQuotaFromCloud()

	// Single instance: if the API already answers, another copy of the
	// program is running — never serve twice.
	if tcpUp(apiAddr) {
		return
	}

	mux := stdhttp.NewServeMux()
	mux.HandleFunc("/", gui.HandleDashboard)
	mux.HandleFunc("/api/state", gui.HandleState)
	mux.HandleFunc("/api/start", gui.HandleStart)
	mux.HandleFunc("/api/sessions", gui.HandleSessions)
	mux.HandleFunc("/api/pick", gui.HandlePick)
	mux.HandleFunc("/api/pick-save", gui.HandlePickSave)
	mux.HandleFunc("/api/pause", gui.HandlePause)
	mux.HandleFunc("/api/stop", gui.HandleStop)
	mux.HandleFunc("/api/proxy-lists", proxy.HandleLists)
	mux.HandleFunc("/api/proxy-fetch", proxy.HandleFetch)
	mux.HandleFunc("/viewer", gui.HandleViewerPage)
	mux.HandleFunc("/api/valids", viewer.HandleValidList)
	mux.HandleFunc("/api/open-viewer", gui.HandleOpenViewer)
	mux.HandleFunc("/api/open-link", gui.HandleOpenLink)
	mux.HandleFunc("/api/editor/analyze", editor.HandleAnalyze)
	mux.HandleFunc("/api/editor/analyze-stream", editor.HandleAnalyzeStream)
	mux.HandleFunc("/api/editor/extract", editor.HandleExtract)
	mux.HandleFunc("/api/editor/generate", editor.HandleGenerate)
	mux.HandleFunc("/api/editor/save", editor.HandleSave)
	mux.HandleFunc("/api/editor/filter", editor.HandleFilter)
	mux.HandleFunc("/api/editor/filter-stream", editor.HandleFilterStream)
	mux.HandleFunc("/api/editor/banners", editor.HandleBanners)
	mux.HandleFunc("/api/mail/list", mail.HandleList)
	mux.HandleFunc("/api/mail/connect", mail.HandleConnect)
	mux.HandleFunc("/api/mail/probe", mail.HandleProbe)
	mux.HandleFunc("/api/mail/delete", mail.HandleDelete)
	mux.HandleFunc("/api/mail/body", mail.HandleBody)
	mux.HandleFunc("/api/mail/view", mail.HandleView)
	mux.HandleFunc("/api/update/check", r2store.HandleCheck)
	mux.HandleFunc("/api/update/apply", r2store.HandleApply)
	mux.HandleFunc("/ui/", ui.Asset)

	go func() {
		_ = stdhttp.ListenAndServe(apiAddr, mux)
	}()

	viewerMux := stdhttp.NewServeMux()
	viewerMux.HandleFunc("/", gui.HandleViewerPage)
	viewerMux.HandleFunc("/viewer", gui.HandleViewerPage)
	viewerMux.HandleFunc("/api/valids", viewer.HandleValidList)
	viewerMux.HandleFunc("/api/accounts", viewer.HandleAccounts)
	viewerMux.HandleFunc("/api/stats", viewer.HandleStats)
	viewerMux.HandleFunc("/api/reload", viewer.HandleReload)
	viewerMux.HandleFunc("/api/events", viewer.HandleEvents)
	viewerMux.HandleFunc("/api/mail/list", mail.HandleList)
	viewerMux.HandleFunc("/api/mail/connect", mail.HandleConnect)
	viewerMux.HandleFunc("/api/mail/probe", mail.HandleProbe)
	viewerMux.HandleFunc("/api/mail/delete", mail.HandleDelete)
	viewerMux.HandleFunc("/api/mail/body", mail.HandleBody)
	viewerMux.HandleFunc("/api/mail/view", mail.HandleView)
	viewerMux.HandleFunc("/api/proxy-lists", proxy.HandleLists)
	viewerMux.HandleFunc("/api/proxy-fetch", proxy.HandleFetch)
	viewerMux.HandleFunc("/api/pick", gui.HandlePick)
	viewerMux.HandleFunc("/ui/", ui.Asset)

	go func() {
		_ = stdhttp.ListenAndServe(viewerAddr, viewerMux)
	}()

	time.Sleep(200 * time.Millisecond)

	// Window bootstrap: the native client is the real UI when it is bundled
	// next to this exe — launch it and follow its lifetime. Without it, the
	// built-in webview window (or the system browser) is the UI.
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
	if gui.OpenAppWindow("http://" + apiAddr) {
		return
	}
	gui.OpenBrowser("http://" + apiAddr)
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
