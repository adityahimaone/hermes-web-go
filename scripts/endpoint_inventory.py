#!/usr/bin/env python3
"""Compare Python API paths against native Go router registrations."""
from __future__ import annotations

import argparse
import re
from pathlib import Path

API_PATH = re.compile(r"['\"](/api/[A-Za-z0-9_./{}:-]+)")
ROUTE_CALL = re.compile(r"\.(?:Get|Post|Put|Delete|Patch|Handle|HandleFunc)\(\s*['\"](/api/[^'\"]+)")


def normalized(path: str) -> str:
    return path.split("?", 1)[0].rstrip("/") or "/"


def python_paths(root: Path) -> set[str]:
    source = (root / "api" / "routes.py").read_text()
    # routes.py is the legacy dispatcher. Limit extraction to API literals and
    # ignore prose/comments that do not occur as quoted route values.
    return {normalized(path) for path in API_PATH.findall(source)}


def go_paths(root: Path) -> set[str]:
    paths: set[str] = set()
    for source_path in (root / "internal" / "httpserver").glob("*.go"):
        source = source_path.read_text()
        paths.update(normalized(path) for path in ROUTE_CALL.findall(source))
    registry = (root / "internal" / "proxy" / "registry.go").read_text()
    paths.update(normalized(path) for path in API_PATH.findall(registry))
    return paths


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--legacy-root", type=Path, default=Path("../hermes-webui-personal"))
    parser.add_argument("--go-root", type=Path, default=Path("."))
    parser.add_argument("--only-missing", action="store_true")
    args = parser.parse_args()

    legacy = python_paths(args.legacy_root.resolve())
    native = go_paths(args.go_root.resolve())
    missing = sorted(legacy - native)
    go_only = sorted(native - legacy)

    print(f"legacy_python_api={len(legacy)}")
    print(f"native_go_api={len(native)}")
    print(f"not_yet_migrated={len(missing)}")
    print(f"go_only={len(go_only)}")
    print("--- not yet migrated (legacy routes without Go registration) ---")
    for path in missing:
        print(path)
    if not args.only_missing:
        print("--- go-only / non-legacy paths ---")
        for path in go_only:
            print(path)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
