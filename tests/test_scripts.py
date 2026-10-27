"""The shell layer (scripts/*.sh), fast and without VMs.

Covered here:

- the verbs hlabd calls: every `shell.run(...)` / `shell.ok(...)` in hlabd/ names a
  verb of hlab.sh's `main` (or of box.sh's, which hlab.sh hands on) and passes an
  argument count its function takes; both sides are parsed from the sources, so there
  is no table to keep up; every verb is its function's name with dashes;
- box.sh's remove-tree refuses every path not strictly inside the box (HLAB_BOX);
- the nftables rulesets: the table inet hlab that `hlab.sh init-net` loads from
  hlab.nft with the addresses put in (ip, sysctl and nft stubbed), the set elements
  of fabric/faults.py and the firewall table of fabric/firewall.py, rendered from a
  lab with servers, a network, a client, faults and firewalls. `nft -c` needs
  CAP_NET_ADMIN for its full check (it reads the kernel's ruleset first); without it,
  only nft's parser runs (grammar, keywords, statement order) and the set names are
  compared in Python. `bin/hlab test` gives the test container CAP_NET_ADMIN, so the
  full check runs there;
- box-shell.sh's decisions (SFTP, read-only, rsync, scp, borg, df, who may connect),
  table-driven with stubbed commands on PATH;
- lib.sh's is_alive and stop_pid with a real process and a PID file.

Only the live tests (itests/) show that the commands really do their work: taps,
routes, rules, tc, QEMU, dnsmasq, the Storage Box's sshd, chroots and mounts.
"""

import ast
import os
import re
import shutil
import subprocess
from pathlib import Path

import pytest
from conftest import create_network, create_server, wait_action

from hlabd import config

REPO = Path(__file__).resolve().parents[1]
SCRIPTS = REPO / "scripts"
HLAB_SH = SCRIPTS / "hlab.sh"
BOX_SH = SCRIPTS / "box.sh"
BOX_SHELL_SH = SCRIPTS / "box-shell.sh"
LIB_SH = SCRIPTS / "lib.sh"

# an argument count without an upper bound
MANY = 1000

# the scripts run in bash; lib.sh reads /proc/<pid>/comm
NEEDS_BASH = pytest.mark.skipif(
    not shutil.which("bash"), reason="bash is not installed"
)
NEEDS_BASH_AND_PROC = pytest.mark.skipif(
    not shutil.which("bash") or not Path("/proc/self/comm").exists(),
    reason="lib.sh needs bash and /proc (Linux)",
)


def write_stub(bin_dir: Path, name: str, body: str) -> None:
    """Write an executable bash script `name` into bin_dir."""
    bin_dir.mkdir(exist_ok=True)
    path = bin_dir / name
    path.write_text(f"#!/bin/bash\n{body}\n")
    path.chmod(0o755)


# --- verbs ----------------------------------------------------------------------------


def shell_functions(script: Path) -> dict[str, str]:
    """Return the bodies of a script's functions by name."""
    pattern = re.compile(r"^([\w]+)\(\) \{\n(.*?)^\}", re.M | re.S)
    return dict(pattern.findall(script.read_text()))


def function_arity(body: str) -> tuple[int, int]:
    """Return how many arguments a shell function takes, as (least, most).

    Its parameters are its `local name="$N"` (required) and `local name="${N:-}"`
    (optional); `"$@"` after a shift takes any number more.
    """
    required, optional = 0, 0
    for line in body.splitlines():
        if not line.strip().startswith("local "):
            continue
        for index, default in re.findall(r"\$\{?(\d)(:-)?", line):
            if default:
                optional = max(optional, int(index))
            else:
                required = max(required, int(index))
    most = MANY if '"$@"' in body else max(required, optional)
    return required, most


def main_arms(script: Path) -> list[tuple[str, str, bool]]:
    """Return the one-verb arms of a script's main: (verb, function, passes "$@")."""
    return [
        (verb, function, bool(passes_args))
        for verb, function, passes_args in re.findall(
            r"^\s+([\w-]+)\) (\w+)( \"\$@\")? ;;$",
            shell_functions(script)["main"],
            re.M,
        )
    ]


