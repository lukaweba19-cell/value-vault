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

const AppVersion = "1.0.25"

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
	// Sidecar objects for installs that only carry the exe, fetched on
	// first boot after a self-update (bootstrap.go). Older clients ignore
	// these; Bundle/BundleSHA are the legacy zip (≤1.0.19).
	DLL       string `json:"dll,omitempty"`
	DLLSHA    string `json:"dll_sha256,omitempty"`
	Bundle    string `json:"bundle,omitempty"`
	BundleSHA string `json:"bundle_sha256,omitempty"`
	// Files is the full install tree as loose objects at the bucket root:
	// object key (relative path, forward slashes) -> sha256.
	Files map[string]string `json:"files,omitempty"`
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
	for k, v := range m.Files {
		m.Files[k] = strings.ToLower(strings.TrimSpace(v))
	}
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

// Legacy staging-file names from ≤1.0.17. The current apply flow swaps the
// launcher in one shot and never creates these — they are only swept by
// AfterUpdateCleanup so old installs stop littering the folder.
const (
	pendingNewSuffix = ".new"
	pendingMarker    = "solo-update.pending"
)

// launcherPath resolves the launcher executable (the file the standalone
// flow replaces). Inside the Flutter host process (the window exe, which
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
	dir := filepath.Dir(self)
	// Canonical name first, then the ≤1.0.18 name: an install whose
	// launcher has not relocated yet still has a swap-able file beside the
	// window, and the new image renames itself to ExeKey at next boot.
	for _, n := range []string{keys.ExeKey, "helper.exe"} {
		p := filepath.Join(dir, n)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("%s not found beside the app", keys.ExeKey)
}

// HandleApplyPending is the in-process (dart:ffi) update apply. It follows
// the simple self-update model: download the new launcher, rename the
// running one aside, write the new bytes in its place, then drop a small
// script that starts the new launcher and deletes the old file once this
// window exits. No .new/.pending staging files are ever created — every
// failure path restores the rename before returning.
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
	// Swap: renaming a running image is allowed on Windows; after the
	// rename the path is free for the new bytes.
	old := launcher + ".old"
	_ = os.Remove(old)
	if err := os.Rename(launcher, old); err != nil {
		fail("could not move the current launcher aside: " + err.Error())
		return
	}
	if err := os.WriteFile(launcher, raw, 0755); err != nil {
		_ = os.Rename(old, launcher) // restore — leave nothing behind
		fail("could not write the new launcher: " + err.Error())
		return
	}
	// Restart script: waits for this window to exit, starts the new
	// launcher and deletes the old image (retrying while it winds down).
	restarting := true
	if err := startRestartBat(os.Getpid(), launcher); err != nil {
		// The swap itself succeeded; the new launcher runs from the next
		// manual start and AfterUpdateCleanup removes the .old file.
		restarting = false
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "restarting": restarting, "version": m.Version})
}

// startRestartBat drops the post-exit script: wait for the window process
// to die, start the (already swapped) launcher, delete the old image with
// a bounded retry loop, then delete itself.
func startRestartBat(windowPID int, launcher string) error {
	if runtime.GOOS != "windows" {
		return nil
	}
	bat := filepath.Join(os.TempDir(), "solo-restart.bat")
	script := fmt.Sprintf(
		"@echo off\r\n:wait\r\ntasklist /FI \"PID eq %d\" | find \"%d\" >nul\r\n"+
			"if not errorlevel 1 (\r\n  ping -n 2 127.0.0.1 >nul\r\n  goto wait\r\n)\r\n"+
			"ping -n 2 127.0.0.1 >nul\r\n"+
			"start \"\" \"%s\"\r\n"+
			"set /i _n=0\r\n"+
			":delold\r\n"+
			"if not exist \"%s.old\" goto doneold\r\n"+
			"if %%_n%% GEQ 30 goto doneold\r\n"+
			"del /F /Q \"%s.old\" >nul 2>nul\r\n"+
			"set /i _n+=1\r\n"+
			"ping -n 2 127.0.0.1 >nul\r\n"+
			"goto delold\r\n"+
			":doneold\r\n"+
			"del /F /Q \"%%~f0\"\r\n",
		windowPID, windowPID, launcher, launcher, launcher)
	if err := os.WriteFile(bat, []byte(script), 0644); err != nil {
		return err
	}
	return hiddenCmd("cmd", "/C", bat).Start()
}

func startDetached(exe string) error {
	if runtime.GOOS == "windows" {
		pid := os.Getpid()
		bat := filepath.Join(os.TempDir(), "solo-upd.bat")
		// The delete loop matters: the .old file is the image this process
		// was running from and can stay locked briefly — retry instead of
		// leaving the old exe behind. ping is used instead of timeout so
		// the script also works in a hidden console.
		script := fmt.Sprintf(
			"@echo off\r\n:wait\r\ntasklist /FI \"PID eq %d\" | find \"%d\" >nul\r\n"+
				"if not errorlevel 1 (\r\n  ping -n 2 127.0.0.1 >nul\r\n  goto wait\r\n)\r\n"+
				"taskkill /F /IM \"%s\" >nul 2>nul\r\n"+
				"start \"\" \"%s\"\r\n"+
				"set /i _n=0\r\n"+
				":delold\r\n"+
				"if not exist \"%s.old\" goto doneold\r\n"+
				"if %%_n%% GEQ 20 goto doneold\r\n"+
				"del /F /Q \"%s.old\" >nul 2>nul\r\n"+
				"set /i _n+=1\r\n"+
				"ping -n 2 127.0.0.1 >nul\r\n"+
				"goto delold\r\n"+
				":doneold\r\n"+
				"del \"%%~f0\"\r\n",
			pid, pid, filepath.Base(exe), exe, exe, exe)
		if err := os.WriteFile(bat, []byte(script), 0644); err != nil {
			return err
		}
		return hiddenCmd("cmd", "/C", "start", "/B", "", bat).Start()
	}
	cmd := exec.Command(exe)
	cmd.Dir = filepath.Dir(exe)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Start()
}

// AfterUpdateCleanup removes update leftovers at every boot: the renamed
// old launcher (".old", locked for a moment while the old process winds
// down) plus any legacy staging files from ≤1.0.17 (".new" downloads and
// the "solo-update.pending" marker — the current flow never creates
// them). It runs at launcher boot and from the window process, so it
// resolves the launcher's path rather than the calling executable's.
func AfterUpdateCleanup() {
	self, err := os.Executable()
	if err != nil {
		return
	}
	dir := filepath.Dir(self)
	target := self
	if !strings.EqualFold(filepath.Base(self), keys.ExeKey) {
		if p, err := launcherPath(); err == nil {
			target = p
		}
	}
	olds := []string{
		target + ".old",
		filepath.Join(dir, "xbox-checker.exe.old"), // pre-1.0.17 launcher name
		filepath.Join(dir, "helper.exe.old"),       // 1.0.17–1.0.18 launcher name
	}
	for i := 0; i < 5; i++ {
		removed := true
		for _, n := range olds {
			if _, err := os.Stat(n); err != nil {
				continue
			}
			if os.Remove(n) != nil {
				removed = false
			}
		}
		if removed {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	// Legacy staging litter (≤1.0.17): never valid in the current flow.
	for _, n := range []string{
		filepath.Join(dir, keys.ExeKey+pendingNewSuffix),
		filepath.Join(dir, "xbox-checker.exe"+pendingNewSuffix),
		filepath.Join(dir, "helper.exe"+pendingNewSuffix), // ≤1.0.18 staging name
		filepath.Join(dir, pendingMarker),
	} {
		_ = os.Remove(n)
	}
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
