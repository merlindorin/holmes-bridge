#!/usr/bin/env python3
"""Trim the official incident.io OpenAPI spec down to the operations this repo implements.

incident.io publishes a 6MB, 145-operation, 1029-schema spec. Generating all of
it would bury the handful of calls the mock serves and the bridge makes under
tens of thousands of lines of dead code. Vendoring a hand-written spec instead
would drift.

So: take the real spec, keep the operations in KEEP, transitively close over the
schemas they reference, and emit that. The result is wire-identical to
production for everything it covers, and small enough to read.

Usage:  python3 hack/trim-spec.py [--spec URL] [--out PATH]
"""

import argparse
import json
import re
import shutil
import subprocess
import sys
import urllib.request

SPEC_URL = "https://api.incident.io/v1/openapiV3.json"
OUT = "api/incidentio/openapi.yaml"

# (path, [methods]) to keep. Add here, then run `task spec:refresh`.
KEEP = [
    ("/v1/identity",                                   ["get"]),
    ("/v2/incidents",                                  ["get", "post"]),
    ("/v2/incidents/{id}",                             ["get"]),
    ("/v2/incidents/{id}/actions/edit",                ["post"]),
    ("/v2/alerts",                                     ["get"]),
    ("/v2/alerts/{id}",                                ["get"]),
    ("/v2/alerts/{id}/actions/resolve",                ["post"]),
    ("/v2/incident_alerts",                            ["get"]),
    ("/v2/alert_events/http/{alert_source_config_id}", ["post"]),
    ("/v2/incident_updates",                           ["get", "post"]),
    ("/v2/incident_timeline_items",                    ["get", "post"]),
    ("/v2/incident_timeline_items/{id}",               ["patch"]),
    ("/v3/actions",                                    ["get", "post"]),
    ("/v3/actions/{id}",                               ["get", "put", "delete"]),
    ("/v3/follow_ups",                                 ["get", "post"]),
    ("/v3/follow_ups/{id}",                            ["get", "put", "delete"]),
    ("/v1/severities",                                 ["get"]),
    ("/v1/severities/{id}",                            ["get"]),
    ("/v1/incident_statuses",                          ["get"]),
    ("/v1/incident_statuses/{id}",                     ["get"]),
    ("/v2/incident_roles",                             ["get"]),
    ("/v2/incident_roles/{id}",                        ["get"]),
    ("/v2/users",                                      ["get"]),
    ("/v2/users/{id}",                                 ["get"]),
    ("/v2/catalog_types",                              ["get"]),
    ("/v2/catalog_types/{id}",                         ["get"]),
    ("/v2/catalog_entries",                            ["get"]),
    ("/v2/catalog_entries/{id}",                       ["get"]),
]

# Upstream operationIds look like "Incidents V2#List", which sanitises into
# unreadable Go. These are the names we want instead; anything not listed falls
# back to a title-cased squash of the original.
OPERATION_IDS = {
    "Alerts V2#ListIncidentAlerts": "AlertsV2ListIncidentAlerts",
    "Alert Events V2#CreateHTTP":   "AlertEventsV2CreateHTTP",
    "Catalog V2#ListTypes":         "CatalogV2ListTypes",
    "Catalog V2#ShowType":          "CatalogV2ShowType",
    "Catalog V2#ListEntries":       "CatalogV2ListEntries",
    "Catalog V2#ShowEntry":         "CatalogV2ShowEntry",
}


def strip_examples(node):
    """Drop example blocks. They are most of the spec's bytes and none of its meaning."""
    if isinstance(node, dict):
        return {k: strip_examples(v) for k, v in node.items() if k not in ("example", "examples")}
    if isinstance(node, list):
        return [strip_examples(v) for v in node]
    return node


def collect_refs(node, out):
    if isinstance(node, dict):
        ref = node.get("$ref")
        if isinstance(ref, str) and ref.startswith("#/components/schemas/"):
            out.add(ref.rsplit("/", 1)[1])
        for value in node.values():
            collect_refs(value, out)
    elif isinstance(node, list):
        for value in node:
            collect_refs(value, out)


