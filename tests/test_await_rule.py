"""The await rule: across an await, code keeps a record's ID, not the record.

DEVELOPING.md (The state, Awaits) explains the rule. This test checks it in the
functions of `hlabd/api/` and `hlabd/lab.py`, reading their source: a name bound to a
state record must not be read or written after an await, unless it was bound again
after that await. Reading `name["id"]` is always fine: IDs never change.

A name holds a record when it is
- a parameter annotated with a record type of records.py (`X | None` too), or with a
  type parameter bound to one,
- bound to `lab.current(...)`, `lab.get_or_404(...)`, `state.get(...)`,
  `state[KIND][...]`, or a call of a function of hlabd whose return annotation is a
  record type,
- the loop variable over a list of records (`state[KIND].values()`, a copy of it, a
  comprehension over it, a parameter such as `list[Action]`).
Internet clients are left out: they never change once made, and fields that never
change may be read from a record held across an await. A nested function (the steps
of a plan) runs later, so the records it takes from the function around it count as
held across an await from its first line on.

A dataclass carries what one phase of a request hands to the next, often across an
await, so its fields hold IDs, not records (`test_no_dataclass_holds_a_record`).

The few deliberate exceptions carry a comment `# record: <why>`: on the line of a use
(for that use), or on the line that binds the name (for every use until the name is
bound again; for a parameter, its line in the signature; for a list, also its items).
A comment on a line of its own counts for the line below it.
"""

import ast
import io
import re
import tokenize
from dataclasses import dataclass, replace
from pathlib import Path

HLABD = Path(__file__).parents[1] / "hlabd"

# what a name holds: a record, a list of records, or a dict of them by ID (a kind)
RECORD, COLLECTION, MAPPING = "record", "collection", "mapping"

# a record inside a server's record that changes on its own
SUB_RECORDS = {"Nic"}
# records that never change once made (an Internet client has no update): fields that
# never change may be read from a record held across an await
UNCHANGING = {"Client"}
UNCHANGING_KINDS = {"CLIENTS"}

ALLOW = re.compile(r"#\s*record:\s*\S")


def record_types() -> set[str]:
    """Return the names of the record types: the kinds of `Record`, actions, NICs."""
    tree = ast.parse((HLABD / "records.py").read_text())
    names = {"Action", "Record", "ActionRecord", "ProtectedRecord", *SUB_RECORDS}
    for node in tree.body:
        if (
            isinstance(node, ast.Assign)
            and isinstance(node.targets[0], ast.Name)
            and node.targets[0].id == "Record"
        ):
            names |= {n.id for n in ast.walk(node.value) if isinstance(n, ast.Name)}
    return names - UNCHANGING


def annotation_kind(annotation: ast.expr | None, records: set[str]) -> str | None:
    """Return RECORD, COLLECTION or None for a type annotation."""
    match annotation:
        case ast.Name(id=name) if name in records:
            return RECORD
        case ast.BinOp(op=ast.BitOr(), left=left, right=right):
            return annotation_kind(left, records) or annotation_kind(right, records)
        case ast.Subscript(value=ast.Name(id=outer), slice=inner) if outer in (
            "list",
            "Iterable",
            "Iterator",
            "Sequence",
        ):
            if annotation_kind(inner, records) == RECORD:
                return COLLECTION
    return None


def returning_records(records: set[str]) -> dict[str, str]:
    """Return the functions of hlabd that return a record or a list of them."""
    found: dict[str, str] = {}
    for path in HLABD.rglob("*.py"):
        for node in ast.walk(ast.parse(path.read_text())):
            if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
                kind = annotation_kind(node.returns, records)
                if kind:
                    found[node.name] = kind
    return found


@dataclass(frozen=True)
class Binding:
    """What a name holds, whether an await came since, and whether that is allowed."""

    kind: str
    stale: bool = False
    allowed: bool = False  # bound on a line with `# record: ...`


type Env = dict[str, Binding]


