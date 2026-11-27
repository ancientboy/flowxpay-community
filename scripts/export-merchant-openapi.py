#!/usr/bin/env python3
"""Export only the supported merchant bearer endpoints and outgoing Webhook."""
import argparse
import json
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[1]
PATHS = (
    "/capabilities", "/payments", "/payments/{id}", "/payments/{id}/refunds",
    "/refunds/{id}", "/collection-exceptions",
)
OUTPUT = ROOT / "web/src/merchant-openapi.json"


def export():
    source = yaml.safe_load((ROOT / "docs/openapi.yaml").read_text())
    merchant = {key: source[key] for key in ("openapi", "info", "servers", "security", "webhooks")}
    merchant["paths"] = {path: source["paths"][path] for path in PATHS}
    merchant["components"] = {
        "securitySchemes": {"SandboxKey": source["components"]["securitySchemes"]["SandboxKey"]},
    }

    def collect(node):
        if isinstance(node, dict):
            if "$ref" in node:
                _, _, group, name = node["$ref"].split("/")
                group_output = merchant["components"].setdefault(group, {})
                if name not in group_output:
                    group_output[name] = source["components"][group][name]
                    collect(group_output[name])
            for value in node.values():
                collect(value)
        elif isinstance(node, list):
            for item in node:
                collect(item)

    collect(merchant["paths"])
    collect(merchant["webhooks"])
    return json.dumps(merchant, ensure_ascii=False, indent=2) + "\n"


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    content = export()
    if args.check:
        if OUTPUT.read_text() != content:
            parser.exit(1, "Merchant contract needs regeneration. Run scripts/export-merchant-openapi.py.\n")
    else:
        OUTPUT.write_text(content)
