// Package r2store implements the minimal signed Cloudflare R2 client
// (get/put), the update manifest handling and the self-update flow.
package r2store

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	stdhttp "net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"xbox-checker/internal/keys"
)

const AppVersion = "1.0.15"

func SignGet(key string) (*stdhttp.Request, error) {
	key = strings.TrimPrefix(key, "/")
	path := "/" + keys.Bucket + "/" + strings.ReplaceAll(key, " ", "%20")
	now := time.Now().UTC()
	amz := now.Format("20060102T150405Z")
	dat := now.Format("20060102")
	payload := "UNSIGNED-PAYLOAD"
	canonical := "GET\n" + path + "\n\nhost:" + keys.EndpointHost + "\nx-amz-content-sha256:" + payload + "\nx-amz-date:" + amz + "\n\nhost;x-amz-content-sha256;x-amz-date\n" + payload
	scope := dat + "/auto/s3/aws4_request"
	sts := "AWS4-HMAC-SHA256\n" + amz + "\n" + scope + "\n" + fmt.Sprintf("%x", sha256.Sum256([]byte(canonical)))
	mac := func(k, m []byte) []byte {
		h := hmac.New(sha256.New, k)
		h.Write(m)
		return h.Sum(nil)
	}
	k := mac(mac(mac(mac([]byte("AWS4"+keys.SecretKey), []byte(dat)), []byte("auto")), []byte("s3")), []byte("aws4_request"))
	sig := fmt.Sprintf("%x", mac(k, []byte(sts)))
	auth := "AWS4-HMAC-SHA256 Credential=" + keys.AccessKey + "/" + scope + ", SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature=" + sig
	req, err := stdhttp.NewRequest("GET", "https://"+keys.EndpointHost+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-amz-date", amz)
	req.Header.Set("x-amz-content-sha256", payload)
	req.Header.Set("Authorization", auth)
	return req, nil
}

func Get(key string) ([]byte, error) {
	req, err := SignGet(key)
	if err != nil {
		return nil, err
	}
	cli := &stdhttp.Client{Timeout: 3 * time.Minute}
	resp, err := cli.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 180<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("r2 %s: %s %s", key, resp.Status, strings.TrimSpace(string(b)))
	}
	return b, nil
}

func Put(key string, body []byte, contentType string) error {
	key = strings.TrimPrefix(key, "/")
	path := "/" + keys.Bucket + "/" + strings.ReplaceAll(key, " ", "%20")
	now := time.Now().UTC()
	amz := now.Format("20060102T150405Z")
	dat := now.Format("20060102")
	payload := fmt.Sprintf("%x", sha256.Sum256(body))
	canonical := "PUT\n" + path + "\n\nhost:" + keys.EndpointHost + "\nx-amz-content-sha256:" + payload + "\nx-amz-date:" + amz + "\n\nhost;x-amz-content-sha256;x-amz-date\n" + payload
	scope := dat + "/auto/s3/aws4_request"
	sts := "AWS4-HMAC-SHA256\n" + amz + "\n" + scope + "\n" + fmt.Sprintf("%x", sha256.Sum256([]byte(canonical)))
	mac := func(k, m []byte) []byte {
		h := hmac.New(sha256.New, k)
		h.Write(m)
		return h.Sum(nil)
	}
	k := mac(mac(mac(mac([]byte("AWS4"+keys.WriteSecretKey), []byte(dat)), []byte("auto")), []byte("s3")), []byte("aws4_request"))
	sig := fmt.Sprintf("%x", mac(k, []byte(sts)))
	auth := "AWS4-HMAC-SHA256 Credential=" + keys.WriteAccessKey + "/" + scope + ", SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature=" + sig
	req, err := stdhttp.NewRequest("PUT", "https://"+keys.EndpointHost+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("x-amz-date", amz)
	req.Header.Set("x-amz-content-sha256", payload)
	req.Header.Set("Authorization", auth)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	req.Header.Set("Content-Type", contentType)
	cli := &stdhttp.Client{Timeout: 45 * time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("r2 put %s: %s %s", key, resp.Status, strings.TrimSpace(string(b)))
	}
	return nil
}

// UpdateManifest is the JSON document stored in R2 that describes the
// latest released build.
type UpdateManifest struct {
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
	Key     string `json:"key"`
	Notes   string `json:"notes"`
	URL     string `json:"url"`
	// Optional sidecar objects for installs that only carry the exe: the
	// native client DLL and the full Flutter bundle, fetched once on first
	// boot after a self-update (bootstrap.go). Older clients ignore these.
	DLL       string `json:"dll,omitempty"`
	DLLSHA    string `json:"dll_sha256,omitempty"`
	Bundle    string `json:"bundle,omitempty"`
	BundleSHA string `json:"bundle_sha256,omitempty"`
}

func FetchUpdateManifest() (UpdateManifest, error) {
	var m UpdateManifest
	b, err := Get(keys.ManifestKey)
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, err
	}
	m.Version = strings.TrimSpace(m.Version)
	m.SHA256 = strings.ToLower(strings.TrimSpace(m.SHA256))
	m.DLLSHA = strings.ToLower(strings.TrimSpace(m.DLLSHA))
	m.BundleSHA = strings.ToLower(strings.TrimSpace(m.BundleSHA))
	if m.Key == "" {
		m.Key = keys.ExeKey
	}
	if m.DLL == "" {
		m.DLL = keys.DllKey
	}
	if m.Bundle == "" {
		m.Bundle = keys.BundleKey
	}
	return m, nil
}