def merge(*envs: Env | None) -> Env | None:
    """Join the names of several paths: stale if stale on any of them."""
    reached = [env for env in envs if env is not None]
    if not reached:
        return None
    joined: Env = {}
    for env in reached:
        for name, binding in env.items():
            old = joined.get(name)
            if old is not None:
                binding = Binding(
                    old.kind,
                    old.stale or binding.stale,
                    old.allowed and binding.allowed,
                )
            joined[name] = binding
    return joined


def awaited(env: Env) -> Env:
    """Mark every record held so far as held across an await."""
    return {name: replace(binding, stale=True) for name, binding in env.items()}


def stored_names(nodes: list[ast.stmt]) -> set[str]:
    """Return the names a function body binds, without nested functions."""
    names: set[str] = set()
    pending: list[ast.AST] = list(nodes)
    while pending:
        node = pending.pop()
        if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)):
            names.add(node.name)
            continue
        if isinstance(node, ast.Name) and isinstance(node.ctx, ast.Store):
            names.add(node.id)
        pending.extend(ast.iter_child_nodes(node))
    return names


def has_await(nodes: list[ast.AST]) -> bool:
    """Tell whether code awaits, without nested functions."""
    pending: list[ast.AST] = list(nodes)
    while pending:
        node = pending.pop()
        if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef, ast.Lambda)):
            continue
        if isinstance(node, (ast.Await, ast.AsyncWith, ast.AsyncFor)):
            return True
        pending.extend(ast.iter_child_nodes(node))
    return False


def unchanging(kind: ast.expr | None) -> bool:
    """Tell whether a kind constant names records that never change."""
    return isinstance(kind, ast.Name) and kind.id in UNCHANGING_KINDS


def is_state(node: ast.expr) -> bool:
    """Tell whether an expression is the state (`state`, `lab.state`, `self.state`)."""
    return (isinstance(node, ast.Name) and node.id == "state") or (
        isinstance(node, ast.Attribute) and node.attr == "state"
    )


