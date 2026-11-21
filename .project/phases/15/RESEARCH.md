# Phase 15 research: project-name slug from display name

## golang.org/x/text

- Installed: v0.43.0 (go.mod line 58, `// indirect`; go 1.27). pkg.go.dev shows v0.44.0 published 2026-11-09. Upgrade is optional; the API used here is stable. Maintained (golang.org/x, recent release).
- Using it directly means go.mod must move it from indirect to direct (`go mod tidy` does this).
- Source: https://pkg.go.dev/golang.org/x/text/unicode/norm

## NFD approach

- `norm.NFD.String(s)` returns the NFD form (documented "String returns f(s)"). It is a pure function, with no state and no error.
- Option A (manual): `d := norm.NFD.String(s)`, then range over runes, skip `unicode.Is(unicode.Mn, r)`, and map the rest. This is the recommended choice. It is simple and has no transformer plumbing. Slug building needs a rune loop anyway (the a-z0-9 filter and the `-` collapsing), so the mark removal fits into the same loop.
- Option B (`transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)` with `transform.String`): this is the usual idiom from the x/text docs and blogs. Pitfalls:
  - The transformer is stateful and not safe for concurrent use. Build it per call, or call `Reset`.
  - `transform.String` returns an error that must be handled, although it cannot fail here.
  - The trailing NFC step is only needed if the result stays Unicode. For an ASCII slug it is pointless.
  - It needs two more imports (`transform`, `runes`).
  - The result is the same as A.
- Pitfalls shared by both:
  - The order of steps matters. Do the ä ö ü ß mapping before NFD, or NFD turns ä into `a` plus U+0308 and "ae" is lost. Pick one: map ä ö ü ß on the original string (lower-case first, or also map Ä Ö Ü), then NFD.
  - Lower-case first with `strings.ToLower`. Note that `İ` (U+0130) lower-cases to `i` plus U+0307 (Mn), which is then dropped. This is fine.
  - `ß` has no decomposition, so it must be mapped by hand. Upper-case `ẞ` (U+1E9E) lower-cases to ß in Go, so lower-case first.
  - Only Mn is dropped. Mc and Me marks are rare in Latin text and become `-` through the fallback, which is acceptable.
  - Compatibility forms (NFKD) are not needed. If wanted, NFKD would also turn ligatures such as ﬁ and fullwidth letters into ASCII. NFD does not.
  - Invalid UTF-8: `norm.NFD.String` passes invalid bytes through, and ranging yields U+FFFD, which becomes `-` in the fallback. No panic.
  - Trim leading and trailing `-`, and decide what an empty result does (for example only emoji or CJK gives an empty slug). The caller needs a fallback or an error.

## Latin letters with no canonical decomposition (NFD leaves them as they are)

These must be mapped by hand (lower-case form; upper-case ones are removed by ToLower first):

| Letter | Transliteration |
|---|---|
| ø (Ø) | o (Danish/Norwegian oe is also common) |
| ł (Ł) | l |
| đ (Đ) | d |
| ð (Ð) | d (or dh) |
| æ (Æ) | ae |
| œ (Œ) | oe |
| þ (Þ) | th |
| ı (dotless i) | i |
| ŋ (Ŋ) | ng |
| ß | ss (also ẞ) |
| ħ (Ħ) | h |
| ŧ (Ŧ) | t |
| ĸ | k |
| ſ (long s) | s |
| ƒ | f |
| ɓ ɗ ƙ etc. (African letters) | rare; no standard mapping |

- Letters that do decompose, so no manual map is needed: ä ö ü å ç ñ é è ê ë á à â ã ß-free accents, ő ű, ů, ř, š, ž, č, ě, ń, ś, ź, ż, ą, ę (ogonek), ș ț (comma below), ğ, ş, İ. Ö, Ü, Ä decompose as well, but ä ö ü must be mapped to ae oe ue before NFD (project rule).
- Note: ø, đ, ł look like they have a stroke "overlay", but the Unicode data has no decomposition for them (the overlay marks U+0335 to U+0338 are not used in canonical mappings of these). Verified by knowledge of UnicodeData.txt; the exact set was not machine-checked in this run. A quick test in the phase (`norm.NFD.String("øłđðæœþıŋ")` equals the input) would settle it.
- Recommended: a small `strings.NewReplacer` with the pairs above (including ä→ae, ö→oe, ü→ue, ß→ss), applied after `strings.ToLower` and before `norm.NFD.String`.

## Not confirmed

- The no-decomposition list is from the Unicode data as remembered, not re-checked against UnicodeData.txt in this run. The behaviour of v0.44.0 versus v0.43.0 for these functions was not compared (norm tables follow the Unicode version; a change there does not affect Latin letters).