def script_verbs(script: Path) -> dict[str, tuple[int, int]]:
    """Return the verbs of a script's main with the argument counts they take.

    A verb that passes no "$@" takes none; one that runs an outside command (nft, tc)
    takes any number.
    """
    functions = shell_functions(script)
    verbs = {}
    for verb, function, passes_args in main_arms(script):
        if not passes_args:
            assert function_arity(functions[function])[0] == 0, (
                f"{script.name}: {verb} calls {function} without its arguments"
            )
            verbs[verb] = (0, 0)
        elif function in functions:
            verbs[verb] = function_arity(functions[function])
        else:
            verbs[verb] = (0, MANY)
    return verbs


def handed_to_box() -> set[str]:
    """Return the verbs hlab.sh's main hands on to box.sh (one arm, `a | b ...)`)."""
    arm = re.search(
        r"^\s+([\w|\s\\-]+)\)\n"
        r'\s+exec "\$\(dirname "\$0"\)/box\.sh" "\$\{verb\}" "\$@"$',
        HLAB_SH.read_text(),
        re.M,
    )
    assert arm, "hlab.sh no longer hands verbs on to box.sh"
    return {verb.strip(" \\\n") for verb in arm.group(1).split("|")}


def hlabd_calls() -> list[tuple[str, str, int, bool]]:
    """Return every hlab.sh call in hlabd/: (where, verb, argument count, starred).

    A call is `<...>.shell.run|ok("verb", ...)` or `shell.run|ok(...)`; with a starred
    argument the count is the least.
    """
    calls = []
    for path in sorted((REPO / "hlabd").rglob("*.py")):
        for node in ast.walk(ast.parse(path.read_text(), str(path))):
            if not (
                isinstance(node, ast.Call)
                and isinstance(node.func, ast.Attribute)
                and node.func.attr in ("run", "ok")
                and node.args
                and isinstance(node.args[0], ast.Constant)
                and isinstance(node.args[0].value, str)
            ):
                continue
            target = node.func.value
            if not (
                (isinstance(target, ast.Attribute) and target.attr == "shell")
                or (isinstance(target, ast.Name) and target.id == "shell")
            ):
                continue
            rest = node.args[1:]
            starred = any(isinstance(arg, ast.Starred) for arg in rest)
            count = sum(not isinstance(arg, ast.Starred) for arg in rest)
            where = f"{path.relative_to(REPO)}:{node.lineno}"
            calls.append((where, node.args[0].value, count, starred))
    return calls


def all_verbs() -> dict[str, tuple[int, int]]:
    """Return hlab.sh's verbs and box.sh's, which hlab.sh hands on."""
    verbs = script_verbs(HLAB_SH)
    box_verbs = script_verbs(BOX_SH)
    assert handed_to_box() == set(box_verbs), (
        "hlab.sh hands on other verbs than box.sh's"
    )
    assert not set(verbs) & set(box_verbs), "a verb in both hlab.sh and box.sh"
    return verbs | box_verbs


def test_the_parsers_find_the_verbs_and_the_calls():
    verbs = all_verbs()
    assert verbs["init-net"] == (0, 0)
    assert verbs["set-route"] == (1, 2)
    assert verbs["start-vm"] == (5, MANY)
    assert verbs["nft"] == (0, MANY)
    assert verbs["set-access"] == (5, 5)
    called = {verb for _, verb, _, _ in hlabd_calls()}
    assert {"init-net", "start-vm", "nft", "tc", "set-account"} <= called


@pytest.mark.parametrize("script", [HLAB_SH, BOX_SH], ids=lambda path: path.name)
def test_every_verb_is_its_functions_name_with_dashes(script):
    for verb, function, _ in main_arms(script):
        assert verb.replace("-", "_") == function, (
            f"{script.name}: {verb} runs {function}"
        )


@pytest.mark.parametrize(
    "where, verb, count, starred", hlabd_calls(), ids=lambda value: str(value)
)
def test_every_verb_hlabd_calls_exists_with_its_argument_count(
    where, verb, count, starred
):
    verbs = all_verbs()
    assert verb in verbs, f"{where}: hlab.sh has no verb {verb}"
    least, most = verbs[verb]
    assert count <= most, f"{where}: {verb} takes at most {most} arguments"
    if not starred:
        assert count >= least, f"{where}: {verb} takes at least {least} arguments"


