Self-hosted fonts for the documentation site, both SIL OFL 1.1 (licence text and copyright lines in OFL.txt).

- Fredoka (The Fredoka Project Authors), display. Variable wght 300-700, latin subset with the
  wdth axis pinned to 100.
- Recursive (The Recursive Project Authors), body and code. Variable wght 300-900 and MONO 0-1,
  latin subset. The CASL, CRSV and slnt axes are pinned to 0, 0.5 and 0. Code uses
  font-variation-settings: 'MONO' 1.

Both start from the latin woff2 files Google Fonts serves (Fredoka v17, Recursive v44) and were
reduced with fontTools' instancer (axis pinning only, no glyph or name changes). The Reserved
Font Names are not modified. Latin is preloaded; other scripts fall back to the system stack.
