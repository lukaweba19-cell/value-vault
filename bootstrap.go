// First-boot bootstrap for installs that only carry the exe. Old clients
// self-update by replacing just xbox-checker.exe; after this binary restarts
// it pulls the remaining release objects (xbox_checker.dll and the full
// Flutter client bundle) from R2 once, so the native window can start.
package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"

	"xbox-checker/internal/keys"
	"xbox-checker/internal/r2store"
)

// ensureClientBundle materializes the Flutter client next to this exe when
// any required piece is missing (launcher exe, engine DLL or the Flutter
// asset bundle). It is a no-op on healthy installs (everything present → no
// network traffic at all).
func ensureClientBundle() {
	dir := exeDir()
	if dir == "" {
		bootLogf("client bootstrap: cannot resolve exe dir")
		return
	}
	if clientFilesReady() {
		return
	}
	bootLogf("client bootstrap: files missing beside exe — fetching bundle from R2")
	m, err := r2store.FetchUpdateManifest()
	if err != nil {
		bootLogf("client bootstrap: manifest: %v", err)
		return
	}
	raw, err := r2store.Get(m.Bundle)
	if err != nil {
		bootLogf("client bootstrap: bundle: %v", err)
		return
	}
	if m.BundleSHA != "" {
		sum := sha256.Sum256(raw)
		if got := hex.EncodeToString(sum[:]); got != m.BundleSHA {
			bootLogf("client bootstrap: bundle sha mismatch (got %s)", got)
			return
		}
	}
	if err := extractBundle(raw, dir); err != nil {
		bootLogf("client bootstrap: extract: %v", err)
	}
}

// clientFilesReady reports whether the Flutter client is complete next to
// this exe: the launcher, the engine DLL and the Flutter asset bundle.
func clientFilesReady() bool {
	dir := exeDir()
	if dir == "" {
		return false
	}
	for _, n := range []string{nativeUIExe, keys.DllKey} {
		if _, err := os.Stat(filepath.Join(dir, n)); err != nil {
			return false
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "data", "flutter_assets")); err != nil {
		return false
	}
	return true
}

func exeDir() string {
	self, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Dir(self)
}

// extractBundle unpacks the release zip beside the exe. Individual files
// that cannot be written (notably this running exe, which Windows keeps
// locked — it is already the newest piece, having arrived via self-update)
// are skipped rather than failing the whole extraction.
func extractBundle(raw []byte, dir string) error {
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return err
	}
	self, _ := os.Executable()
	skipNames := map[string]bool{
		filepath.Base(filepath.Clean(self)): true,
		"xbox-checker.exe":                  true, // may equal self; never overwrite
	}
	extracted := 0
	for _, f := range zr.File {
		name := filepath.Clean(f.Name)
		if name == "." || strings.HasPrefix(name, "..") || filepath.IsAbs(name) {
			continue
		}
		dst := filepath.Join(dir, name)
		if !underDir(dst, dir) {
			continue
		}
		if f.FileInfo().IsDir() {
			_ = os.MkdirAll(dst, 0o755)
			continue
		}
		if skipNames[filepath.Base(dst)] {
			continue
		}
		if err := writeZipFile(f, dst); err != nil {
			bootLogf("client bootstrap: %s: %v", name, err)
			continue
		}
		extracted++
	}
	bootLogf("client bootstrap: extracted %d files", extracted)
	return nil
}

func writeZipFile(f *zip.File, dst string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, rc)
	return err
}

// underDir keeps extraction inside the target directory (zip-slip guard).
func underDir(dst, dir string) bool {
	rel, err := filepath.Rel(dir, dst)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}
