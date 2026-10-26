"""The layers: the fabric works on plain data and never gets a record of the state."""

import ast
from pathlib import Path

HLABD = Path(__file__).parents[1] / "hlabd"


def record_type_names() -> set[str]:
    """Return the names of the record types in records.py (TypedDicts and unions)."""
    tree = ast.parse((HLABD / "records.py").read_text())
    names = set()
    for node in tree.body:
        if isinstance(node, ast.ClassDef):
            names.add(node.name)
        elif isinstance(node, ast.Assign):
            names.update(t.id for t in node.targets if isinstance(t, ast.Name))
        elif isinstance(node, ast.TypeAlias):
            names.add(node.name.id)
    return names


def annotations(tree: ast.AST) -> list[ast.expr]:
    """Return every annotation in a module: arguments, returns, fields, variables."""
    found: list[ast.expr] = []
    for node in ast.walk(tree):
        if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
            arguments = node.args
            every = [
                *arguments.posonlyargs,
                *arguments.args,
                *arguments.kwonlyargs,
                *filter(None, [arguments.vararg, arguments.kwarg]),
            ]
            found += [a.annotation for a in every if a.annotation is not None]
            if node.returns is not None:
                found.append(node.returns)
        elif isinstance(node, ast.AnnAssign):
            found.append(node.annotation)
    return found


def test_no_record_type_appears_in_the_fabric():
    # the fabric runs in threads; a record there could be read while the loop changes
    # it, so it gets plain values and its own small dataclasses (DEVELOPING.md, Where
    # code runs)
    records = record_type_names()
    assert {"Server", "Volume", "Client", "Record", "FirewallRule"} <= records
    found = []
    for path in sorted((HLABD / "fabric").rglob("*.py")):
        tree = ast.parse(path.read_text())
        found += [
            f"{path.name}:{node.lineno} imports records"
            for node in ast.walk(tree)
            if isinstance(node, ast.ImportFrom) and (node.module or "") == "records"
        ]
        for annotation in annotations(tree):
            for node in ast.walk(annotation):
                name = getattr(node, "id", None) or getattr(node, "attr", None)
                if isinstance(node, ast.Constant) and isinstance(node.value, str):
                    name = node.value  # a forward reference
                if name in records:
                    found.append(f"{path.name}:{node.lineno} {name}")
    assert found == []
