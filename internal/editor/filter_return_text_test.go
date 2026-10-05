package editor

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ReturnText delivers the filtered bytes in the response instead of writing
// a destination file — the path a client without a save dialog (iOS, which
// shows its own share sheet) uses to let the user pick where the file lands.
func TestFilterReturnText(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.txt")
	if err := os.WriteFile(src, []byte("a@OUTLOOK.COM:pw1\nb@gmail.com:pw2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	body, err := json.Marshal(map[string]any{
		"path": src, "email_types": "@outlook", "return_text": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/editor/filter", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	HandleFilter(rec, req)

	var res map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("bad response %q: %v", rec.Body.String(), err)
	}
	if ok, _ := res["ok"].(bool); !ok {
		t.Fatalf("filter failed: %v", res)
	}
	text, _ := res["text"].(string)
	low := strings.ToLower(text)
	if !strings.Contains(low, "a@outlook.com:pw1") {
		t.Fatalf("text = %q, missing the filtered line", text)
	}
	if strings.Contains(low, "gmail") {
		t.Fatalf("text = %q, non-matching line leaked through", text)
	}
	if dest, _ := res["dest"].(string); dest != "" {
		t.Fatalf("dest = %q, want empty — return_text must not report a written file", dest)
	}
}

// The default path (no return_text) still writes the destination itself.
func TestFilterWritesDestByDefault(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.txt")
	if err := os.WriteFile(src, []byte("a@OUTLOOK.COM:pw1\nb@gmail.com:pw2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := RunFilter(filterReq{Path: src, EmailTypes: "@gmail"}, nil)
	if ok, _ := res["ok"].(bool); !ok {
		t.Fatalf("filter failed: %v", res)
	}
	dest, _ := res["dest"].(string)
	if dest == "" {
		t.Fatalf("no dest: %v", res)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("dest file not written: %v", err)
	}
	if !strings.Contains(strings.ToLower(string(got)), "b@gmail.com:pw2") {
		t.Fatalf("dest file = %q, missing the filtered line", got)
	}
}
