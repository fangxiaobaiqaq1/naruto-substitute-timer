#!/usr/bin/env python3
"""Machine-check the official 529-avatar corpus and its bounded A/S embed.

This is intentionally asset/index-only: it imports no recognition code and maps
all records by ID, never by directory or list order.
"""
import argparse
import hashlib
import importlib.util
import json
import pathlib
import sys

HERE = pathlib.Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("import_as_avatars", HERE / "import-as-avatars.py")
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


def add(failures, ident, reason):
    failures.append(f"{ident}: {reason}")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("root", help="source corpus directory")
    parser.add_argument("--embedded", default="assets/avatars")
    args = parser.parse_args()
    root, embedded = pathlib.Path(args.root), pathlib.Path(args.embedded)
    try:
        index, entries, by_id, bases, selected, failures, ambiguities = module.validate_source(root)
    except (OSError, ValueError, json.JSONDecodeError) as exc:
        print(f"FAIL source: {exc}", file=sys.stderr)
        return 1

    print(f"source entries={len(entries)} bases={len(entries) - sum(e['isSkin'] for e in entries)} skins={sum(e['isSkin'] for e in entries)}")
    print(f"source selected A/S bases={len(bases)} linked skins={sum(e['isSkin'] for e in selected)} total={len(selected)}")
    print(f"source base-name form suffixes flagged={len(ambiguities)}")

    try:
        manifest = json.loads((embedded / "index.json").read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        add(failures, "embedded", f"cannot read manifest: {exc}")
        manifest = {}
    manifest_entries = manifest.get("entries", [])
    manifest_by_id = {}
    for entry in manifest_entries:
        ident = str(entry.get("id", ""))
        if ident in manifest_by_id:
            add(failures, ident, "duplicate embedded ID")
        manifest_by_id[ident] = entry

    expected_ids = {entry["id"] for entry in selected}
    if set(manifest_by_id) != expected_ids:
        add(failures, "embedded", f"ID set mismatch manifest={len(manifest_by_id)} expected={len(expected_ids)}")
    png_ids = {path.stem for path in embedded.glob("*.png")}
    if png_ids != set(manifest_by_id):
        add(failures, "embedded", f"PNG ID set mismatch png={len(png_ids)} manifest={len(manifest_by_id)}")

    generated = module.make_manifest(root, index, by_id, bases, selected)
    for field in ("sourceRoot", "sourceIndex", "sourceIndexSha256", "sourceCSV", "sourceCSVSha256", "sourceChecksums", "sourceChecksumsSha256", "sourceGeneratedAt", "sourceScope", "selection", "fullCorpusEntries", "selectedBaseEntries", "selectedSkinEntries"):
        if manifest.get(field) != generated.get(field):
            add(failures, "embedded", f"provenance {field}={manifest.get(field)!r}, expected={generated.get(field)!r}")
    if manifest.get("sourceMetadataAmbiguities") != ambiguities:
        add(failures, "embedded", "source metadata ambiguity report differs from source")

    expected_by_id = {entry["id"]: entry for entry in generated["entries"]}
    names = {}
    for ident, entry in manifest_by_id.items():
        expected = expected_by_id.get(ident)
        if expected is None:
            continue
        for field, value in expected.items():
            if entry.get(field) != value:
                add(failures, ident, f"manifest {field}={entry.get(field)!r}, expected={value!r}")
        name = entry.get("canonicalName", "")
        if not name or not entry.get("canonicalRole", ""):
            add(failures, ident, "empty canonical mapping")
        names.setdefault(name, []).append(ident)
        png = embedded / f"{ident}.png"
        if not png.is_file():
            continue
        try:
            module.validate_png(png)
        except ValueError as exc:
            add(failures, ident, f"invalid embedded PNG: {exc}")
        if module.sha256(png) != entry.get("sha256"):
            add(failures, ident, "embedded PNG SHA-256 mismatch")
        if png.stat().st_size != entry.get("bytes"):
            add(failures, ident, "embedded PNG byte count mismatch")
    for name, ids in names.items():
        if len(ids) > 1:
            add(failures, "embedded", f"duplicate canonical mapping {name!r}: {ids}")

    print(f"embedded entries={len(manifest_by_id)} png={len(png_ids)} bytes={sum(e.get('bytes', 0) for e in manifest_entries)}")
    if failures:
        for failure in failures:
            print(f"FAIL {failure}", file=sys.stderr)
        return 1
    print("PASS corpus/index mapping and bounded embed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
