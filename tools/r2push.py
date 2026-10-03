#!/usr/bin/env python3
"""Push dist-test exe + dll + client bundle + manifest to R2.

- Reads the R2 constants from internal/keys/*.go at runtime (values never printed).
- Refuses to upload unless: manifest version == AppVersion (from r2store.go),
  the manifest sha256 equals the sha256 of the exe, and — when present — the
  dll/bundle shas match too.
- Uploads exe, dll and bundle FIRST, the manifest LAST (clients must never
  see a manifest referencing missing objects), then GETs the manifest back
  to confirm what clients will see.
"""
import datetime
import glob
import hashlib
import hmac
import json
import re
import sys
import urllib.request
import pathlib

ROOT = pathlib.Path(__file__).resolve().parents[1]
EXE_PATH = ROOT / "dist-test" / "installer.exe"
DLL_PATH = ROOT / "dist-test" / "solo.dll"
BUNDLE_PATH = ROOT / "dist-test" / "solo-checker-bundle.zip"
MANIFEST_PATH = ROOT / "dist-test" / "manifest.json"


def load_go_consts(path_glob, names):
    text = ""
    for f in glob.glob(str(ROOT / path_glob)):
        text += open(f, encoding="utf-8").read()
    out = {}
    for n in names:
        m = re.search(r'^\s*' + n + r'\s*=\s*"([^"]*)"', text, re.M)
        if not m:
            sys.exit(f"missing constant {n}")
        out[n] = m.group(1)
    return out


def hmac_sha256(key, msg):
    return hmac.new(key, msg.encode("utf-8"), hashlib.sha256).digest()


def sigv4_headers(method, host, path, body, content_type, creds, payload_hash):
    now = datetime.datetime.now(datetime.timezone.utc)
    amz = now.strftime("%Y%m%dT%H%M%SZ")
    date = now.strftime("%Y%m%d")
    canonical = (
        f"{method}\n{path}\n\n"
        f"host:{host}\n"
        f"x-amz-content-sha256:{payload_hash}\n"
        f"x-amz-date:{amz}\n\n"
        f"host;x-amz-content-sha256;x-amz-date\n"
        f"{payload_hash}"
    )
    scope = f"{date}/auto/s3/aws4_request"
    string_to_sign = (
        "AWS4-HMAC-SHA256\n" + amz + "\n" + scope + "\n"
        + hashlib.sha256(canonical.encode("utf-8")).hexdigest()
    )
    k = hmac_sha256(("AWS4" + creds["WriteSecretKey"]).encode("utf-8"), date)
    k = hmac_sha256(k, "auto")
    k = hmac_sha256(k, "s3")
    k = hmac_sha256(k, "aws4_request")
    sig = hmac.new(k, string_to_sign.encode("utf-8"), hashlib.sha256).hexdigest()
    headers = {
        "x-amz-date": amz,
        "x-amz-content-sha256": payload_hash,
        "Authorization": (
            f"AWS4-HMAC-SHA256 Credential={creds['WriteAccessKey']}/{scope}, "
            f"SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature={sig}"
        ),
    }
    if content_type:
        headers["Content-Type"] = content_type
    return headers


def put(creds, key, body, content_type):
    host = creds["EndpointHost"]
    enc_key = key.replace(" ", "%20")
    path = "/" + creds["Bucket"] + "/" + enc_key
    payload = hashlib.sha256(body).hexdigest()
    headers = sigv4_headers("PUT", host, path, body, content_type, creds, payload)
    req = urllib.request.Request("https://" + host + path, data=body, headers=headers, method="PUT")
    with urllib.request.urlopen(req, timeout=120) as resp:
        resp.read()
        if resp.status not in (200, 201):
            sys.exit(f"PUT {key}: HTTP {resp.status}")
    print(f"uploaded {key} ({len(body)} bytes)")