@NEEDS_BASH
def test_an_unknown_verb_fails_with_status_2():
    env = {**os.environ, **config.script_env()}
    process = subprocess.run(
        ["bash", str(HLAB_SH), "no-such-verb"], capture_output=True, text=True, env=env
    )
    assert process.returncode == 2
    assert "unknown command: no-such-verb" in process.stderr


@NEEDS_BASH
def test_remove_tree_removes_only_paths_strictly_inside_the_box(tmp_path):
    box, outside = tmp_path / "box", tmp_path / "outside"
    (box / "data" / "7").mkdir(parents=True)
    (box / "data" / "7" / "file").write_text("data")
    outside.mkdir()
    (outside / "file").write_text("keep")
    (box / "data" / "escape").symlink_to(outside)
    env = {**os.environ, **config.script_env(), "HLAB_BOX": str(box)}

    def remove_tree(path) -> subprocess.CompletedProcess:
        return subprocess.run(
            ["bash", str(BOX_SH), "remove-tree", str(path)],
            capture_output=True,
            text=True,
            env=env,
        )

    refused = [
        "/",
        box,
        f"{box}/",
        f"{box}/data/..",
        outside,
        f"{box}/data/../../outside",
        box / "data" / "escape",
        box / "data" / "escape" / "file",
    ]
    for path in refused:
        process = remove_tree(path)
        assert process.returncode == 1, path
        assert "refusing to remove" in process.stderr
    assert (outside / "file").read_text() == "keep"
    assert (box / "data" / "7" / "file").exists()
    assert remove_tree(box / "data" / "7").returncode == 0
    assert not (box / "data" / "7").exists()


# --- nftables -------------------------------------------------------------------------

NFT = shutil.which("nft")
# what nft -c says without CAP_NET_ADMIN, after its parser has run
NO_CACHE = "cache initialization failed: Operation not permitted"


def nft_check(text: str, path: Path) -> subprocess.CompletedProcess:
    """Run `nft -c -f` on a ruleset."""
    path.write_text(text)
    return subprocess.run(
        [NFT, "-c", "-f", str(path)], capture_output=True, text=True, check=False
    )


def nft_full_check_works() -> bool:
    """Tell whether `nft -c` can check against the kernel here (CAP_NET_ADMIN)."""
    if not NFT:
        return False
    process = subprocess.run(
        [NFT, "-c", "-f", "-"],
        input="table inet probe {}\n",
        capture_output=True,
        text=True,
    )
    return process.returncode == 0


def parse_errors(text: str, path: Path) -> str:
    """Return what nft's parser finds wrong in a ruleset (empty when nothing).

    Without CAP_NET_ADMIN nft parses the whole ruleset, then fails to read the
    kernel's; that last error alone means the ruleset parsed.
    """
    process = nft_check(text, path)
    if process.returncode == 0:
        return ""
    errors = [line for line in process.stderr.splitlines() if NO_CACHE not in line]
    return "\n".join(errors)


def render_init_ruleset(tmp_path: Path) -> str:
    """Return the table inet hlab that `hlab.sh init-net` loads (hlab.nft, rendered).

    ip, sysctl and nft are stubs: ip names eth0 as the uplink, nft keeps its input.
    """
    bin_dir = tmp_path / "bin"
    write_stub(
        bin_dir,
        "ip",
        'if [[ "$*" == "-4 route show default" ]]; then\n'
        "  echo 'default via 172.17.0.1 dev eth0'\n"
        "fi",
    )
    write_stub(bin_dir, "sysctl", "exit 0")
    write_stub(bin_dir, "nft", '[[ "$*" == "-f -" ]] && cat >"${NFT_OUT}"')
    out = tmp_path / "init.nft"
    env = {
        **os.environ,
        **config.script_env(),
        "PATH": f"{bin_dir}:{os.environ['PATH']}",
        "NFT_OUT": str(out),
    }
    subprocess.run(["bash", str(HLAB_SH), "init-net"], check=True, env=env)
    return out.read_text()


