// Command xbox-checker is the SOLO Checker desktop app. This file is only
// wiring: CLI flags, HTTP routes and window bootstrap live here; all logic
// lives under internal/*.
package main

import (
	"fmt"
	"os"
	"strings"
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
	"xbox-checker/internal/xbox"
)

func main() {
	gui.HideConsole()
	r2store.AfterUpdateCleanup()
	go core.PullQuotaFromCloud()

	if len(os.Args) > 1 && os.Args[1] == "--revoke-tbird" {
		combo := os.Getenv("MAILTEST_COMBO")
		if combo == "" && len(os.Args) > 2 {
			combo = os.Args[2]
		}
		proxyStr := os.Getenv("MAILTEST_PROXY")
		if proxyStr == "" && len(os.Args) > 3 {
			proxyStr = os.Args[3]
		}
		email, pass, _ := strings.Cut(combo, ":")
		if err := mail.RevokeThunderbirdConsent(strings.TrimSpace(email), strings.TrimSpace(pass), strings.TrimSpace(proxyStr)); err != nil {
			fmt.Println("REVOKE_ERR", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--mailtest" {
		combo := os.Getenv("MAILTEST_COMBO")
		if combo == "" && len(os.Args) > 2 {
			combo = os.Args[2]
		}
		email, pass, _ := strings.Cut(combo, ":")
		email, pass = strings.TrimSpace(email), strings.TrimSpace(pass)
		fmt.Println("mailtest login", email)
		msgs, err := mail.ListMailOnce(email, pass, "", 0, 20, "", "inbox")
		if err != nil {
			fmt.Println("MAIL_ERR", err)
		} else {
			fmt.Println("MAIL_OK", len(msgs))
			for i, m := range msgs {
				if i >= 8 {
					break
				}
				fmt.Printf("  - %s | from=%s | preview=%d chars\n", m.Subject, m.From, len(m.Preview))
			}
		}
		kind := xbox.ClassifyAccountType(email, "")
		fmt.Println("ACCOUNT_TYPE", kind)
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--folders-test" {
		combo := os.Getenv("MAILTEST_COMBO")
		if combo == "" && len(os.Args) > 2 {
			combo = os.Args[2]
		}
		email, pass, _ := strings.Cut(combo, ":")
		email, pass = strings.TrimSpace(email), strings.TrimSpace(pass)
		fmt.Println("folders-test", email)
		for _, folder := range []string{"inbox", "sent", "spam", "deleted"} {
			msgs, err := mail.ListMailOnce(email, pass, "", 0, 15, "kind:email", folder)
			if err != nil {
				fmt.Printf("[%s] ERR %v\n", folder, err)
				continue
			}
			fmt.Printf("[%s] count=%d folderName=%s\n", folder, len(msgs), mail.FolderName(folder))
			for i, m := range msgs {
				if i >= 5 {
					break
				}
				fmt.Printf("  - %s | from=%s\n", m.Subject, m.From)
			}
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--viewer" {
		_ = gui.OpenAppWindowTitled("Valid Viewer", "http://127.0.0.1:2719/", 1180, 820)
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

	addr := "127.0.0.1:2718"
	go func() {
		_ = stdhttp.ListenAndServe(addr, mux)
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
		_ = stdhttp.ListenAndServe("127.0.0.1:2719", viewerMux)
	}()

	time.Sleep(200 * time.Millisecond)
	if gui.OpenAppWindow("http://" + addr) {
		return
	}
	gui.OpenBrowser("http://" + addr)
	select {}
}
