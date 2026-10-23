# Changelog

## v0.2.0 (2026-10-23)

Page decoding and rendering. The library now produces images. Module
path: github.com/din14970/djvu-go.

- `Document.Render(page, RenderOptions{Subsample})` and
  `Document.RenderPNG` composite a page into a top-down RGBA image.
  Output is byte-identical to the reference ddjvu tool for photo,
  compound palette, three-layer color and bilevel pages, at native
  resolution and at subsampling factors 2 through 12.
- JB2 bilevel decoding (Sjbz) including shared shape dictionaries
  (Djbz) referenced through INCL chunks. Page masks match
  `ddjvu -mode=black` byte for byte.
- IW44 wavelet decoding (BG44, FG44): luma, chroma and gray-in-color
  data, with slice decoding verified decision-for-decision against an
  instrumented djvulibre build.
- FGbz palette decoding (DjVuPalette) with BZZ compressed per-blit
  color indices.
- MRC compositing: subsampled alpha maps, background attenuation, the
  bilinear pixmap scaler with power-of-two pre-averaging, per-color
  group blending and supersampled stencil blending.
- Robustness: fuzz targets for every decoder and for the whole
  pipeline. Oversized pages (over 20000 pixels) are rejected before
  allocation; malformed chunks return errors, never panic. Fuzzing
  found and fixed two decoder bugs (a JB2 record-ordering panic and an
  IW44 end-of-input denial of service).
- Performance: a typical 300 dpi scanned page decodes in well under a
  second; decoded layers are cached per document; benchmarks for the
  alpha blits and full renders.

## v0.1.0 (2026-10-23)

Initial release: structure parsing only.

- IFF chunk walker (FORM/LIST/PROP/CAT, lazy payload access).
- Bundled and indirect multipage documents (DIRM versions 0 and 1,
  BZZ compressed directories).
- Page INFO, annotations, document metadata, hidden text and outline.
- BZZ decompression.