@pytest.fixture
def rulesets(api, tmp_path) -> dict[str, str]:
    """Return the rulesets the lab writes, from a lab with something in every set.

    "init" is hlab.sh's table inet hlab, "faults" the set elements on top of it,
    "firewall" the table inet hlabfw.
    """
    network = create_network(api, "10.0.1.0/24")
    web = create_server(api, "web", labels={"role": "web"}, networks=[network["id"]])
    create_server(api, "db", location="hel1", networks=[network["id"]])
    assert api.post("/_lab/clients", json={"name": "alice"}).status_code == 200
    floating_ip = api.post(
        "/v1/floating_ips", json={"type": "ipv4", "home_location": "fsn1"}
    ).json()["floating_ip"]
    wait_action(
        api,
        api.post(
            f"/v1/floating_ips/{floating_ip['id']}/actions/assign",
            json={"server": web["server"]["id"]},
        ).json()["action"],
    )
    for fault in (
        {"type": "cut", "first": "web", "second": "db"},
        {"type": "isolate", "vm": "db"},
        {"type": "block", "vm": "web", "what": "api"},
        {"type": "block", "vm": "alice", "what": "box"},
        {"type": "block", "vm": "web", "what": "internet"},
    ):
        response = api.post("/_lab/faults", json=fault)
        assert response.status_code == 200, response.text
    any_ip = ["0.0.0.0/0", "::/0"]
    rules = [
        {"direction": "in", "protocol": "tcp", "port": "22", "source_ips": any_ip},
        {"direction": "in", "protocol": "icmp", "source_ips": ["192.0.2.0/24"]},
        {
            "direction": "in",
            "protocol": "udp",
            "port": "9987-9990",
            "source_ips": ["2001:db8:ffff::/48", "198.51.100.7/32"],
        },
        {"direction": "in", "protocol": "gre", "source_ips": any_ip},
        {"direction": "in", "protocol": "esp", "source_ips": ["::/0"]},
        {
            "direction": "out",
            "protocol": "tcp",
            "port": "any",
            "destination_ips": any_ip,
        },
        {"direction": "out", "protocol": "icmp", "destination_ips": ["2001:db8::/32"]},
    ]
    response = api.post(
        "/v1/firewalls",
        json={
            "name": "fw",
            "rules": rules,
            "apply_to": [
                {"type": "label_selector", "label_selector": {"selector": "role=web"}}
            ],
        },
    )
    assert response.status_code == 201, response.text
    root = api.lab.fabric.root
    init = render_init_ruleset(tmp_path)
    return {
        "init": init,
        "faults": init + (root / "faults.nft").read_text(),
        "firewall": (root / "firewall.nft").read_text(),
    }


@NEEDS_BASH
def test_the_rendered_rulesets_fill_what_they_should(rulesets):
    assert 'iifname "eth0" drop' in rulesets["init"]
    # the web UI: only the Docker host (the default gateway) reaches it from outside
    ui_port = config.UI_PORT
    assert (
        f'iifname "eth0" ip saddr 172.17.0.1 tcp dport {ui_port} accept'
        in rulesets["init"]
    )
    assert f'iifname != "lo" tcp dport {ui_port} drop' in rulesets["init"]
    rules = [x for x in rulesets["init"].splitlines() if not x.lstrip().startswith("#")]
    assert not [x for x in rules if "${" in x], "a placeholder of hlab.nft is left"
    assert f"ip saddr {config.METADATA} tcp sport 80" in rulesets["init"]
    for name in (
        "cuts",
        "isolated",
        "blocked_svc",
        "blocked_inet",
        "private_pairs",
        "src4",
        "src6",
        "tap_tables",
    ):
        assert f"add element inet hlab {name} {{" in rulesets["faults"]
    assert "jump in_" in rulesets["firewall"]
    assert "jump out_" in rulesets["firewall"]


@NEEDS_BASH
def test_faults_fill_only_the_sets_hlab_sh_declares(rulesets):
    declared = set(re.findall(r"^\s+(?:set|map) (\w+) \{", rulesets["init"], re.M))
    used = set(
        re.findall(
            r"^(?:flush (?:set|map)|add element) inet hlab (\w+)",
            rulesets["faults"],
            re.M,
        )
    )
    assert used and used <= declared


@pytest.mark.skipif(not NFT, reason="nft is not installed")
@pytest.mark.parametrize("name", ["init", "faults", "firewall"])
@NEEDS_BASH
def test_the_rulesets_parse(rulesets, name, tmp_path):
    assert parse_errors(rulesets[name], tmp_path / "check.nft") == ""


