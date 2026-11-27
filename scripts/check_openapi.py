#!/usr/bin/env python3
"""Check the FlowXPay merchant contract against registered routes and fixtures."""
import json
from pathlib import Path
import re

from jsonschema import Draft202012Validator
import yaml

ROOT = Path(__file__).resolve().parents[1]
VERBS = ("get", "post", "put", "patch", "delete", "head", "options")


class UniqueLoader(yaml.SafeLoader):
    def construct_mapping(self, node, deep=False):
        mapping = {}
        for key_node, value_node in node.value:
            key = self.construct_object(key_node, deep=deep)
            if key in mapping:
                raise ValueError(f"Duplicate YAML key {key!r} at line {key_node.start_mark.line + 1}")
            mapping[key] = self.construct_object(value_node, deep=deep)
        return mapping


def load_contract():
    return yaml.load((ROOT / "docs/openapi.yaml").read_text(), Loader=UniqueLoader)


def resolve(document, ref):
    assert ref.startswith("#/"), "The sandbox contract must resolve locally"
    target = document
    for key in ref[2:].split("/"):
        target = target[key.replace("~1", "/").replace("~0", "~")]
    return target


def references(node):
    if isinstance(node, dict):
        if "$ref" in node:
            yield node["$ref"]
        for value in node.values():
            yield from references(value)
    if isinstance(node, list):
        for value in node:
            yield from references(value)


def validate(document, schema, value):
    Draft202012Validator({**schema, "components": document["components"]},
                         format_checker=Draft202012Validator.FORMAT_CHECKER).validate(value)


def check(document):
    assert document["openapi"] == "3.1.0"
    ref_list = list(references(document))
    for ref in ref_list:
        resolve(document, ref)
    for schema in document["components"]["schemas"].values():
        Draft202012Validator.check_schema(schema)

    server_prefix = document["servers"][0]["url"].rstrip("/")
    operations = {}
    operation_ids = set()
    for path, item in document["paths"].items():
        for method in VERBS:
            if method not in item:
                continue
            operation = item[method]
            operation_id = operation["operationId"]
            assert operation_id not in operation_ids, operation_id
            operation_ids.add(operation_id)
            parameters = [resolve(document, p["$ref"]) if "$ref" in p else p
                          for p in item.get("parameters", []) + operation.get("parameters", [])]
            expected = set(re.findall(r"\{([^{}]+)\}", path))
            assert {p["name"] for p in parameters if p["in"] == "path" and p.get("required")} == expected, path
            servers = operation.get("servers", item.get("servers"))
            prefix = servers[0]["url"].rstrip("/") if servers else server_prefix
            key = (method.upper(), prefix + path)
            assert key not in operations, key
            operations[key] = operation.get("security", document["security"])

    settlement_root = "/sandbox/workspace/settlement/{account}/{currency}"
    actual = {}
    for unit in (ROOT / "backend/cmd/api").glob("*.go"):
        if unit.name.endswith("_test.go"):
            continue
        for line in unit.read_text().splitlines():
            match = re.search(r'HandleFunc\("([A-Z]+) (/[^"]+)", (.+)', line)
            if not match:
                match = re.search(r'HandleFunc\("([A-Z]+) "\+settlementRoot\+"([^"]+)", (.+)', line)
                if not match:
                    continue
                method, suffix, handler = match.groups()
                path = settlement_root + suffix
            else:
                method, path, handler = match.groups()
            if path.startswith(("/v1/", settlement_root, "/sandbox/channel-callbacks/",
                                "/sandbox/workspace/api-keys", "/sandbox/workspace/route-changes",
                                "/sandbox/workspace/chargebacks", "/sandbox/workspace/overview",
                                "/sandbox/workspace/payments", "/sandbox/workspace/setup", "/sandbox/workspace/channel-quotes", "/sandbox/workspace/settlement-summary",
                                "/sandbox/ops/dashboard")) or handler.startswith("s.platformOperation("):
                actual[method, path] = (
                    [{"WorkspaceSession": []}] if handler.startswith(("s.authorizeMember(", "s.settlementRoute(", "s.settlementReport(s.authorizeMember("))
                    else [{"SandboxKey": []}] if handler.startswith("s.authorize(")
                    else [{"PlatformSession": []}] if handler.startswith(("s.platformOperation(", "p.authorize("))
                    else [] if handler.startswith("s.receiveCallback") else None
                )
    assert operations.keys() == actual.keys(), {
        "missing": sorted(actual.keys() - operations.keys()), "extra": sorted(operations.keys() - actual.keys())
    }
    for key in operations:
        assert operations[key] == actual[key] and actual[key] is not None, key

    samples = document["paths"]["/payments"]["post"]["requestBody"]["content"]["application/json"]
    for example in samples["examples"].values():
        validate(document, samples["schema"], example["value"])
    statements = sorted((ROOT / "docs/fixtures/phase-5").glob("*.json"))
    for path in statements:
        validate(document, {"$ref": "#/components/schemas/SettlementStatement"}, json.loads(path.read_text()))
    assert document["webhooks"]["terminalBusinessEvent"]["post"]["security"] == []
    print(f"OpenAPI {document['info']['version']}: {len(operations)} registered operations and auth schemes, "
          f"{len(ref_list)} resolved references, {len(samples['examples']) + len(statements)} fixtures, 1 Webhook.")


if __name__ == "__main__":
    check(load_contract())