def get(creds, key):
    host = creds["EndpointHost"]
    enc_key = key.replace(" ", "%20")
    path = "/" + creds["Bucket"] + "/" + enc_key
    headers = sigv4_headers("GET", host, path, None, None, creds, "UNSIGNED-PAYLOAD")
    req = urllib.request.Request("https://" + host + path, headers=headers, method="GET")
    with urllib.request.urlopen(req, timeout=60) as resp:
        return resp.read()


def main():
    creds = load_go_consts(
        "internal/keys/*.go",
        ["EndpointHost", "WriteAccessKey", "WriteSecretKey", "Bucket", "ManifestKey", "ExeKey", "DllKey", "BundleKey"],
    )

    exe = EXE_PATH.read_bytes()
    manifest_raw = MANIFEST_PATH.read_bytes()
    manifest = json.loads(manifest_raw)
    exe_sha = hashlib.sha256(exe).hexdigest()

    dll = DLL_PATH.read_bytes() if DLL_PATH.is_file() else None
    bundle = BUNDLE_PATH.read_bytes() if BUNDLE_PATH.is_file() else None

    r2store = (ROOT / "internal" / "r2store" / "r2store.go").read_text(encoding="utf-8")
    m = re.search(r'AppVersion\s*=\s*"([^"]+)"', r2store)
    app_version = m.group(1) if m else "?"

    # Hard gates before anything touches R2:
    if not manifest.get("version"):
        sys.exit("manifest has no version")
    if manifest.get("version") != app_version:
        sys.exit(f"manifest version {manifest.get('version')!r} != AppVersion {app_version!r}")
    if manifest.get("sha256", "").lower() != exe_sha:
        sys.exit(f"manifest sha {manifest.get('sha256')} != exe sha {exe_sha}")
    if manifest.get("dll_sha256"):
        if dll is None:
            sys.exit("manifest lists a dll but dist-test/xbox_checker.dll is missing")
        if manifest["dll_sha256"].lower() != hashlib.sha256(dll).hexdigest():
            sys.exit("manifest dll sha != dll sha")
    if manifest.get("bundle_sha256"):
        if bundle is None:
            sys.exit("manifest lists a bundle but dist-test/solo-checker-bundle.zip is missing")
        if manifest["bundle_sha256"].lower() != hashlib.sha256(bundle).hexdigest():
            sys.exit("manifest bundle sha != bundle sha")
    print(f"checks ok: version {app_version} (manifest + AppVersion), sha {exe_sha}")

    exe_key = manifest.get("key") or creds["ExeKey"]
    if exe_key != creds["ExeKey"]:
        print(f"note: manifest key {exe_key!r} differs from ExeKey; uploading to manifest key")

    # Sidecar objects first, manifest last: clients must never see a
    # manifest that references an object which is not uploaded yet.
    put(creds, exe_key, exe, "application/octet-stream")
    if manifest.get("dll_sha256"):
        dll_key = manifest.get("dll") or creds["DllKey"]
        put(creds, dll_key, dll, "application/octet-stream")
    if manifest.get("bundle_sha256"):
        bundle_key = manifest.get("bundle") or creds["BundleKey"]
        put(creds, bundle_key, bundle, "application/zip")
    put(creds, creds["ManifestKey"], manifest_raw, "application/json")

    seen = json.loads(get(creds, creds["ManifestKey"]))
    print(f"R2 manifest now says: version={seen.get('version')} sha256={seen.get('sha256')}")
    if seen.get("version") != app_version or seen.get("sha256", "").lower() != exe_sha:
        sys.exit("post-upload verification FAILED")
    if manifest.get("dll_sha256") and seen.get("dll_sha256", "").lower() != manifest["dll_sha256"].lower():
        sys.exit("post-upload verification FAILED (dll)")
    if manifest.get("bundle_sha256") and seen.get("bundle_sha256", "").lower() != manifest["bundle_sha256"].lower():
        sys.exit("post-upload verification FAILED (bundle)")
    print("PUSH TO R2 OK")


if __name__ == "__main__":
    main()