class FunctionCheck:
    """Walk one function in the order it runs and collect uses of stale records."""

    def __init__(self, module: "ModuleCheck", function, outer: Env):
        """Start with the function's record parameters and the records it captured."""
        self.module = module
        self.function = function
        self.loops: list[tuple[list[Env], list[Env]]] = []  # (breaks, continues)
        records = module.records | {
            param.name
            for param in getattr(function, "type_params", [])
            if isinstance(param, ast.TypeVar)
            and annotation_kind(param.bound, module.records)
        }
        arguments = function.args
        params = [
            *arguments.posonlyargs,
            *arguments.args,
            *arguments.kwonlyargs,
            *filter(None, [arguments.vararg, arguments.kwarg]),
        ]
        local = stored_names(function.body) | {param.arg for param in params}
        # a nested function runs later: what it takes from outside is held across an
        # await from its first line on
        self.env: Env = {
            name: replace(binding, stale=True)
            for name, binding in outer.items()
            if name not in local
        }
        for param in params:
            kind = annotation_kind(param.annotation, records)
            if kind:
                self.env[param.arg] = Binding(kind, allowed=self.allowed(param.lineno))

    def allowed(self, line: int) -> bool:
        """Tell whether a line carries `# record: <why>`."""
        return line in self.module.allowed_lines

    def run(self) -> None:
        """Check the function body."""
        self.block(self.function.body, dict(self.env))

    # --- statements -------------------------------------------------------------------

    def block(self, statements: list[ast.stmt], env: Env | None) -> Env | None:
        """Run a block; None means the block cannot end normally (return, raise)."""
        for statement in statements:
            if env is None:
                return None
            env = self.statement(statement, env)
        return env

    def statement(self, node: ast.stmt, env: Env) -> Env | None:
        """Run one statement and return the names after it."""
        match node:
            case ast.FunctionDef() | ast.AsyncFunctionDef():
                FunctionCheck(self.module, node, env).run()
                env.pop(node.name, None)
                return env
            case ast.ClassDef():
                return env
            case ast.Assign(targets=targets, value=value):
                env = self.expression(value, env)
                for target in targets:
                    env = self.bind(target, value, env, node.lineno)
                return env
            case ast.AnnAssign(target=target, value=value):
                if value is None:
                    return env
                env = self.expression(value, env)
                return self.bind(target, value, env, node.lineno)
            case ast.AugAssign(target=target, value=value):
                env = self.expression(value, env)
                return self.expression(target, env, written=True)
            case ast.Return(value=value) | ast.Raise(exc=value):
                if value is not None:
                    self.expression(value, env)
                return None
            case ast.Break() | ast.Continue():
                breaks, continues = self.loops[-1]
                (breaks if isinstance(node, ast.Break) else continues).append(env)
                return None
            case ast.If(test=test, body=body, orelse=orelse):
                env = self.expression(test, env)
                return merge(self.block(body, dict(env)), self.block(orelse, dict(env)))
            case ast.For() | ast.AsyncFor():
                return self.for_loop(node, env)
            case ast.While(test=test):
                return self.loop(lambda head: self.expression(test, head), node, env)
            case ast.With() | ast.AsyncWith():
                return self.with_block(node, env)
            case ast.Try() | ast.TryStar():
                return self.try_block(node, env)
        for child in ast.iter_child_nodes(node):  # Expr, Delete, Assert, ...
            if isinstance(child, ast.expr):
                env = self.expression(child, env)
        return env

    def for_loop(self, node: ast.For | ast.AsyncFor, env: Env) -> Env | None:
        """Run a loop: over a list of records, its variable holds a record."""
        env = self.expression(node.iter, env)
        item = self.item_of(node.iter, env, node.lineno)
        # the list as it was when the loop began: an await in the body makes the
        # next item a record held across that await
        hidden = f"<loop {node.lineno}:{node.col_offset}>"
        if item is not None:
            env[hidden] = replace(item, kind=COLLECTION)

        def head(head_env: Env) -> Env:
            if isinstance(node, ast.AsyncFor):
                head_env = awaited(head_env)
            head_env = self.unbind(node.target, head_env)
            items = head_env.get(hidden)
            if items is not None and isinstance(node.target, ast.Name):
                head_env[node.target.id] = replace(items, kind=RECORD)
            return head_env

        return self.loop(head, node, env)

    def loop(self, head, node: ast.For | ast.AsyncFor | ast.While, env: Env):
        """Run a loop body twice: the second time with what the first left stale."""
        self.loops.append(([], []))
        breaks, continues = self.loops[-1]
        first = self.block(node.body, head(dict(env)))
        again = merge(env, first, *continues)
        assert again is not None
        second = self.block(node.body, head(dict(again)))
        self.loops.pop()
        ended = merge(again, second, *continues)
        assert ended is not None
        return merge(self.block(node.orelse, dict(ended)), *breaks)

    def with_block(self, node: ast.With | ast.AsyncWith, env: Env) -> Env | None:
        """Run a with block; `async with` awaits when it enters and when it leaves."""
        for item in node.items:
            env = self.expression(item.context_expr, env)
            if isinstance(node, ast.AsyncWith):
                env = awaited(env)
            if item.optional_vars is not None:
                env = self.unbind(item.optional_vars, env)
        after = self.block(node.body, env)
        return (
            awaited(after)
            if after is not None and isinstance(node, ast.AsyncWith)
            else after
        )

    def try_block(self, node: ast.Try | ast.TryStar, env: Env) -> Env | None:
        """Run try, except, else, finally; a handler may start after any await."""
        body = self.block(node.body, dict(env))
        failed = merge(env, body)
        assert failed is not None
        if has_await(list(node.body)):
            failed = awaited(failed)
        ends = [self.block(node.orelse, body) if body is not None else None]
        for handler in node.handlers:
            start = dict(failed)
            if handler.type is not None:
                start = self.expression(handler.type, start)
            if handler.name:
                start.pop(handler.name, None)
            ends.append(self.block(handler.body, start))
        after = merge(*ends)
        if not node.finalbody:
            return after
        # the finally block also runs after an error that goes on
        self.block(node.finalbody, merge(failed, after))
        return self.block(node.finalbody, after) if after is not None else None

    def bind(self, target: ast.expr, value: ast.expr, env: Env, line: int) -> Env:
        """Bind an assignment's target; a subscript or attribute target is a use."""
        if isinstance(target, ast.Name):
            found = self.kind_of(value, env)
            if found is None:
                env.pop(target.id, None)
            else:
                env[target.id] = replace(
                    found, allowed=found.allowed or self.allowed(line)
                )
            return env
        if isinstance(target, (ast.Tuple, ast.List)):
            if isinstance(value, (ast.Tuple, ast.List)) and len(value.elts) == len(
                target.elts
            ):
                for part, part_value in zip(target.elts, value.elts, strict=True):
                    env = self.bind(part, part_value, env, line)
                return env
            return self.unbind(target, env)
        return self.expression(target, env, written=True)

    def unbind(self, target: ast.expr, env: Env) -> Env:
        """Forget the names a target binds to something that is not a record."""
        for node in ast.walk(target):
            if isinstance(node, ast.Name):
                env.pop(node.id, None)
            elif isinstance(node, (ast.Subscript, ast.Attribute)):
                env = self.expression(node, env, written=True)
        return env

    # --- expressions ------------------------------------------------------------------

    def kind_of(self, node: ast.expr, env: Env) -> Binding | None:
        """Return what an expression gives: a record, a list or dict of them, or None.

        A dict of records (`state[KIND]`) gives records through `[...]`, `get`, `pop`
        and `values()`; iterated or copied with `list()`, it gives IDs.
        """
        match node:
            case ast.Name(id=name):
                return env.get(name)
            case ast.Await(value=value):
                found = self.kind_of(value, env)
                return replace(found, stale=False) if found else None
            case ast.Subscript(value=value, slice=index):
                if is_state(value):
                    return None if unchanging(index) else Binding(MAPPING)
                found = self.kind_of(value, env)
                if found is None or found.kind == RECORD:
                    return None
                if found.kind == COLLECTION and isinstance(index, ast.Slice):
                    return found
                return replace(found, kind=RECORD)
            case ast.Call(func=func, args=args):
                name = getattr(func, "attr", None) or getattr(func, "id", None)
                receiver = func.value if isinstance(func, ast.Attribute) else None
                first = self.kind_of(args[0], env) if args else None
                kind_given = args[0] if args else None
                if name in ("current", "get_or_404"):
                    return None if unchanging(kind_given) else Binding(RECORD)
                if receiver is not None and name in ("get", "pop", "values"):
                    if is_state(receiver) and name == "get":
                        return None if unchanging(kind_given) else Binding(RECORD)
                    found = self.kind_of(receiver, env)
                    if found is not None and found.kind == MAPPING:
                        kind = COLLECTION if name == "values" else RECORD
                        return replace(found, kind=kind)
                    if found is not None and found.kind == COLLECTION and name == "pop":
                        return replace(found, kind=RECORD)
                if first is not None and first.kind == COLLECTION:
                    if name in ("list", "sorted", "reversed"):
                        return first
                    if name == "next":
                        return replace(first, kind=RECORD)
                if name in self.module.returning:
                    return Binding(self.module.returning[name])
            case (
                ast.ListComp(elt=elt, generators=[generator])
                | ast.GeneratorExp(elt=elt, generators=[generator])
            ):
                found = self.kind_of(generator.iter, env)
                if (
                    found is not None
                    and found.kind == COLLECTION
                    and isinstance(elt, ast.Name)
                    and isinstance(generator.target, ast.Name)
                    and generator.target.id == elt.id
                ):
                    return found
            case ast.BoolOp(values=values):
                for value in values:
                    found = self.kind_of(value, env)
                    if found is not None:
                        return found
            case ast.IfExp(body=body, orelse=orelse):
                return self.kind_of(body, env) or self.kind_of(orelse, env)
        return None

    def item_of(self, iterable: ast.expr, env: Env, line: int) -> Binding | None:
        """Return what a loop over an expression binds: a record, or None."""
        found = self.kind_of(iterable, env)
        if found is None or found.kind != COLLECTION:
            return None
        return Binding(RECORD, found.stale, found.allowed or self.allowed(line))

    def expression(self, node: ast.expr, env: Env, written: bool = False) -> Env:
        """Visit an expression in the order it runs; an await makes records stale."""
        match node:
            case ast.Name(id=name):
                self.use(name, node.lineno, env)
                return env
            case ast.Subscript(value=ast.Name(), slice=ast.Constant(value="id")) if (
                not written
            ):
                return env  # IDs never change
            case ast.Await(value=value):
                return awaited(self.expression(value, env))
            case ast.Lambda():
                return env
            case ast.ListComp() | ast.SetComp() | ast.GeneratorExp() | ast.DictComp():
                return self.comprehension(node, env)
        for child in ast.iter_child_nodes(node):
            if isinstance(child, ast.expr):
                env = self.expression(child, env)
            elif isinstance(child, ast.keyword):
                env = self.expression(child.value, env)
        return env

    def comprehension(self, node, env: Env) -> Env:
        """Visit a comprehension; one that awaits is a loop and runs twice."""
        awaits = has_await([node])
        inner = dict(env)
        for repeat in (False, True) if awaits else (False,):
            for generator in node.generators:
                if not repeat:
                    inner = self.expression(generator.iter, inner)
                item = self.item_of(generator.iter, inner, node.lineno)
                inner = self.unbind(generator.target, inner)
                if item is not None and isinstance(generator.target, ast.Name):
                    inner[generator.target.id] = replace(
                        item, stale=item.stale or repeat
                    )
                for condition in generator.ifs:
                    inner = self.expression(condition, inner)
            parts = (
                [node.key, node.value] if isinstance(node, ast.DictComp) else [node.elt]
            )
            for part in parts:
                inner = self.expression(part, inner)
        return awaited(env) if awaits else env

    def use(self, name: str, line: int, env: Env) -> None:
        """Note the use of a record that was held across an await."""
        binding = env.get(name)
        if binding is None or not binding.stale or binding.allowed:
            return
        if self.allowed(line) or binding.kind == MAPPING:
            return
        self.module.found.add((line, name))


