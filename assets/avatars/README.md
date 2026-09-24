# 内置 A/S 忍者头像

`assets/avatars/` is the production-bounded avatar corpus embedded in the Windows executable. It is generated from a reproducible public snapshot, not from directory order. Pass the local snapshot path explicitly:

```sh
python3 scripts/import-as-avatars.py <source-corpus> --output assets/avatars
python3 scripts/verify-avatar-corpus.py <source-corpus> --embedded assets/avatars
```

## Selection and counts

The reviewed source snapshot (`2026-09-23 20:54:47`) contains 529 public avatars: 454 ninja records and 75 skin records. The embed contains exactly:

- 183 non-skin records whose official-list `rank` is `A` or `S`;
- 59 skins whose `baseNinjaId` points to one of those 183 records;
- 242 valid PNGs, totaling 3,009,250 bytes.

The other 287 B/C or unlinked-skin PNGs intentionally remain external and are **not** embedded.

## ID mapping, provenance, and validation

`index.json` is keyed by stable `id`; neither importer nor validator relies on list or filesystem order. For every selected item it records the source role/form fields, a non-empty `canonicalRole` and `canonicalName`, source metadata path/checksum, PNG bytes/SHA-256, and complete source index/CSV/checksum provenance.

The machine-runnable validator checks all 529 source records for unique valid IDs, valid PNG structure, byte counts and SHA-256, corresponding JSONP metadata fields, CSV/index agreement, linked skin base IDs, and non-empty canonical names. It then verifies the embedded subset’s exact rank filter, skin linkage, unique canonical mappings, PNGs, checksums, byte counts, and provenance.

The source records 56 skin `baseNinjaName` values that append the base form in `「…」` (for example `宇智波鼬「须佐能乎」`) while their referenced base ID has role `宇智波鼬`. These are retained verbatim in `sourceMetadataAmbiguities` rather than silently rewritten; canonical production mappings deliberately use the role before `「` plus the explicit source `skinTitle`. This produces unique mappings without guessing a role or rank.