@pytest.mark.skipif(not NFT, reason="nft is not installed")
def test_a_misspelt_rule_fails_the_parse_check(tmp_path):
    text = "table inet t {\n  chain c {\n    ip saddr 192.0.2.1 droop\n  }\n}\n"
    assert "syntax error" in parse_errors(text, tmp_path / "check.nft")


@pytest.mark.skipif(
    not nft_full_check_works(),
    reason="nft -c needs CAP_NET_ADMIN (e.g. docker run --cap-add NET_ADMIN)",
)
@pytest.mark.parametrize("name", ["init", "faults", "firewall"])
@NEEDS_BASH
def test_the_rulesets_pass_nfts_full_check(rulesets, name, tmp_path):
    process = nft_check(rulesets[name], tmp_path / "check.nft")
    assert process.returncode == 0, process.stderr


# --- box-shell.sh ---------------------------------------------------------------------

# commands box-shell may run; each stub logs its call (du also answers like du -sb)
STUBBED = (
    "ls cat head tail sha256sum stat mkdir rm mv cp touch chmod dd rsync scp borg"
).split()


@pytest.fixture
def box_shell(tmp_path):
    """Return a function running box-shell.sh like sshd does, with stubbed commands.

    The function takes the session's command and the account's settings and returns
    (exit status, stdout, stderr, the stubs' calls). The script runs as a copy whose
    fixed paths (the account root's files, sftp-server) point into tmp_path.
    """
    bin_dir = tmp_path / "bin"
    log = tmp_path / "calls.log"
    stub = 'echo "$(basename "$0") $*" >>"${STUB_LOG}"'
    for name in STUBBED:
        write_stub(bin_dir, name, stub)
    write_stub(bin_dir, "sftp-server", stub)
    write_stub(bin_dir, "du", f"{stub}\nprintf '1000\\t/home\\n'")
    (tmp_path / "box-shell.env").write_text(
        f'public_net="{config.PUBLIC_NET}"\n'
        f'floating_net="{config.FLOATING_NET}"\n'
        f'client6="{config.CLIENT6}"\n'
        f'lab6="{config.SERVICES_NET6}"\n'
        f'ipv6_prefix="{config.IPV6_PREFIX}"\n'
    )
    script = BOX_SHELL_SH.read_text()
    for fixed, moved in (
        ("/usr/local/lib/lab/box-shell.env", tmp_path / "box-shell.env"),
        ("/etc/hlab-access", tmp_path / "hlab-access"),
        ("/usr/lib/openssh/sftp-server", bin_dir / "sftp-server"),
    ):
        assert fixed in script, f"box-shell.sh no longer uses {fixed}"
        script = script.replace(fixed, str(moved))
    copy = tmp_path / "box-shell"
    copy.write_text(script)

    def run(command, port=23, client=f"{config.PUBLIC_NET}.5", **settings):
        access = {"ssh": 1, "external": 0, "readonly": 0, "quota": 100000, **settings}
        (tmp_path / "hlab-access").write_text(
            "".join(f"{key}={value}\n" for key, value in access.items())
        )
        log.write_text("")
        env = {
            "PATH": f"{bin_dir}:/usr/bin:/bin",
            "STUB_LOG": str(log),
            "SSH_CLIENT": f"{client} 50000 {port}",
            "SSH_CONNECTION": f"{client} 50000 {config.SERVICES} {port}",
            "SSH_ORIGINAL_COMMAND": command,
        }
        process = subprocess.run(
            ["bash", str(copy)], capture_output=True, text=True, env=env
        )
        return process.returncode, process.stdout, process.stderr, log.read_text()

    return run


def ran(call):
    """Expect the session to run this command (as the stub logs it)."""
    return ("ran", call)


def denied(message):
    """Expect the session to be refused with this message."""
    return ("denied", message)


READ_ONLY = {"readonly": 1}
INTERNET_V4 = f"{config.CLIENT_NET}.10"