class ModuleCheck:
    """Check every function of a module, nested ones included."""

    def __init__(self, source: str, records: set[str], returning: dict[str, str]):
        """Read the module and its `# record:` comments."""
        self.source = source
        self.records, self.returning = records, returning
        # the comment's line; a comment on a line of its own also covers the next
        self.allowed_lines: set[int] = set()
        lines = source.splitlines()
        for token in tokenize.generate_tokens(io.StringIO(source).readline):
            if token.type == tokenize.COMMENT and ALLOW.search(token.string):
                line = token.start[0]
                own_line = not lines[line - 1][: token.start[1]].strip()
                self.allowed_lines |= {line, line + 1} if own_line else {line}
        self.found: set[tuple[int, str]] = set()

    def run(self) -> list[tuple[int, str]]:
        """Return the lines and names of records used across an await."""
        for node in ast.parse(self.source).body:
            functions = node.body if isinstance(node, ast.ClassDef) else [node]
            for function in functions:
                if isinstance(function, (ast.FunctionDef, ast.AsyncFunctionDef)):
                    FunctionCheck(self, function, {}).run()
        return sorted(self.found)


def check(source: str) -> list[str]:
    """Return `line: name` for each use of a record held across an await."""
    records = record_types()
    found = ModuleCheck(source, records, returning_records(records)).run()
    return [f"{line}: {name}" for line, name in found]


