#!/bin/sh
# Regenerates the DjVu golden fixtures in this directory.
# Requires the djvulibre tools (cjb2, djvm, djvmcvt, djvused).
set -e

rm -f *.pbm *.djvu *.txt
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

python3 - <<'EOF'
import struct
def make_pbm(path, w, h, blocks):
    data = bytearray()
    for y in range(h):
        row = bytearray((w + 7) // 8)
        for (x0, y0, bw, bh) in blocks:
            if y0 <= y < y0 + bh:
                for x in range(x0, min(x0 + bw, w)):
                    row[x // 8] |= 1 << (7 - x % 8)
        data += row
    with open(path, 'wb') as f:
        f.write(b"P4\n%d %d\n" % (w, h))
        f.write(bytes(data))

make_pbm("page1.pbm", 200, 280, [(20, 200, 160, 8), (30, 100, 40, 60), (90, 100, 40, 60), (150, 100, 40, 60), (20, 20, 160, 6)])
make_pbm("page2.pbm", 200, 280, [(20, 240, 160, 10), (20, 150, 160, 8), (40, 40, 120, 80)])
make_pbm("page3.pbm", 200, 280, [(40, 60, 120, 12), (40, 90, 120, 12), (40, 210, 120, 30)])
EOF

# Single page bilevel DjVu documents.
cjb2 -dpi 150 page1.pbm p1.djvu
cjb2 -dpi 150 page2.pbm p2.djvu
cjb2 -dpi 150 page3.pbm p3.djvu
cp p1.djvu single.djvu

# Single page document with annotations and hidden text. Document-level
# metadata is deliberately not set: djvused stores it in a shared
# annotation file, which turns the document into a bundled FORM:DJVM.
djvused single.djvu -s -f - <<'EOF'
select 1
set-ant
(zoom stretch)
(maparea "https://example.org/" "External link" (rect 10 10 40 40) (border_avis))
.
set-txt
(page 0 0 200 280 "Hello single page")
.
EOF

# Bundled multipage document with metadata, annotations, hidden text,
# an outline and thumbnails.
djvm -c bundled.djvu p1.djvu p2.djvu p3.djvu
djvused bundled.djvu -s -f - <<'EOF'
set-meta
title	"Example Document"
author	"Jane Doe"
subject	"DjVu test fixture"
keywords	"djvu, test, fixture"
producer	"djvulibre 3.5.29"
.
select p1.djvu
set-ant
(zoom stretch)
(maparea "#p2.djvu" "Go to page two" (rect 10 10 40 40) (border_avis))
.
set-txt
(page 0 0 200 280 "Hello hidden text page one")
.
select p2.djvu
set-txt
(page 0 0 200 280 (line 10 100 180 120 (word 10 100 60 120 "Second") (word 70 100 130 120 "page")))
.
set-outline
(bookmarks
 ("Chapter 1" "#p1.djvu")
 ("Chapter 2" "#p2.djvu"
  ("Section 2.1" "#p3.djvu")))
.
EOF

# Indirect multipage document.
rm -rf "$tmp/indir"
mkdir -p "$tmp/indir"
djvmcvt -i bundled.djvu "$tmp/indir" index.djvu
mv "$tmp/indir/index.djvu" indirect.djvu
mv "$tmp/indir"/* .

rm -f *.pbm
ls -la