BOX_SHELL_CASES = [
    # (what, command, port, client or None for a server's, settings, expected)
    ("sftp on port 22", "internal-sftp", 22, None, {}, ran("sftp-server -d /home")),
    (
        "sftp read-only",
        "/usr/lib/openssh/sftp-server",
        22,
        None,
        READ_ONLY,
        ran("sftp-server -R -d /home"),
    ),
    (
        "sftp works with ssh disabled",
        "internal-sftp",
        22,
        None,
        {"ssh": 0},
        ran("sftp-server -d /home"),
    ),
    (
        "port 22 runs no commands",
        "ls",
        22,
        None,
        {},
        denied("This service allows sftp connections only."),
    ),
    (
        "port 23 needs ssh enabled",
        "ls",
        23,
        None,
        {"ssh": 0},
        denied("SSH is disabled for this account"),
    ),
    ("no interactive shell", "", 23, None, {}, denied("no interactive shell")),
    ("ls runs", "ls -la", 23, None, {}, ran("ls -la")),
    ("words are not globbed", "ls *", 23, None, {}, ran("ls *")),
    ("reading works read-only", "sha256sum f", 23, None, READ_ONLY, ran("sha256sum f")),
    ("rm writes", "rm f", 23, None, {}, ran("rm f")),
    ("rm read-only", "rm f", 23, None, READ_ONLY, denied("read-only account")),
    ("a shell is refused", "bash -c id", 23, None, {}, denied("command not allowed")),
    ("a ; is refused", "ls; id", 23, None, {}, denied("command not allowed")),
    ("a pipe is refused", "ls | id", 23, None, {}, denied("command not allowed")),
    ("$(...) is refused", "ls $(id)", 23, None, {}, denied("command not allowed")),
    ("`...` is refused", "ls `id`", 23, None, {}, denied("command not allowed")),
    ("a redirect is refused", "ls >f", 23, None, {}, denied("command not allowed")),
    (
        "rsync sending read-only",
        "rsync --server --sender -logDtpre.iLsfxCIvu . /home/d",
        23,
        None,
        READ_ONLY,
        ran("rsync --server --sender -logDtpre.iLsfxCIvu . /home/d"),
    ),
    (
        "rsync receiving read-only",
        "rsync --server -logDtpre.iLsfxCIvu . /home/d",
        23,
        None,
        READ_ONLY,
        denied("read-only account"),
    ),
    (
        "rsync receiving",
        "rsync --server -logDtpre.iLsfxCIvu . /home/d",
        23,
        None,
        {},
        ran("rsync --server -logDtpre.iLsfxCIvu . /home/d"),
    ),
    (
        "rsync removing what it sent read-only",
        "rsync --server --sender --remove-source-files . d",
        23,
        None,
        READ_ONLY,
        denied("read-only account"),
    ),
    (
        "rsync with a log file read-only",
        "rsync --server --sender --log-file=x . d",
        23,
        None,
        READ_ONLY,
        denied("read-only account"),
    ),
    (
        "rsync as a daemon",
        "rsync --server --daemon .",
        23,
        None,
        {},
        denied("command not allowed"),
    ),
    (
        "rsync not as a server",
        "rsync -e sh x y",
        23,
        None,
        {},
        denied("command not allowed"),
    ),
    ("scp sending read-only", "scp -f f", 23, None, READ_ONLY, ran("scp -f f")),
    (
        "scp receiving read-only",
        "scp -v -t d",
        23,
        None,
        READ_ONLY,
        denied("read-only account"),
    ),
    ("scp receiving", "scp -t d", 23, None, {}, ran("scp -t d")),
    ("scp without a direction", "scp f", 23, None, {}, denied("command not allowed")),
    (
        "scp options after -- do not count",
        "scp -- -t",
        23,
        None,
        {},
        denied("command not allowed"),
    ),
    (
        "borg serve",
        "borg serve --umask=077",
        23,
        None,
        {},
        ran("borg serve --umask=077"),
    ),
    ("borg-1.4 serve runs borg", "borg-1.4 serve", 23, None, {}, ran("borg serve")),
    ("borg read-only", "borg serve", 23, None, READ_ONLY, denied("read-only account")),
    ("borg other than serve", "borg list", 23, None, {}, denied("command not allowed")),
    (
        "a Floating IP may connect",
        "ls",
        23,
        f"{config.FLOATING_NET}.10",
        {},
        ran("ls "),
    ),
    ("a server's IPv6 may connect", "ls", 23, "2001:db8:1::2", {}, ran("ls ")),
    (
        "an Internet client may not",
        "ls",
        23,
        INTERNET_V4,
        {},
        denied("not reachable from outside"),
    ),
    (
        "an Internet client over IPv6 may not",
        "ls",
        23,
        f"{config.CLIENT6}::10",
        {},
        denied("not reachable from outside"),
    ),
    (
        "the lab itself may not",
        "ls",
        23,
        f"{config.SERVICES_NET6}::1",
        {},
        denied("not reachable from outside"),
    ),
    (
        "an Internet client may when external is on",
        "ls",
        23,
        INTERNET_V4,
        {"external": 1},
        ran("ls "),
    ),
    (
        "sftp from the Internet may not either",
        "internal-sftp",
        22,
        INTERNET_V4,
        {},
        denied("not reachable from outside"),
    ),
]