def record_fields(source: str) -> list[str]:
    """Return `line: field` for each field of a dataclass annotated with a record.

    A field whose line (or the line above) has a `# record:` comment is left out.
    """
    module = ModuleCheck(source, set(), {})
    records = record_types()
    found = []
    for node in ast.walk(ast.parse(source)):
        if not isinstance(node, ast.ClassDef) or not any(
            "dataclass" in ast.unparse(d) for d in node.decorator_list
        ):
            continue
        for item in node.body:
            if (
                isinstance(item, ast.AnnAssign)
                and isinstance(item.target, ast.Name)
                and annotation_kind(item.annotation, records)
                and item.lineno not in module.allowed_lines
            ):
                found.append(f"{item.lineno}: {item.target.id}")
    return found


def test_no_dataclass_holds_a_record():
    # a dataclass hands data from one phase to the next, across awaits: IDs only
    paths = sorted((HLABD / "api").rglob("*.py")) + [HLABD / "lab.py"]
    found = [
        f"{path.relative_to(HLABD.parent).as_posix()}:{entry}"
        for path in paths
        for entry in record_fields(path.read_text())
    ]
    assert found == []


def test_a_record_in_a_dataclass_field_is_found():
    source = """
@dataclass(frozen=True)
class Request:
    volumes: list[Volume]
    server_id: int
"""
    assert record_fields(source) == ["4: volumes"]


