# 内置 A/S 忍者头像

`assets/avatars/` is the production-bounded avatar corpus embedded in the Windows executable. It is generated from a reproducible public snapshot, not from directory order. Pass the local snapshot path explicitly:

```sh
python3 scripts/import-as-avatars.py <source-corpus> --output assets/avatars
python3 scripts/verify-avatar-corpus.py <source-corpus> --embedded assets/avatars
```

## Selection and counts

The reviewed baseline snapshot (`2026-09-23 20:54:47`) contains 529 public avatars: 454 ninja records and 75 skin records. Only ID `90316` was refreshed on 2026-11-06 (see below); the other records and selection are unchanged. The embed contains exactly:

- 183 non-skin records whose official-list `rank` is `A` or `S`;
- 59 skins whose `baseNinjaId` points to one of those 183 records;
- 242 valid PNGs, totaling 3,003,344 bytes.

The other 287 B/C or unlinked-skin PNGs intentionally remain external and are **not** embedded.

## 2026-11-06: 九喇嘛水门 90316 avatar refresh

The user-supplied duel frame has the new portrait on both sides; the old 85×85 icon scored only about 0.63. The official [90316 detail](https://hyrz.qq.com/act/a20240723fashion/ninja-data/prod/90316.json) still identifies 波风水门[九喇嘛连结], rank S, and its `zhanshi.rzzs.lbtx` now points to [this 70×70 PNG](https://ninja.hyrz.qq.com/hyrz/prod/ninja/90316/ap.png?t=1793930251). The original downloaded bytes are used without resizing; recognition thresholds and the existing 13px row-offset policy are unchanged. Windows and Android consume this same asset.

- Fetched: `2026-11-06T14:48:47Z`; only this ID was fetched, not the roster.
- PNG: 10,661 bytes, SHA-256 `50cfa25a2fdaa9248c9a8ae1776de95fdfe3ae1d1ac2f2054ed871cc1308035a`.
- Detail: SHA-256 `663b486bea9d4a11de6ab9f9e09a0564dec23c1b94acfc98cf2a37db8031d81c`.
- Replaces PNG SHA-256 `25615699dd7617e738eb43b160927de6ea2095909876f1819a61d3776a1de478` (16,567 bytes). The September source archive is retained unchanged.
- The separately staged source `inbox/sources/hyrz-20261106-minato/` retains the other 528 records, updates this ID's PNG/detail and index/CSV checksums, and states the single-ID refresh in its scope. The normal importer and validator generated/verified the manifest; its provenance no longer claims an unmodified September snapshot.
- `TestMinatoKyubiDuelScreenshot` checks the private user frame at 960–2560 widths, with titles visible/occluded and repeated current-frame checks. This is screenshot coverage, not an entire live-match accuracy claim.

## ID mapping, provenance, and validation

`index.json` is keyed by stable `id`; neither importer nor validator relies on list or filesystem order. For every selected item it records the source role/form fields, a non-empty `canonicalRole` and `canonicalName`, source metadata path/checksum, PNG bytes/SHA-256, and complete source index/CSV/checksum provenance.

The machine-runnable validator checks all 529 source records for unique valid IDs, valid PNG structure, byte counts and SHA-256, corresponding JSONP metadata fields, CSV/index agreement, linked skin base IDs, and non-empty canonical names. It then verifies the embedded subset’s exact rank filter, skin linkage, unique canonical mappings, PNGs, checksums, byte counts, and provenance.

The source records 56 skin `baseNinjaName` values that append the base form in `「…」` (for example `宇智波鼬「须佐能乎」`) while their referenced base ID has role `宇智波鼬`. These are retained verbatim in `sourceMetadataAmbiguities` rather than silently rewritten; canonical production mappings deliberately use the role before `「` plus the explicit source `skinTitle`. This produces unique mappings without guessing a role or rank.
