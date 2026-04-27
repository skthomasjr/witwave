"""Helper for scripts/check-prom-rule-metrics.sh (#1682).

Reads rendered chart YAML from stdin, extracts witwave-prefixed metric
names from PrometheusRule alert expressions, and verifies each appears
in at least one production source file. Exits 0 on clean, 1 on any
unresolved metric.

Run via the shell wrapper, not directly — the wrapper handles helm
rendering + PATH guards. Kept as a separate file (rather than inlined
in the shell script) so its punctuation can't collide with bash quoting.
"""

from __future__ import annotations

import os
import pathlib
import re
import sys

REPO_ROOT = pathlib.Path(os.environ.get("REPO_ROOT", "."))

WITWAVE_PREFIXES = (
    "backend_",
    "harness_",
    "witwaveagent_",
    "witwaveprompt_",
    "mcp_",
)


def _extract_expressions(rendered: str) -> list[str]:
    """Return every PromQL expr block found in PrometheusRule docs.

    Uses yaml.safe_load_all so we only ever look at structured rule
    definitions — never at description/summary/annotation text, which
    legitimately mentions historical metric names that no longer exist
    (and would cause false-positive failures here).
    """
    try:
        import yaml
    except ImportError:
        # Fall back to a degraded line walker if PyYAML isn't on PATH.
        # This is a best-effort path for callers running outside the
        # operator's normal Python env; CI installs PyYAML.
        return _extract_expressions_lines(rendered)

    expressions: list[str] = []
    for doc in yaml.safe_load_all(rendered):
        if not isinstance(doc, dict):
            continue
        if doc.get("kind") != "PrometheusRule":
            continue
        spec = doc.get("spec") or {}
        groups = spec.get("groups") or []
        for grp in groups:
            for rule in grp.get("rules") or []:
                expr = rule.get("expr")
                if isinstance(expr, str):
                    expressions.append(expr)
    return expressions


def _extract_expressions_lines(rendered: str) -> list[str]:
    """Fallback line-walker for environments without PyYAML."""
    expressions: list[str] = []
    in_rule = False
    in_expr = False
    expr_lines: list[str] = []

    for line in rendered.splitlines():
        if line.startswith("kind:"):
            in_rule = line.strip() == "kind: PrometheusRule"
            if expr_lines:
                expressions.append("\n".join(expr_lines))
                expr_lines = []
            in_expr = False
            continue
        if not in_rule:
            continue
        stripped = line.strip()
        if stripped.startswith("expr:"):
            if expr_lines:
                expressions.append("\n".join(expr_lines))
                expr_lines = []
            rest = stripped[len("expr:"):].strip()
            if rest in ("|", ">"):
                in_expr = True
            else:
                expressions.append(rest)
            continue
        if in_expr:
            if re.match(r"^[ ]{0,8}[a-z_][a-z0-9_]*:", line):
                in_expr = False
                if expr_lines:
                    expressions.append("\n".join(expr_lines))
                    expr_lines = []
                continue
            expr_lines.append(line)

    if expr_lines:
        expressions.append("\n".join(expr_lines))
    return expressions


_PROMETHEUS_AUTO_SUFFIXES = ("_bucket", "_count", "_sum")


def _strip_prometheus_suffix(name: str) -> str:
    """Strip the Prometheus client library's auto-generated histogram /
    summary suffixes so a histogram declared as ``foo_seconds`` resolves
    even when the alert references ``foo_seconds_bucket``."""
    for suffix in _PROMETHEUS_AUTO_SUFFIXES:
        if name.endswith(suffix):
            return name[: -len(suffix)]
    return name


def _collect_metrics(expressions: list[str]) -> set[str]:
    """Tokenize expressions and keep only witwave-owned prefixes.

    Strips Prometheus auto-generated suffixes (_bucket, _count, _sum)
    before returning so histogram base names match their declarations.
    """
    ident_re = re.compile(r"[A-Za-z_][A-Za-z0-9_]*")
    metrics: set[str] = set()
    for expr in expressions:
        for tok in ident_re.findall(expr):
            if tok.startswith(WITWAVE_PREFIXES):
                metrics.add(_strip_prometheus_suffix(tok))
    return metrics


def _read_sources() -> tuple[str, int]:
    """Concatenate every plausible source file into a single haystack
    string for grep. Returns (blob, file_count)."""
    search_globs = [
        "backends/*/metrics.py",
        "harness/metrics.py",
        "harness/**/*.py",
        "tools/*/metrics.py",
        "tools/*/server.py",
        "shared/*.py",
        "operator/internal/controller/metrics.go",
        "operator/internal/controller/*.go",
    ]
    candidates: set[pathlib.Path] = set()
    for glob in search_globs:
        for path in REPO_ROOT.glob(glob):
            if path.is_file():
                candidates.add(path)

    blob_parts: list[str] = []
    for path in sorted(candidates):
        try:
            blob_parts.append(path.read_text(encoding="utf-8", errors="replace"))
        except OSError:
            continue
    return "\n".join(blob_parts), len(candidates)


def main() -> int:
    rendered = sys.stdin.read()
    if not rendered:
        print("check_prom_rule_metrics: empty stdin", file=sys.stderr)
        return 2

    expressions = _extract_expressions(rendered)
    metrics = _collect_metrics(expressions)

    if not metrics:
        print(
            "check_prom_rule_metrics: no witwave-prefixed metrics found in PrometheusRule",
            file=sys.stderr,
        )
        return 2

    blob, file_count = _read_sources()
    unresolved = sorted(m for m in metrics if m not in blob)
    if unresolved:
        print(
            "check_prom_rule_metrics: metrics referenced in PrometheusRule but not declared in source:",
            file=sys.stderr,
        )
        for m in unresolved:
            print(f"  - {m}", file=sys.stderr)
        print("", file=sys.stderr)
        print(
            f"Checked {len(metrics)} metric(s) against {file_count} source file(s).",
            file=sys.stderr,
        )
        return 1

    print(
        f"check_prom_rule_metrics: all {len(metrics)} witwave-prefixed metric(s) resolved"
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
