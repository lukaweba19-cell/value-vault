// First-boot bootstrap for installs that only carry the exe. Old clients
// self-update by replacing just the launcher; after this binary restarts
// it pulls the remaining release files (window, Flutter runtime, data/
// assets, engine DLL) from R2 as individual objects — no zip — verified
// per file against the manifest's sha256 list.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"xbox-checker/internal/keys"
	"xbox-checker/internal/r2store"
)

// ensureClientBundle materializes the Flutter client next to this exe when
// any required piece is missing or when the extracted client is from an
// older release (data/client.version stamp differs from AppVersion — an
// update must deliver the new DLL and assets, not just the new launcher).
// It pulls every entry of the manifest's file list as its own R2 object,
// skipping files already present with the right sha256 (so a later
// release only downloads what changed). It is a no-op on healthy, current
// installs (everything present → no network traffic at all).
func ensureClientBundle() {
	dir := exeDir()
	if dir == "" {
		bootLogf("client bootstrap: cannot resolve exe dir")
		return
	}
	if clientFilesReady() && clientStampCurrent(dir) {
		return
	}
	if clientFilesReady() {
		bootLogf("client bootstrap: client is %q, want %s — pulling file list from R2",
			clientStamp(dir), r2store.AppVersion)
	} else {
		bootLogf("client bootstrap: files missing beside exe — pulling file list from R2")
	}
	m, err := r2store.FetchUpdateManifest()
	if err != nil {
		bootLogf("client bootstrap: manifest: %v", err)
		return
	}
	if len(m.Files) == 0 {
		bootLogf("client bootstrap: manifest %s has no file list", m.Version)
		return
	}
	self, _ := os.Executable()
	rels := make([]string, 0, len(m.Files))
	for rel := range m.Files {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	wrote := 0
	for _, rel := range rels {
		want := m.Files[rel]
		name := filepath.Clean(filepath.FromSlash(rel))
		if name == "." || strings.HasPrefix(name, "..") || filepath.IsAbs(name) {
			bootLogf("client bootstrap: bad file key %q", rel)
			return
		}
		dst := filepath.Join(dir, name)
		if !underDir(dst, dir) {
			bootLogf("client bootstrap: bad file key %q", rel)
			return
		}
		// The running image only: it arrived via self-update, so it is
		// already the new bytes — and Windows keeps it locked anyway.
		if strings.EqualFold(filepath.Clean(dst), filepath.Clean(self)) {
			continue
		}
		if sha256File(dst) == want {
			continue // already correct — only changed files get fetched
		}
		raw, err := r2store.Get(rel)
		if err != nil {
			bootLogf("client bootstrap: %s: %v", rel, err)
			return
		}
		sum := sha256.Sum256(raw)
		if got := hex.EncodeToString(sum[:]); got != want {
			bootLogf("client bootstrap: %s sha mismatch (got %s)", rel, got)
			return
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			bootLogf("client bootstrap: %s: %v", rel, err)
			return
		}
		if err := os.WriteFile(dst, raw, 0o755); err != nil {
			bootLogf("client bootstrap: %s: %v", rel, err)
			return
		}
		wrote++
	}
	if !clientFilesReady() {
		bootLogf("client bootstrap: files still incomplete after pull — not stamping")
		return
	}
	// Stamp the pulled client so the next release refreshes it again.
	if err := os.WriteFile(clientStampPath(dir), []byte(m.Version), 0o644); err != nil {
		bootLogf("client bootstrap: stamp: %v", err)
	}
	bootLogf("client bootstrap: pulled %d files, stamp %s", wrote, m.Version)
	// Remove the engine DLL under its pre-1.0.16 name (xbox_checker.dll);
	// keeping it would leave a stale second engine beside solo.dll forever.
	if keys.DllKey != "xbox_checker.dll" {
		_ = os.Remove(filepath.Join(dir, "xbox_checker.dll"))
	}
	// The window now lives at nativeUIExe; drop the pre-1.0.17 copy (it can
	// only be removed once it is not running — failures are ignored).
	if legacyUIExe != nativeUIExe {
		if _, err := os.Stat(filepath.Join(dir, nativeUIExe)); err == nil {
			_ = os.Remove(filepath.Join(dir, legacyUIExe))
		}
	}
}

// clientStampPath is the version marker written after a successful client
// extraction, kept inside data/ so it ships beside flutter_assets.
func clientStampPath(dir string) string {
	return filepath.Join(dir, "data", "client.version")
}

func clientStamp(dir string) string {
	b, err := os.ReadFile(clientStampPath(dir))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func clientStampCurrent(dir string) bool {
	s := clientStamp(dir)
	return s != "" && s == r2store.AppVersion
}

// clientFilesReady reports whether the Flutter client is complete next to
// this exe: the launcher, the engine DLL and the Flutter asset bundle.
func clientFilesReady() bool {
	dir := exeDir()
	if dir == "" {
		return false
	}
	// Window: accept the current name or the pre-1.0.17 one — an install
	// updated while offline still has a working window under the old name.
	window := false
	for _, n := range []string{nativeUIExe, legacyUIExe} {
		if _, err := os.Stat(filepath.Join(dir, n)); err == nil {
			window = true
			break
		}
	}
	if !window {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, keys.DllKey)); err != nil {
		return false
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

// sha256File returns the lowercase hex sha256 of a file, or "" when the
// file is missing/unreadable (never matches a manifest hash → refetch).
func sha256File(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// underDir keeps extraction inside the target directory (zip-slip guard).
func underDir(dst, dir string) bool {
	rel, err := filepath.Rel(dir, dst)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}