func versionParts(v string) []int {
	v = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(v)), "v")
	var out []int
	for _, p := range strings.Split(v, ".") {
		n := 0
		fmt.Sscanf(p, "%d", &n)
		out = append(out, n)
	}
	return out
}

func VersionNewer(remote, local string) bool {
	r, l := versionParts(remote), versionParts(local)
	n := len(r)
	if len(l) > n {
		n = len(l)
	}
	for i := 0; i < n; i++ {
		rv, lv := 0, 0
		if i < len(r) {
			rv = r[i]
		}
		if i < len(l) {
			lv = l[i]
		}
		if rv > lv {
			return true
		}
		if rv < lv {
			return false
		}
	}
	return false
}

func HandleCheck(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	w.Header().Set("Content-Type", "application/json")
	m, err := FetchUpdateManifest()
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error(), "local": AppVersion})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":     true,
		"update": VersionNewer(m.Version, AppVersion),
		"local":  AppVersion,
		"remote": m.Version,
		"sha256": m.SHA256,
	})
}

func HandleApply(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	w.Header().Set("Content-Type", "application/json")
	m, err := FetchUpdateManifest()
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if !VersionNewer(m.Version, AppVersion) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "already on " + AppVersion})
		return
	}
	raw, err := Get(m.Key)
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sum := sha256.Sum256(raw)
	got := hex.EncodeToString(sum[:])
	if m.SHA256 != "" && got != m.SHA256 {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "sha256 mismatch got " + got})
		return
	}
	exe, err := os.Executable()
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
		return
	}
	tmp := filepath.Join(os.TempDir(), "xbox-checker-new.exe")
	if err := os.WriteFile(tmp, raw, 0755); err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
		return
	}
	old := exe + ".old"
	_ = os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "could not move old exe: " + err.Error()})
		return
	}
	if err := copyFile(tmp, exe); err != nil {
		_ = os.Rename(old, exe)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "could not write new exe: " + err.Error()})
		return
	}
	if err := startDetached(exe); err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "could not start new exe: " + err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "restarting": true, "version": m.Version})
	go func() {
		time.Sleep(600 * time.Millisecond)
		os.Exit(0)
	}()
}

// Staged-update files beside the launcher: HandleApplyPending writes them,
// FinishPendingUpdate consumes them once the Flutter window has exited.
const (
	pendingNewSuffix = ".new"
	pendingMarker    = "solo-update.pending"
)

// launcherPath resolves the launcher executable (the file the standalone
// flow replaces). Inside the Flutter host process (solo_checker.exe, which
// loads this code as a DLL) os.Executable() points at the client, so the
// launcher sits beside it.
func launcherPath() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	if strings.EqualFold(filepath.Base(self), keys.ExeKey) {
		return self, nil
	}
	p := filepath.Join(filepath.Dir(self), keys.ExeKey)
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("%s not found beside the app", keys.ExeKey)
	}
	return p, nil
}

