package ui

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDashboardServes(t *testing.T) {
	w := httptest.NewRecorder()
	Dashboard(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content type = %q", ct)
	}
	body := w.Body.String()
	if !strings.Contains(body, "/ui/dash/main.mjs") || !strings.Contains(body, `<div id="app"></div>`) {
		t.Fatalf("dashboard shell missing mount/module script: %s", body)
	}
}

func TestViewerServes(t *testing.T) {
	w := httptest.NewRecorder()
	Viewer(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "/ui/viewer/main.mjs") {
		t.Fatalf("viewer shell missing module script")
	}
}

func TestAssetsServed(t *testing.T) {
	cases := []struct {
		path string
		ct   string
	}{
		{"/ui/preact.module.mjs", "text/javascript"},
		{"/ui/hooks.module.mjs", "text/javascript"},
		{"/ui/htm.module.mjs", "text/javascript"},
		{"/ui/app.mjs", "text/javascript"},
		{"/ui/theme.css", "text/css"},
		{"/ui/viewer.css", "text/css"},
		{"/ui/dash/main.mjs", "text/javascript"},
		{"/ui/dash/home.mjs", "text/javascript"},
		{"/ui/dash/setup.mjs", "text/javascript"},
		{"/ui/dash/run.mjs", "text/javascript"},
		{"/ui/dash/editor.mjs", "text/javascript"},
		{"/ui/viewer/main.mjs", "text/javascript"},
		{"/ui/viewer/mail.mjs", "text/javascript"},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		Asset(w, httptest.NewRequest("GET", c.path, nil))
		if w.Code != 200 {
			t.Errorf("%s: status = %d", c.path, w.Code)
			continue
		}
		if got := w.Header().Get("Content-Type"); !strings.HasPrefix(got, c.ct) {
			t.Errorf("%s: content type = %q, want prefix %q", c.path, got, c.ct)
		}
		if w.Body.Len() == 0 {
			t.Errorf("%s: empty body", c.path)
		}
	}
}

func TestAssetTraversalBlocked(t *testing.T) {
	w := httptest.NewRecorder()
	Asset(w, httptest.NewRequest("GET", "/ui/../ui.go", nil))
	if w.Code != 404 {
		t.Fatalf("traversal: status = %d, want 404", w.Code)
	}
}

func TestPreactExportsH(t *testing.T) {
	w := httptest.NewRecorder()
	Asset(w, httptest.NewRequest("GET", "/ui/preact.module.mjs", nil))
	// preact.module.js exports createElement aliased as h (needed by htm binding)
	if !strings.Contains(w.Body.String(), "as h,") {
		t.Fatalf("preact module does not export h")
	}
}
