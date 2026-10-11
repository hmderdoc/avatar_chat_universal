# ANSI editor

The art and avatar editor the page opens, vendored from Futureland's site
(`webv4_custom/ansi-editor`, `root/js/ansi-editor-modal.js`,
`root/css/ansi-editor-modal.css`) so the bundle needs nothing from that
site at run time. `build.js` bundles `src/` into
`dist/webv4/root/avatarchat/ansi-editor.js` and copies the rest beside it.

- `src/`, `ansi-editor-modal.js`, `ansi-editor-modal.css`: Futureland's own
  code, written for that site in the style of Moebius (palette layout,
  tools); no Moebius source is copied. Same licence as this repository.
- `fonts/CP437.F16`: the IBM VGA 8x16 code page 437 font as shipped by
  Moebius (`blocktronics/moebius`, `app/fonts/ibm/CP437.F16`), Apache
  License 2.0; the licence text is `fonts/LICENSE-moebius.txt`.

Changes here should go back to Futureland's copy too, or the two drift.