// HandleApplyPending is the in-process (dart:ffi) variant of HandleApply.
// The DLL's host process must never replace its own running executable, so
// this only downloads the new launcher next to the current one and drops a
// marker file; the launcher swaps itself (FinishPendingUpdate) after the
// Flutter window exits, then restarts and bootstraps the new client files.
func HandleApplyPending(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	w.Header().Set("Content-Type", "application/json")
	fail := func(msg string) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": msg})
	}
	m, err := FetchUpdateManifest()
	if err != nil {
		fail(err.Error())
		return
	}
	if !VersionNewer(m.Version, AppVersion) {
		fail("already on " + AppVersion)
		return
	}
	launcher, err := launcherPath()
	if err != nil {
		fail(err.Error())
		return
	}
	raw, err := Get(m.Key)
	if err != nil {
		fail(err.Error())
		return
	}
	sum := sha256.Sum256(raw)
	got := hex.EncodeToString(sum[:])
	if m.SHA256 != "" && got != m.SHA256 {
		fail("sha256 mismatch got " + got)
		return
	}
	newExe := launcher + pendingNewSuffix
	if err := os.WriteFile(newExe, raw, 0755); err != nil {
		fail(err.Error())
		return
	}
	marker := filepath.Join(filepath.Dir(launcher), pendingMarker)
	if err := os.WriteFile(marker, []byte(m.Version), 0644); err != nil {
		_ = os.Remove(newExe)
		fail(err.Error())
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "restarting": true, "version": m.Version})
}

// FinishPendingUpdate is called by the launcher after the Flutter window
// exits. When a staged update marker is present it swaps the launcher for
// the downloaded one and starts it detached (the helper script waits for
// this process to exit first). Returns a log message when work was done,
// or "" when there is nothing to apply.
func FinishPendingUpdate() string {
	self, err := os.Executable()
	if err != nil {
		return ""
	}
	dir := filepath.Dir(self)
	marker := filepath.Join(dir, pendingMarker)
	if _, err := os.Stat(marker); err != nil {
		return ""
	}
	if !strings.EqualFold(filepath.Base(self), keys.ExeKey) {
		return "" // leave the marker for the real launcher
	}
	newExe := self + pendingNewSuffix
	raw, err := os.ReadFile(newExe)
	if err != nil {
		_ = os.Remove(marker)
		return "staged update missing: " + err.Error()
	}
	old := self + ".old"
	_ = os.Remove(old)
	if err := os.Rename(self, old); err != nil {
		_ = os.Remove(marker)
		return "could not move running exe aside: " + err.Error()
	}
	if err := os.WriteFile(self, raw, 0755); err != nil {
		_ = os.Rename(old, self)
		_ = os.Remove(marker)
		return "could not write new exe: " + err.Error()
	}
	_ = os.Remove(newExe)
	_ = os.Remove(marker)
	if err := startDetached(self); err != nil {
		return "update written but restart failed (start it manually): " + err.Error()
	}
	return "launcher swapped; restarting"
}

func startDetached(exe string) error {
	if runtime.GOOS == "windows" {
		pid := os.Getpid()
		bat := filepath.Join(os.TempDir(), "solo-upd.bat")
		script := fmt.Sprintf("@echo off\r\n:wait\r\ntasklist /FI \"PID eq %d\" | find \"%d\" >nul\r\nif not errorlevel 1 (\r\n  timeout /t 1 /nobreak >nul\r\n  goto wait\r\n)\r\ntaskkill /F /IM \"%s\" >nul 2>nul\r\nstart \"\" \"%s\"\r\nif exist \"%s.old\" del /F /Q \"%s.old\"\r\ndel \"%%~f0\"\r\n", pid, pid, filepath.Base(exe), exe, exe, exe)
		if err := os.WriteFile(bat, []byte(script), 0644); err != nil {
			return err
		}
		cmd := exec.Command("cmd", "/C", "start", "/B", "", bat)
		cmd.Stdin = nil
		cmd.Stdout = nil
		cmd.Stderr = nil
		return cmd.Start()
	}
	cmd := exec.Command(exe)
	cmd.Dir = filepath.Dir(exe)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Start()
}

func AfterUpdateCleanup() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	_ = os.Remove(exe + ".old")
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	cerr := out.Close()
	if err != nil {
		return err
	}
	return cerr
}