def test_no_record_is_used_across_an_await():
    # DEVELOPING.md, The state, Awaits: keep the ID across an await and look the
    # record up again; `# record: <why>` marks the few deliberate exceptions
    paths = sorted((HLABD / "api").rglob("*.py")) + [HLABD / "lab.py"]
    found = [
        f"{path.relative_to(HLABD.parent).as_posix()}:{entry}"
        for path in paths
        for entry in check(path.read_text())
    ]
    assert found == []


def test_a_record_used_after_an_await_is_found():
    source = """
async def steps(lab: Lab, server: Server) -> None:
    await lab.sleep(1)
    lab.set_status(server, "off")
"""
    assert check(source) == ["4: server"]


def test_a_record_looked_up_again_or_read_for_its_id_is_fine():
    source = """
async def steps(lab: Lab, server: Server) -> None:
    await lab.sleep(1)
    sid = server["id"]
    server = lab.current(SERVERS, sid)
    lab.set_status(server, "off")
"""
    assert check(source) == []


def test_the_steps_of_a_plan_may_not_use_the_records_of_the_plan():
    source = """
async def poweroff(lab: Lab, server: Server, body: dict) -> Plan:
    async def steps():
        lab.set_status(server, "off")

    return Plan(steps, server=server)
"""
    assert check(source) == ["4: server"]


def test_a_loop_that_awaits_may_not_use_the_records_of_a_copied_list():
    source = """
async def restart(lab: Lab) -> None:
    for server in list(lab.state[SERVERS].values()):
        await lab.blocking(work, server["id"])
        lab.set_status(server, "off")
    for sid in list(lab.state[SERVERS]):
        await lab.blocking(work, sid)
"""
    assert check(source) == ["5: server"]


def test_an_except_block_may_follow_any_await():
    source = """
async def start(lab: Lab, sid: int) -> None:
    server = lab.current(SERVERS, sid)
    try:
        await lab.blocking(work, sid)
        server = lab.current(SERVERS, sid)
    except Exception:
        log.error(server["name"])
"""
    assert check(source) == ["8: server"]


def test_a_record_comment_allows_a_use_or_a_binding():
    source = """
async def remove(lab: Lab, server: Server) -> None:
    await lab.sleep(1)
    tear_down(server)  # record: torn down as last seen
    # record: torn down as last seen
    other = lab.current(SERVERS, 1)
    await lab.sleep(1)
    tear_down(other)
    await lab.sleep(1)
    tear_down(server)
"""
    assert check(source) == ["10: server"]  # one use only
