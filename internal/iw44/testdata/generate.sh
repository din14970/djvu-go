#!/bin/sh
# Regenerates the IW44 golden fixtures in this directory.
# Requires the djvulibre tools (c44, ddjvu).
set -e

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

python3 - "$tmp" <<'EOF'
import sys
tmp = sys.argv[1]
w, h = 320, 240
# Gray source: a wrapping gradient with a quadratic term.
with open(f"{tmp}/gray.pgm", "wb") as f:
    f.write(b"P5\n%d %d\n255\n" % (w, h))
    for y in range(h):
        f.write(bytes(min(255, (x * 7 + y * 5 + (x * y) // 64) % 256) for x in range(w)))
# Color source: independent channel gradients.
with open(f"{tmp}/color.ppm", "wb") as f:
    f.write(b"P6\n%d %d\n255\n" % (w, h))
    for y in range(h):
        row = bytearray()
        for x in range(w):
            row += bytes(((x * 3 + y) % 256, (y * 2) % 256, (x + y * 3 + (x * y) // 128) % 256))
        f.write(row)
EOF

c44 -dpi 150 "$tmp/gray.pgm" gray.djvu
ddjvu -format=pgm -mode=background gray.djvu gray_bg.pgm
c44 -dpi 150 "$tmp/color.ppm" color.djvu
ddjvu -format=ppm -mode=background color.djvu color_bg.ppm
rm -f "$tmp"/*.pgm "$tmp"/*.ppm
