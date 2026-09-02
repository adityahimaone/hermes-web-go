import json
import os
from pathlib import Path
import sys

sys.path.insert(0, str(Path(__file__).parents[1] / "tools"))
from generate_route_inventory import generate


def test_route_inventory_has_all_dispatch_methods_and_expected_counts():
    inventory = json.loads((Path(__file__).parents[1] / "testdata/route-inventory.json").read_text())
    assert inventory["route_counts"] == {"GET": 105, "POST": 139, "PATCH": 2, "DELETE": 3, "PUT": 1}
    assert inventory["total_unique_dispatch_literals"] == 250
    assert "/api/kanban/" in inventory["routes"]["GET"]
    assert "/api/sessions/gateway/stream" in inventory["routes"]["GET"]
    assert "/api/workspaces/filemap" in inventory["routes"]["GET"]
    assert "/api/kanban/" in inventory["routes"]["POST"]
    assert "/api/gateway/start" in inventory["routes"]["POST"]
    assert "/api/gateway/stop" in inventory["routes"]["POST"]
    assert "/api/gateway/restart" in inventory["routes"]["POST"]
    assert "/share" in inventory["routes"]["GET"]
    assert "/share/" in inventory["routes"]["GET"]


def test_inventory_matches_reproducible_source():
    configured = os.environ.get("PHASE0_SOURCE_ROUTES")
    if not configured:
        assert Path(json.loads((Path(__file__).parents[1] / "testdata/route-inventory.json").read_text())["source"]) .name == "routes.py"
        return
    source = Path(configured)
    assert source.exists(), f"PHASE0_SOURCE_ROUTES does not exist: {source}"
    inventory = json.loads((Path(__file__).parents[1] / "testdata/route-inventory.json").read_text())
    assert inventory == generate(source)


def test_generator_ignores_unrelated_nested_constants():
    source = Path("nested_routes.py")
    source.write_text(
        """
from urllib.parse import urlparse


def handle_get(handler):
    parsed = urlparse(handler.path)
    if parsed.path == '/api/real':
        def helper():
            return '/api/not-a-route'
        return helper()
    return None
"""
    )
    try:
        assert generate(source)["routes"] == {
            "GET": ["/api/real"], "POST": [], "PATCH": [], "DELETE": [], "PUT": []
        }
    finally:
        source.unlink()


def test_generator_includes_prefix_and_gateway_dispatch_literals():
    source = Path("prefix_routes.py")
    source.write_text(
        """
def handle_get(handler):
    parsed = handler.path
    if parsed.path == '/share' or parsed.path.startswith('/share/'):
        return None


def handle_post(handler):
    parsed = handler.path
    if parsed.path in {'/api/gateway/start', '/api/gateway/stop', '/api/gateway/restart'}:
        return None
"""
    )
    try:
        inventory = generate(source)
        assert inventory["routes"]["GET"] == ["/share", "/share/"]
        assert inventory["routes"]["POST"] == ["/api/gateway/restart", "/api/gateway/start", "/api/gateway/stop"]
    finally:
        source.unlink()
