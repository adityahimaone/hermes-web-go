#!/usr/bin/env python3
from __future__ import annotations
"""Reproducibly extract route literals from handle_* dispatch branches."""
import ast
import json
import sys
from pathlib import Path

METHODS = {
    "handle_get": "GET",
    "handle_post": "POST",
    "handle_patch": "PATCH",
    "handle_delete": "DELETE",
    "handle_put": "PUT",
}


def _dispatch_tests(function: ast.FunctionDef | ast.AsyncFunctionDef):
    """Yield direct dispatch condition expressions, skipping nested helpers."""
    stack: list[ast.AST] = list(function.body)
    while stack:
        node = stack.pop()
        if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef, ast.Lambda)):
            continue
        if isinstance(node, ast.If):
            if any(isinstance(value, ast.Attribute) and value.attr == "path" for value in ast.walk(node.test)):
                yield node.test
            stack.extend(node.body)
            stack.extend(node.orelse)
        else:
            stack.extend(ast.iter_child_nodes(node))


def generate(source: Path) -> dict:
    tree = ast.parse(source.read_text(), filename=str(source))
    routes = {method: set() for method in METHODS.values()}
    for node in tree.body:
        if not isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)) or node.name not in METHODS:
            continue
        method = METHODS[node.name]
        for test in _dispatch_tests(node):
            for value in ast.walk(test):
                if isinstance(value, ast.Constant) and isinstance(value.value, str) and value.value.startswith("/"):
                    routes[method].add(value.value)
    # Broad static/fallback branches are not API dispatch literals. Keep share
    # prefixes because they are public route branches with distinct semantics.
    ignored = {
        "/api/", "/dashboard-plugins/", "/extensions/", "/", "/index.html",
        "/manifest.json", "/manifest.webmanifest", "/plugins/", "/session/",
        "/session/manifest.json", "/session/manifest.webmanifest", "/session/static/",
        "/static/",
    }
    for method in routes:
        routes[method] -= ignored
    ordered = {method: sorted(values) for method, values in routes.items()}
    source_label = "/".join(source.parts[-2:]) if len(source.parts) >= 2 else source.name
    return {
        "source": source.name,
        "source_label": source_label,
        "dispatch_functions": {method: function for function, method in METHODS.items()},
        "route_counts": {method: len(values) for method, values in ordered.items()},
        "total_unique_dispatch_literals": sum(map(len, ordered.values())),
        "routes": ordered,
    }


if __name__ == "__main__":
    if len(sys.argv) != 2:
        raise SystemExit("usage: generate_route_inventory.py ROUTES_PY")
    print(json.dumps(generate(Path(sys.argv[1])), indent=2, sort_keys=False) + "\n")