def fetch(url):
    """Fetch the spec, preferring urllib and falling back to curl.

    Some Python installs (notably python.org builds on macOS) ship without a CA
    bundle, so urllib fails on any HTTPS URL. curl uses the system trust store
    and is present everywhere this runs.
    """
    try:
        with urllib.request.urlopen(url, timeout=120) as response:
            return json.load(response)
    except Exception as err:  # noqa: BLE001 - any failure is worth the fallback
        print(f"urllib failed ({err}); falling back to curl", file=sys.stderr)

    if shutil.which("curl") is None:
        sys.exit("could not fetch the spec: urllib failed and curl is not installed")

    result = subprocess.run(
        ["curl", "-sSL", "--max-time", "120", url],
        capture_output=True, text=True, check=False,
    )
    if result.returncode != 0:
        sys.exit(f"curl failed to fetch the spec: {result.stderr.strip()}")

    return json.loads(result.stdout)


def operation_id(original):
    if original in OPERATION_IDS:
        return OPERATION_IDS[original]
    return re.sub(r"[^A-Za-z0-9]+", " ", original).title().replace(" ", "")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--spec", default=SPEC_URL)
    parser.add_argument("--out", default=OUT)
    args = parser.parse_args()

    print(f"fetching {args.spec}", file=sys.stderr)
    source = fetch(args.spec)

    schemas = source["components"]["schemas"]
    paths, missing, seen_ids = {}, [], set()

    for path, methods in KEEP:
        if path not in source["paths"]:
            missing.append(path)
            continue

        entry = {}
        for method in methods:
            if method not in source["paths"][path]:
                missing.append(f"{method.upper()} {path}")
                continue

            operation = strip_examples(source["paths"][path][method])
            name = operation_id(operation.get("operationId", ""))
            if name in seen_ids:
                sys.exit(f"duplicate operationId {name} on {method.upper()} {path}")
            seen_ids.add(name)
            operation["operationId"] = name
            entry[method] = operation

        if "parameters" in source["paths"][path]:
            entry["parameters"] = strip_examples(source["paths"][path]["parameters"])

        paths[path] = entry

    if missing:
        sys.exit(f"these operations are no longer in the upstream spec: {missing}")

    kept, frontier = set(), set()
    collect_refs(paths, frontier)
    while frontier:
        name = frontier.pop()
        if name in kept or name not in schemas:
            continue
        kept.add(name)
        nested = set()
        collect_refs(schemas[name], nested)
        frontier |= nested - kept

    document = {
        "openapi": "3.0.3",
        "info": {
            "title": "incident.io (subset)",
            "version": "1.0.0",
            "description": (
                "Wire-compatible subset of the incident.io REST API, trimmed from the "
                f"official specification at {SPEC_URL}.\n\n"
                "Generated by hack/trim-spec.py — do not edit by hand. To change what is "
                "covered, edit KEEP in that script and run `task spec:refresh`."
            ),
        },
        "servers": [{"url": "/", "description": "incident.io"}],
        "security": [{"bearerAuth": []}],
        "paths": paths,
        "components": {
            "securitySchemes": {
                "bearerAuth": {
                    "type": "http",
                    "scheme": "bearer",
                    "description": "incident.io API key",
                }
            },
            "schemas": {name: strip_examples(schemas[name]) for name in sorted(kept)},
        },
    }

    write_yaml(document, args.out)
    print(f"wrote {args.out}: {len(seen_ids)} operations, {len(kept)} schemas", file=sys.stderr)


def write_yaml(document, out):
    """Emit YAML via PyYAML, or fall back to yq, whichever the machine has."""
    try:
        import yaml
    except ImportError:
        pass
    else:
        with open(out, "w", encoding="utf-8") as handle:
            yaml.safe_dump(document, handle, sort_keys=False, default_flow_style=False, width=100)
        return

    if shutil.which("yq") is None:
        sys.exit("need either PyYAML (pip install pyyaml) or yq (brew install yq) to write YAML")

    result = subprocess.run(
        ["yq", "-P", "-p=json", "-o=yaml"],
        input=json.dumps(document), capture_output=True, text=True, check=False,
    )
    if result.returncode != 0:
        sys.exit(f"yq failed to convert the trimmed spec: {result.stderr.strip()}")

    with open(out, "w", encoding="utf-8") as handle:
        handle.write(result.stdout)


if __name__ == "__main__":
    main()
