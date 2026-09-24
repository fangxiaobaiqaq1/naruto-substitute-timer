#!/usr/bin/env python3
"""Validate the official avatar corpus and import only the A/S production subset.

All joins are by ID.  The source JSON index, CSV export, PNGs, and JSONP detail
snapshots are cross-checked before the output directory is changed.
"""
import argparse
import csv
import hashlib
import json
import pathlib
import re
import shutil
import struct
import sys
import zlib

RANKS = {"A", "S"}
PNG_SIGNATURE = b"\x89PNG\r\n\x1a\n"


def sha256(path):
    h = hashlib.sha256()
    with path.open("rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def read_jsonp(path):
    raw = path.read_text(encoding="utf-8")
    start, end = raw.find("{"), raw.rfind("}")
    if start < 0 or end < start:
        raise ValueError("not a JSONP object")
    return json.loads(raw[start : end + 1])


def validate_png(path):
    data = path.read_bytes()
    if not data.startswith(PNG_SIGNATURE):
        raise ValueError("bad PNG signature")
    pos, chunks, idat = len(PNG_SIGNATURE), [], bytearray()
    while pos + 12 <= len(data):
        length = struct.unpack(">I", data[pos : pos + 4])[0]
        end = pos + 12 + length
        if end > len(data):
            raise ValueError("truncated PNG chunk")
        kind, payload = data[pos + 4 : pos + 8], data[pos + 8 : pos + 8 + length]
        crc = struct.unpack(">I", data[pos + 8 + length : end])[0]
        if zlib.crc32(kind + payload) & 0xFFFFFFFF != crc:
            raise ValueError(f"bad {kind.decode('latin1')} CRC")
        chunks.append(kind)
        if kind == b"IHDR":
            if length != 13:
                raise ValueError("invalid IHDR")
            width, height = struct.unpack(">II", payload[:8])
            if not width or not height:
                raise ValueError("zero-sized image")
        elif kind == b"IDAT":
            idat.extend(payload)
        elif kind == b"IEND":
            if end != len(data):
                raise ValueError("trailing data after IEND")
            break
        pos = end
    if chunks[:1] != [b"IHDR"] or b"IDAT" not in chunks or chunks[-1:] != [b"IEND"]:
        raise ValueError("missing IHDR/IDAT/IEND")
    try:
        zlib.decompress(bytes(idat))
    except zlib.error as exc:
        raise ValueError(f"invalid IDAT stream: {exc}") from exc


def canonical_parts(entry):
    if entry["isSkin"]:
        role, variant = entry.get("baseNinjaName", "").strip(), entry.get("skinTitle", "").strip()
    else:
        role, variant = entry.get("ninja", "").strip(), entry.get("form", "").strip()
    # The source uses 「...」 to append a base form to some skin names.  Keep
    # that source text in the manifest, but use the unambiguous role plus the
    # explicit skin title as the runtime canonical mapping.
    role = re.split(r"[「[]", role, maxsplit=1)[0].strip()
    return role, variant


def canonical_name(entry):
    role, variant = canonical_parts(entry)
    return role if not variant else f"{role}[{variant}]"


def fail(failures, ident, reason):
    failures.append(f"{ident}: {reason}")


def validate_source(root):
    failures = []
    ambiguities = []
    index_path, csv_path = root / "avatars_index.json", root / "avatars_index.csv"
    index = json.loads(index_path.read_text(encoding="utf-8"))
    entries = index.get("entries")
    if not isinstance(entries, list):
        raise ValueError("avatars_index.json has no entries list")
    by_id = {}
    for entry in entries:
        ident = str(entry.get("id", ""))
        if ident in by_id:
            fail(failures, ident, "duplicate ID")
        by_id[ident] = entry
        if not re.fullmatch(r"\d{5,6}", ident):
            fail(failures, ident, "invalid ID")
        if not isinstance(entry.get("isSkin"), bool):
            fail(failures, ident, "isSkin is not boolean")
        role, variant = canonical_parts(entry)
        if not role:
            fail(failures, ident, "empty canonical role name")
        if entry["isSkin"] and not variant:
            fail(failures, ident, "empty canonical skin variant name")
        png = root / "avatars" / f"{ident}.png"
        if not png.is_file():
            fail(failures, ident, "missing PNG")
            continue
        try:
            validate_png(png)
        except ValueError as exc:
            fail(failures, ident, f"invalid PNG: {exc}")
        avatar = entry.get("avatar", {})
        if sha256(png) != avatar.get("sha256"):
            fail(failures, ident, "PNG SHA-256 mismatch")
        if png.stat().st_size != avatar.get("bytes"):
            fail(failures, ident, "PNG byte count mismatch")
        metadata = root / "details" / "metadata" / ("skin" if entry["isSkin"] else "ninja") / f"{ident}.json"
        if not metadata.is_file() or not metadata.stat().st_size:
            fail(failures, ident, "missing metadata")
            continue
        try:
            detail = read_jsonp(metadata)
            if entry["isSkin"]:
                core = detail["szjbxx"]
                checks = {"id": str(core["iFsId"]), "baseNinjaId": str(core["iNinjaId"]),
                          "baseNinjaName": core["sNinjaName"], "skinName": core["szbm"],
                          "skinTitle": core["sTitle"]}
            else:
                core = detail["zhanshi"]["rzzs"]
                # Ninja detail JSONP identifies the ID and role name; rank is supplied
                # by the separately archived official ninja-list row and checked against CSV below.
                checks = {"id": str(core["rzwyID"]), "ninja": core["rzzmc"]}
            for field, actual in checks.items():
                if str(entry.get(field, "")) != str(actual):
                    fail(failures, ident, f"metadata {field}={actual!r}, index={entry.get(field)!r}")
        except (KeyError, TypeError, ValueError, json.JSONDecodeError) as exc:
            fail(failures, ident, f"invalid metadata schema: {exc}")
        if entry["isSkin"]:
            base_id = entry.get("baseNinjaId", "")
            base = by_id.get(base_id)
            if not base or base.get("isSkin"):
                fail(failures, ident, f"baseNinjaId does not identify a base: {base_id!r}")
            elif entry.get("baseNinjaName") != base.get("ninja"):
                ambiguities.append({"id": ident, "baseNinjaId": base_id,
                                    "sourceBaseNinjaName": entry.get("baseNinjaName", ""),
                                    "baseRecordName": base.get("ninja", "")})

    with csv_path.open(encoding="utf-8-sig", newline="") as f:
        rows = list(csv.DictReader(f))
    csv_by_id = {}
    for row in rows:
        ident = row.get("id", "")
        if ident in csv_by_id:
            fail(failures, ident, "duplicate CSV ID")
        csv_by_id[ident] = row
    if set(csv_by_id) != set(by_id):
        fail(failures, "csv", f"ID set mismatch index={len(by_id)} csv={len(csv_by_id)}")
    fields = {"isSkin": lambda e: "1" if e["isSkin"] else "0", "ninja": lambda e: e.get("ninja", ""),
              "form": lambda e: e.get("form", ""), "rank": lambda e: e.get("rank", ""),
              "base_ninja_id": lambda e: e.get("baseNinjaId", ""), "base_ninja_name": lambda e: e.get("baseNinjaName", ""),
              "skin_name": lambda e: e.get("skinName", ""), "skin_title": lambda e: e.get("skinTitle", ""),
              "bytes": lambda e: str(e.get("avatar", {}).get("bytes", "")),
              "sha256": lambda e: e.get("avatar", {}).get("sha256", "")}
    for ident, entry in by_id.items():
        row = csv_by_id.get(ident)
        if not row:
            continue
        for field, expected in fields.items():
            if row.get(field, "") != expected(entry):
                fail(failures, ident, f"CSV {field}={row.get(field)!r}, index={expected(entry)!r}")

    bases = {ident for ident, e in by_id.items() if not e["isSkin"] and e.get("rank") in RANKS}
    selected = [e for e in entries if (not e["isSkin"] and e.get("rank") in RANKS) or (e["isSkin"] and e.get("baseNinjaId") in bases)]
    selected_names = {}
    for entry in selected:
        name = canonical_name(entry)
        selected_names.setdefault(name, []).append(entry["id"])
    for name, ids in selected_names.items():
        if len(ids) > 1:
            fail(failures, "selection", f"duplicate canonical mapping {name!r}: {ids}")
    return index, entries, by_id, bases, selected, failures, ambiguities


def make_manifest(root, index, by_id, bases, selected):
    def metadata_info(entry):
        path = root / "details" / "metadata" / ("skin" if entry["isSkin"] else "ninja") / f"{entry['id']}.json"
        return f"details/metadata/{'skin' if entry['isSkin'] else 'ninja'}/{entry['id']}.json", sha256(path)

    source_index = root / "avatars_index.json"
    source_csv = root / "avatars_index.csv"
    source_checksums = root / "CHECKSUMS.sha256"
    manifest_entries = []
    for entry in selected:
        md_path, md_sha = metadata_info(entry)
        role, variant = canonical_parts(entry)
        manifest_entries.append({
            "id": entry["id"], "isSkin": entry["isSkin"], "ninja": entry.get("ninja", ""),
            "form": entry.get("form", ""), "rank": entry.get("rank", ""),
            "skinTitle": entry.get("skinTitle", ""), "baseNinjaId": entry.get("baseNinjaId", ""),
            "baseNinjaName": entry.get("baseNinjaName", ""), "canonicalRole": role,
            "canonicalVariant": variant, "canonicalName": canonical_name(entry),
            "metadataPath": md_path, "metadataSha256": md_sha,
            "sha256": entry["avatar"]["sha256"], "bytes": entry["avatar"]["bytes"],
        })
    return {
        "sourceRoot": "<source-corpus>", "sourceIndex": "avatars_index.json",
        "sourceIndexSha256": sha256(source_index), "sourceCSV": "avatars_index.csv",
        "sourceCSVSha256": sha256(source_csv),
        "sourceChecksums": "CHECKSUMS.sha256" if source_checksums.exists() else "",
        "sourceChecksumsSha256": sha256(source_checksums) if source_checksums.exists() else "",
        "sourceGeneratedAt": index.get("generatedAt", ""), "sourceScope": index.get("scope", ""),
        "selection": "base rank A/S; skins whose baseNinjaId is a selected A/S base",
        "fullCorpusEntries": len(by_id), "selectedBaseEntries": len(bases),
        "selectedSkinEntries": sum(e["isSkin"] for e in selected), "entries": manifest_entries,
    }


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("source", help="source corpus directory")
    parser.add_argument("--output", default="assets/avatars")
    parser.add_argument("--validate-only", action="store_true", help="validate source without writing assets")
    args = parser.parse_args()
    root = pathlib.Path(args.source)
    try:
        index, entries, by_id, bases, selected, failures, ambiguities = validate_source(root)
    except (OSError, json.JSONDecodeError, ValueError) as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        return 1
    print(f"source entries={len(entries)} bases={len(entries)-sum(e['isSkin'] for e in entries)} skins={sum(e['isSkin'] for e in entries)}")
    print(f"selected entries={len(selected)} A/S bases={len(bases)} linked skins={sum(e['isSkin'] for e in selected)}")
    if failures:
        for item in failures:
            print(f"FAIL {item}", file=sys.stderr)
        return 1
    if not args.validate_only:
        out = pathlib.Path(args.output)
        manifest = make_manifest(root, index, by_id, bases, selected)
        manifest["sourceMetadataAmbiguities"] = ambiguities
        staging = out.with_name(out.name + ".staging")
        shutil.rmtree(staging, ignore_errors=True)
        staging.mkdir(parents=True)
        # Keep repository documentation when regenerating an existing corpus.
        readme = out / "README.md"
        if readme.is_file():
            shutil.copy2(readme, staging / readme.name)
        for entry in selected:
            shutil.copy2(root / "avatars" / f"{entry['id']}.png", staging / f"{entry['id']}.png")
        (staging / "index.json").write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        shutil.rmtree(out, ignore_errors=True)
        staging.rename(out)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
