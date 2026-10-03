#!/usr/bin/env python3
"""Write dist-test/manifest.json for a built release exe (CI helper).

- Reads AppVersion from internal/r2store/r2store.go and ExeKey from
  internal/keys/keys.go (same constants tools/r2push.py gates on).
- Writes {"version","sha256","key","files"} — files maps every file of
  the client bundle tree to its sha256; tools/r2push.py uploads them as
  loose objects at the bucket root (no zip).

Usage:
  python3 tools/manifest.py [--exe PATH] [--dll PATH] [--bundle-dir PATH] [--out PATH]
"""
import argparse
import hashlib
import json
import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parents[1]


def go_string(path_glob, name):
    """Return the value of `name = "..."` from the first matching Go file."""
    for f in sorted(ROOT.glob(path_glob)):
        text = f.read_text(encoding="utf-8")
        m = re.search(r"^\s*(?:const\s+)?" + name + r'\s*=\s*"([^"]*)"', text, re.M)
        if m:
            return m.group(1)
    sys.exit(f"missing constant {name}")


def main():
    ap = argparse.ArgumentParser(description="Write the update manifest for a built release.")
    ap.add_argument("--exe", default=str(ROOT / "dist-test" / "installer.exe"),
                    help="path to the built launcher exe (default: dist-test/installer.exe)")
    ap.add_argument("--dll", default=str(ROOT / "dist-test" / "solo.dll"),
                    help="path to the FFI engine DLL (added to the manifest when present)")
    ap.add_argument("--bundle-dir", default=str(ROOT / "bundle"),
                    help="client bundle tree uploaded as loose R2 objects (default: bundle/)")
    ap.add_argument("--out", default=str(ROOT / "dist-test" / "manifest.json"),
                    help="where to write the manifest (default: dist-test/manifest.json)")
    args = ap.parse_args()

    exe_path = pathlib.Path(args.exe)
    if not exe_path.is_file():
        sys.exit(f"exe not found: {exe_path}")

    version = go_string("internal/r2store/*.go", "AppVersion")
    key = go_string("internal/keys/*.go", "ExeKey")
    sha = hashlib.sha256(exe_path.read_bytes()).hexdigest()

    manifest = {"version": version, "sha256": sha, "key": key}
    notes = []
    dll_path = pathlib.Path(args.dll)
    if dll_path.is_file():
        manifest["dll"] = go_string("internal/keys/*.go", "DllKey")
        manifest["dll_sha256"] = hashlib.sha256(dll_path.read_bytes()).hexdigest()
        notes.append(f"dll={manifest['dll']}")
    bundle_dir = pathlib.Path(args.bundle_dir)
    if not bundle_dir.is_dir():
        sys.exit(f"bundle dir not found: {bundle_dir}")
    files = {}
    for p in sorted(bundle_dir.rglob("*")):
        if p.is_file():
            files[p.relative_to(bundle_dir).as_posix()] = hashlib.sha256(
                p.read_bytes()).hexdigest()
    if not files:
        sys.exit(f"bundle dir is empty: {bundle_dir}")
    manifest["files"] = files
    notes.append(f"files={len(files)}")
    out = pathlib.Path(args.out)
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(json.dumps(manifest, separators=(",", ":")) + "\n", encoding="utf-8")
    extra = (" " + " ".join(notes)) if notes else ""
    print(f"manifest written: version={version} key={key} sha256={sha}{extra}")


if __name__ == "__main__":
    main()
