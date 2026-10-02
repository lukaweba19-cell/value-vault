#!/usr/bin/env python3
"""Extract the largest PNG frame from solo-checker.ico as the app's in-UI icon.

Flutter cannot decode .ico, so the UI uses this PNG (window/taskbar icon keep
using the .ico via Runner.rc).
"""
import pathlib
import struct
import sys

ROOT = pathlib.Path(__file__).resolve().parents[1]
ICO = ROOT / "solo-checker.ico"
OUT = ROOT / "client" / "assets" / "solo_icon.png"


def main():
    data = ICO.read_bytes()
    count = struct.unpack("<H", data[4:6])[0]
    best = None  # (width, bytes)
    for i in range(count):
        off = 6 + i * 16
        w, h, _cc, _r, _planes, _bpp, size, ofs = struct.unpack(
            "<BBBBHHII", data[off : off + 16]
        )
        w = w or 256
        blob = data[ofs : ofs + size]
        if blob[:8] != b"\x89PNG\r\n\x1a\n":
            continue
        if best is None or w > best[0]:
            best = (w, blob)
    if best is None:
        sys.exit("no PNG frame found in solo-checker.ico")
    OUT.parent.mkdir(parents=True, exist_ok=True)
    OUT.write_bytes(best[1])
    print(f"wrote {OUT} ({best[0]}px, {len(best[1])} bytes)")


if __name__ == "__main__":
    main()
