// Package ui serves the Preact-based interface for the checker dashboard
// and the Valid Viewer window. Assets are embedded at build time.
package ui

import (
	"embed"
	"io"
	"net/http"
	"path"
	"strings"
)

//go:embed assets
var assets embed.FS

var contentTypes = map[string]string{
	".html": "text/html; charset=utf-8",
	".css":  "text/css; charset=utf-8",
	".mjs":  "text/javascript; charset=utf-8",
	".svg":  "image/svg+xml",
	".png":  "image/png",
	".ico":  "image/x-icon",
}

func asset(name string) (string, []byte, bool) {
	b, err := assets.ReadFile("assets/" + name)
	if err != nil {
		return "", nil, false
	}
	ct := contentTypes[path.Ext(name)]
	if ct == "" {
		ct = "application/octet-stream"
	}
	return ct, b, true
}

func serve(w http.ResponseWriter, name string) {
	ct, b, ok := asset(name)
	if !ok {
		http.NotFound(w, nil)
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Length", itoa(len(b)))
	_, _ = io.CopyN(w, strings.NewReader(string(b)), int64(len(b)))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// Dashboard serves the main checker UI shell.
func Dashboard(w http.ResponseWriter, r *http.Request) {
	serve(w, "dashboard.html")
}

// Viewer serves the Valid Viewer UI shell.
func Viewer(w http.ResponseWriter, r *http.Request) {
	serve(w, "viewer.html")
}

// Asset serves embedded modules and styles, e.g. /ui/preact.module.mjs.
func Asset(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	name = strings.TrimPrefix(name, "ui/")
	if name == "" || strings.Contains(name, "..") {
		http.NotFound(w, nil)
		return
	}
	serve(w, name)
}
