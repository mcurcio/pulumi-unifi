#!/usr/bin/env python3
"""Generate the DISPOSABLE in-repo API reference from the Pulumi schema.

This is a stopgap. At the first real release the full, canonical API reference
lives on the Pulumi Registry; this generator and its single output page
(docs/site/reference.md) are then deletable in one step:

    rm docs/gen_reference.py docs/site/reference.md   # (and drop the nav entry)

By design it has ZERO coupling to the provider or SDK build: plain Python 3,
stdlib only, no pulumi imports. It reads the already-generated schema.json (a
gitignored build artifact produced by `make build`) and emits ONE markdown
file. Keeping it to a single page avoids MkDocs `--strict` "page not in nav"
failures and keeps the whole thing trivially disposable.

Determinism: everything is sorted by key, so re-running produces byte-identical
output that diffs cleanly.
"""

import argparse
import json
import os
import sys

DEFAULT_SCHEMA = "provider/cmd/pulumi-resource-unifi/schema.json"
DEFAULT_OUT = "docs/site/reference.md"

BANNER = """<!--
  DO NOT EDIT BY HAND. This page is AUTO-GENERATED from the provider schema
  (schema.json) by docs/gen_reference.py. Any manual edits are overwritten on
  the next `make docs` / CI run.
-->

!!! warning "Auto-generated, disposable stopgap"
    This reference is **auto-generated** from the provider schema
    (`schema.json`) by `docs/gen_reference.py`. It is a **disposable stopgap**:
    at the first real release it is superseded by the canonical reference on the
    **[Pulumi Registry](https://www.pulumi.com/registry/)**. **Do not edit it by
    hand** — regenerate with `make docs`.
"""


def type_name(ref):
    """'#/types/unifi:sites/v1:FirewallPolicySource' -> 'FirewallPolicySource'."""
    return ref.rsplit(":", 1)[-1]


def render_type(prop):
    """Render a Pulumi property type spec as a short, readable string."""
    if not isinstance(prop, dict):
        return "-"
    if "$ref" in prop:
        ref = prop["$ref"]
        if ref.startswith("#/types/"):
            return "`%s`" % type_name(ref)
        # pulumi:pulumi:Any and similar special refs
        return "`%s`" % type_name(ref)
    t = prop.get("type")
    if t == "array":
        return "List&lt;%s&gt;" % _inner(render_type(prop.get("items", {})))
    if t == "object":
        ap = prop.get("additionalProperties")
        if ap is not None:
            return "Map&lt;%s&gt;" % _inner(render_type(ap))
        return "`object`"
    if t:
        return "`%s`" % t
    return "`any`"


def _inner(rendered):
    """Strip surrounding backticks so nested renders read as List<Foo>."""
    if rendered.startswith("`") and rendered.endswith("`"):
        return rendered[1:-1]
    return rendered


def esc(text):
    """Escape a description for a single markdown table cell."""
    if not text:
        return ""
    return " ".join(str(text).split()).replace("|", "\\|")


def render_table(properties, required):
    """Render a name/type/required/description table, sorted by name."""
    req = set(required or [])
    lines = [
        "| Name | Type | Required | Description |",
        "| --- | --- | --- | --- |",
    ]
    for name in sorted(properties):
        prop = properties[name] or {}
        lines.append(
            "| `%s` | %s | %s | %s |"
            % (
                name,
                render_type(prop),
                "yes" if name in req else "no",
                esc(prop.get("description")),
            )
        )
    return "\n".join(lines)


def render_resources(resources):
    out = ["## Resources", ""]
    if not resources:
        out.append("_No resources in the current schema._")
        out.append("")
        return out
    for token in sorted(resources):
        res = resources[token]
        out.append("### `%s`" % token)
        out.append("")
        if res.get("description"):
            out.append(esc(res["description"]))
            out.append("")
        inputs = res.get("inputProperties", {})
        if inputs:
            out.append("**Inputs**")
            out.append("")
            # Resources carry input-required under `requiredInputs`; fall back
            # to `required` for robustness against older/other shapes.
            out.append(
                render_table(inputs, res.get("requiredInputs", res.get("required")))
            )
            out.append("")
        outputs = res.get("properties", {})
        if outputs:
            out.append("**Outputs**")
            out.append("")
            out.append(render_table(outputs, res.get("required")))
            out.append("")
    return out


def render_function_io(io):
    """A function's inputs/outputs is either {properties,required} or a $ref."""
    if not io:
        return None
    if "$ref" in io:
        return "Returns: %s" % render_type(io)
    props = io.get("properties", {})
    if not props:
        return None
    return render_table(props, io.get("required"))


def render_functions(functions):
    out = ["## Data Sources", ""]
    if not functions:
        out.append("_No data sources in the current schema._")
        out.append("")
        return out
    for token in sorted(functions):
        fn = functions[token]
        out.append("### `%s`" % token)
        out.append("")
        if fn.get("description"):
            out.append(esc(fn["description"]))
            out.append("")
        inputs = render_function_io(fn.get("inputs"))
        if inputs:
            out.append("**Inputs**")
            out.append("")
            out.append(inputs)
            out.append("")
        outputs = render_function_io(fn.get("outputs"))
        if outputs:
            out.append("**Outputs**")
            out.append("")
            out.append(outputs)
            out.append("")
    return out


def build(schema):
    resources = schema.get("resources", {})
    functions = schema.get("functions", {})
    lines = ["# API Reference", ""]
    lines.append(BANNER)
    lines.append("")
    lines.append(
        "Generated from schema version `%s` covering **%d resources** and "
        "**%d data sources**."
        % (schema.get("version") or "unreleased", len(resources), len(functions))
    )
    lines.append("")
    lines.extend(render_resources(resources))
    lines.extend(render_functions(functions))
    # Single trailing newline, deterministic.
    return "\n".join(lines).rstrip("\n") + "\n"


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--schema", default=DEFAULT_SCHEMA, help="path to schema.json")
    parser.add_argument("--out", default=DEFAULT_OUT, help="output markdown path")
    args = parser.parse_args(argv)

    if not os.path.exists(args.schema):
        sys.stderr.write(
            "error: schema not found at %s; run `make build` first\n" % args.schema
        )
        return 1

    with open(args.schema, encoding="utf-8") as fh:
        schema = json.load(fh)

    content = build(schema)
    os.makedirs(os.path.dirname(args.out), exist_ok=True)
    with open(args.out, "w", encoding="utf-8") as fh:
        fh.write(content)

    sys.stderr.write(
        "wrote %s (%d resources, %d data sources)\n"
        % (args.out, len(schema.get("resources", {})), len(schema.get("functions", {})))
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