@pytest.mark.parametrize(
    "command, port, client, settings, expected",
    [pytest.param(*case[1:], id=case[0]) for case in BOX_SHELL_CASES],
)
@NEEDS_BASH
def test_box_shell_allows_and_refuses_like_the_storage_box(
    box_shell, command, port, client, settings, expected
):
    kwargs = {"port": port, **settings}
    if client:
        kwargs["client"] = client
    status, _, stderr, calls = box_shell(command, **kwargs)
    outcome, detail = expected
    if outcome == "ran":
        assert (status, calls) == (0, detail + "\n"), stderr
    else:
        assert status == 1
        assert detail in stderr
        assert calls == ""


@NEEDS_BASH
def test_box_shell_df_reports_the_usage_against_the_quota(box_shell):
    status, stdout, _, _ = box_shell("df -h", quota=100000)
    assert status == 0
    assert stdout.splitlines() == [
        "Filesystem 1B-blocks Used Available Use% Mounted on",
        "storagebox 100000 1000 99000 1% /home",
    ]


# --- lib.sh ---------------------------------------------------------------------------


def lib(function: str, *args: str) -> int:
    """Run a lib.sh function and return its exit status."""
    return subprocess.run(
        ["bash", "-c", f'source "$0"; {function} "$@"', str(LIB_SH), *args]
    ).returncode


@pytest.fixture
def sleeper(tmp_path):
    """Return a running `sleep` and a PID file naming it; kill it afterwards."""
    process = subprocess.Popen(["sleep", "60"])
    pid_file = tmp_path / "sleep.pid"
    pid_file.write_text(f"{process.pid}\n")
    yield process, pid_file
    process.kill()
    process.wait()


@NEEDS_BASH_AND_PROC
def test_is_alive_while_the_named_process_runs(sleeper):
    _, pid_file = sleeper
    assert lib("is_alive", str(pid_file), "sleep") == 0
    assert lib("is_alive", str(pid_file), "sle") == 0


@NEEDS_BASH_AND_PROC
def test_is_alive_fails_for_another_program_under_that_pid(sleeper):
    _, pid_file = sleeper
    assert lib("is_alive", str(pid_file), "qemu-system") == 1


@NEEDS_BASH_AND_PROC
def test_is_alive_fails_without_a_pid_file_or_process(tmp_path):
    assert lib("is_alive", str(tmp_path / "missing.pid"), "sleep") == 1
    process = subprocess.Popen(["true"])
    process.wait()
    pid_file = tmp_path / "gone.pid"
    pid_file.write_text(f"{process.pid}\n")
    assert lib("is_alive", str(pid_file), "true") == 1


@NEEDS_BASH_AND_PROC
def test_stop_pid_terminates_the_process_and_removes_the_file(sleeper):
    process, pid_file = sleeper
    assert lib("stop_pid", str(pid_file), "sleep") == 0
    assert process.wait(timeout=5) == -15
    assert not pid_file.exists()


@NEEDS_BASH_AND_PROC
def test_stop_pid_sends_the_signal_it_is_given(sleeper):
    process, pid_file = sleeper
    assert lib("stop_pid", str(pid_file), "sleep", "KILL") == 0
    assert process.wait(timeout=5) == -9


@NEEDS_BASH_AND_PROC
def test_stop_pid_spares_another_program_under_that_pid(sleeper):
    process, pid_file = sleeper
    assert lib("stop_pid", str(pid_file), "qemu-system") == 0
    assert process.poll() is None
    assert not pid_file.exists()


@NEEDS_BASH_AND_PROC
def test_stop_pid_without_a_pid_file_succeeds(tmp_path):
    assert lib("stop_pid", str(tmp_path / "missing.pid"), "sleep") == 0
