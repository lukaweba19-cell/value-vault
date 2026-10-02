// Route tables shared by the headless exe (HTTP, used when xbox-checker.exe
// runs on its own) and the dart:ffi bridge the Flutter app loads in-process.
package main

import (
	stdhttp "net/http"

	"xbox-checker/internal/editor"
	"xbox-checker/internal/mail"
	"xbox-checker/internal/proxy"
	"xbox-checker/internal/r2store"
	"xbox-checker/internal/server"
	"xbox-checker/internal/viewer"
)

// newFFIMux is the single route table the Flutter app calls in-process over
// dart:ffi — the union of both listeners, minus the self-update endpoints
// (an in-process UI must never replace its own host executable).
func newFFIMux() *stdhttp.ServeMux {
	mux := stdhttp.NewServeMux()
	mux.HandleFunc("/", server.HandleRoot)
	mux.HandleFunc("/api/state", server.HandleState)
	mux.HandleFunc("/api/start", server.HandleStart)
	mux.HandleFunc("/api/sessions", server.HandleSessions)
	mux.HandleFunc("/api/pick", server.HandlePick)
	mux.HandleFunc("/api/pick-save", server.HandlePickSave)
	mux.HandleFunc("/api/pause", server.HandlePause)
	mux.HandleFunc("/api/stop", server.HandleStop)
	mux.HandleFunc("/api/proxy-lists", proxy.HandleLists)
	mux.HandleFunc("/api/proxy-fetch", proxy.HandleFetch)
	mux.HandleFunc("/api/valids", viewer.HandleValidList)
	mux.HandleFunc("/api/accounts", viewer.HandleAccounts)
	mux.HandleFunc("/api/stats", viewer.HandleStats)
	mux.HandleFunc("/api/reload", viewer.HandleReload)
	mux.HandleFunc("/api/events", viewer.HandleEvents)
	mux.HandleFunc("/api/open-link", server.HandleOpenLink)
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
	return mux
}

// newAPIMux is the headless exe's listener: everything the FFI mux serves,
// plus the R2 self-update endpoints (only meaningful for the standalone exe,
// which replaces itself via os.Executable()).
func newAPIMux() *stdhttp.ServeMux {
	mux := newFFIMux()
	mux.HandleFunc("/api/update/check", r2store.HandleCheck)
	mux.HandleFunc("/api/update/apply", r2store.HandleApply)
	return mux
}

// newViewerMux is the second listener's route table (viewer-only extras).
func newViewerMux() *stdhttp.ServeMux {
	mux := stdhttp.NewServeMux()
	mux.HandleFunc("/", server.HandleRoot)
	mux.HandleFunc("/api/valids", viewer.HandleValidList)
	mux.HandleFunc("/api/accounts", viewer.HandleAccounts)
	mux.HandleFunc("/api/stats", viewer.HandleStats)
	mux.HandleFunc("/api/reload", viewer.HandleReload)
	mux.HandleFunc("/api/events", viewer.HandleEvents)
	mux.HandleFunc("/api/mail/list", mail.HandleList)
	mux.HandleFunc("/api/mail/connect", mail.HandleConnect)
	mux.HandleFunc("/api/mail/probe", mail.HandleProbe)
	mux.HandleFunc("/api/mail/delete", mail.HandleDelete)
	mux.HandleFunc("/api/mail/body", mail.HandleBody)
	mux.HandleFunc("/api/mail/view", mail.HandleView)
	mux.HandleFunc("/api/proxy-lists", proxy.HandleLists)
	mux.HandleFunc("/api/proxy-fetch", proxy.HandleFetch)
	mux.HandleFunc("/api/pick", server.HandlePick)
	return mux
}
