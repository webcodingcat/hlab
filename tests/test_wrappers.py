"""bin/hlab dispatches exactly the commands its usage names, and reads lab names.

The command names come from the case in main() and from the usage comment at the top
of the file, both read as text; formatting (indentation, line breaks, alignment) may
change, the names must match. The reading of the lab name runs in Bash.
"""

import os
import re
import shutil
import subprocess
from pathlib import Path

import pytest

from hlabd import config

BIN = Path(__file__).resolve().parents[1] / "bin"
BASH = (BIN / "hlab").read_text()

# the two arms that are no command of their own
EMPTY = "<none>"
DEFAULT = "<command>"

NEEDS_BASH = pytest.mark.skipif(
    not shutil.which("bash"), reason="bash is not installed"
)


def indented_block(lines: list[str], start: int) -> list[str]:
    """Return the lines after start at the first one's depth, up to a shallower one.

    These are the arms of the case whose header is lines[start].
    """
    body = lines[start + 1 :]
    first = next(line for line in body if line.strip())
    depth = len(first) - len(first.lstrip())
    block = []
    for line in body:
        if line.strip() and len(line) - len(line.lstrip()) < depth:
            break
        block.append(line)
    return [line for line in block if len(line) - len(line.lstrip()) == depth]


def dispatch() -> set[str]:
    """Return the patterns of the case in bin/hlab's main()."""
    main = BASH[BASH.index("\nmain() {") :]
    lines = main.splitlines()
    start = next(
        i for i, line in enumerate(lines) if re.match(r"\s*case\s.*\sin\s*$", line)
    )
    names: set[str] = set()
    for line in indented_block(lines, start):
        label = re.match(r"\s*([^\s()]+)\)", line)
        if not label:
            continue
        for pattern in label.group(1).split("|"):
            names.add({'""': EMPTY, "''": EMPTY, "*": DEFAULT}.get(pattern, pattern))
    return names


def usage() -> tuple[set[str], set[str]]:
    """Return the commands and options named by bin/hlab's usage comment.

    A usage line is `bin/hlab [<option> <value>] <command> ...`; the command part ends
    at two spaces (the description) or the line's end, and `a | b | c` names several.
    """
    header = re.match(r"#!.*\n((?:#.*\n)+)", BASH)
    assert header, "bin/hlab has no usage comment at the top"
    commands: set[str] = set()
    options: set[str] = set()
    line_pattern = r"bin/hlab\s+\[(-[\w-]+)[^\]]*\]\s+(.+?)(?:\s{2,}|$)"
    for line in header.group(1).splitlines():
        found = re.search(line_pattern, line)
        if not found:
            continue
        options.add(found.group(1))
        for part in found.group(2).split("|"):
            commands.add(part.split()[0])
    return commands, options


def options() -> set[str]:
    """Return the options main() in bin/hlab accepts before the command."""
    main = BASH[BASH.index("\nmain() {") : BASH.index('case "${1:-}"')]
    return set(re.findall(r'"\$\{1:-\}"\s*==\s*(-[\w-]+)', main))


def test_the_parsers_find_the_commands():
    for names in (dispatch(), usage()[0]):
        assert {"up", "down", "test", "itest", DEFAULT} <= names


def test_every_command_bin_hlab_dispatches_is_in_its_usage_and_back():
    assert dispatch() - {EMPTY} == usage()[0]


def test_the_usage_names_the_options_bin_hlab_accepts():
    assert usage()[1] == {"-c"}
    assert usage()[1] <= options()


def bash_lab_name(text: str, tmp_path: Path) -> subprocess.CompletedProcess:
    """Run bin/hlab's read_lab_name on a configuration with the given text."""
    config = tmp_path / "hlab.yaml"
    config.write_text(text)
    return subprocess.run(
        ["bash", "-c", 'source "$1" && read_lab_name "$2"', "-", BIN / "hlab", config],
        capture_output=True,
        text=True,
        check=False,
    )


@pytest.mark.parametrize(
    ("text", "name"),
    [
        ("name: lab-1\n", "lab-1"),
        ('name: "lab_1"  # a comment\n', "lab_1"),
        ("# name: other\napi:\n  name: nested\nname :  'lab1' \r\n", "lab1"),
        ("name: lab1\t# a comment\nname: second\n", "lab1"),
        ("api:\n  token: null\n", ""),
        ('name: ""\n', ""),
    ],
)
@NEEDS_BASH
def test_bin_hlab_reads_the_top_level_name(text, name, tmp_path):
    process = bash_lab_name(text, tmp_path)
    assert process.returncode == 0, process.stderr
    assert process.stdout.strip() == name


@pytest.mark.parametrize("text", ["name: my.lab\n", "name: my lab\n", "name: a#b\n"])
@NEEDS_BASH
def test_bin_hlab_refuses_a_name_it_would_cut_off(text, tmp_path):
    process = bash_lab_name(text, tmp_path)
    assert process.returncode == 2 and "may only have" in process.stderr


def bash_ui_port(text: str, tmp_path: Path) -> subprocess.CompletedProcess:
    """Run bin/hlab's read_port for ui_port on a configuration with the given text."""
    config = tmp_path / "hlab.yaml"
    config.write_text(text)
    command = 'source "$1" && read_port "$2" ui_port'
    return subprocess.run(
        ["bash", "-c", command, "-", BIN / "hlab", config],
        capture_output=True,
        text=True,
        check=False,
    )


@pytest.mark.parametrize(
    ("text", "port"),
    [
        ("ui_port: 8481\n", "8481"),
        ("ui_port: '9000'  # a comment\n", "9000"),
        ("api:\n  token: null\n", ""),
    ],
)
@NEEDS_BASH
def test_bin_hlab_reads_the_web_ui_port(text, port, tmp_path):
    process = bash_ui_port(text, tmp_path)
    assert process.returncode == 0, process.stderr
    assert process.stdout.strip() == port


@NEEDS_BASH
def test_bin_hlab_refuses_a_web_ui_port_that_is_no_number(tmp_path):
    process = bash_ui_port("ui_port: http\n", tmp_path)
    assert process.returncode == 2 and "port number" in process.stderr


def test_bin_hlab_publishes_the_ports_the_lab_listens_on():
    # bin/hlab cannot import config.py, so the ports are written twice
    text = (BIN / "hlab").read_text()
    for name, port in [("UI_PORT", config.UI_PORT), ("SOCKS_PORT", config.SOCKS_PORT)]:
        match = re.search(rf"^readonly {name}=(\d+)$", text, re.M)
        assert match and int(match.group(1)) == port, name


# a stand-in for docker in run_image's tests: the container's files live under $BOX,
# and "hlab image" copies files there as the lab would
FAKE_DOCKER = r"""
docker() {
  shift  # exec
  [[ "$1" == -i ]] && shift
  shift  # the container
  # what runs in the container, and its last argument: a path there, mostly
  local cmd="$1" path="${!#}"
  case "${cmd}" in
    mkdir) mkdir -p "${BOX}${path}" ;;
    rm) rm -f "${BOX}${path}" ;;
    mktemp) touch "${BOX}${path}/transfer" && echo "${path}/transfer" ;;
    cat) cat "${BOX}${path}" ;;
    cp) cat >"${BOX}${path}" ;;  # cp /dev/stdin <path>
    hlab)
      local subcommand="$3"
      if [[ "${subcommand}" == export ]]; then  # hlab image export <id> <path>
        local image_id="$4" transfer="$5"
        [[ "${image_id}" == 7 ]] || return 1
        echo "image 7" >"${BOX}${transfer}"
      else  # hlab image import <path> <options ...>
        local transfer="$4"
        echo "imported $(cat "${BOX}${transfer}"):${*:5}"
      fi
      ;;
  esac
}
"""


def bash_image(tmp_path: Path, *args: str) -> subprocess.CompletedProcess:
    """Run bin/hlab's run_image with a fake docker; the container's files are BOX."""
    (tmp_path / "box").mkdir(exist_ok=True)
    command = f'source "$1" && {FAKE_DOCKER} run_image "${{@:2}}"'
    return subprocess.run(
        ["bash", "-c", command, "-", BIN / "hlab", *args],
        capture_output=True,
        text=True,
        check=False,
        cwd=tmp_path,
        env={**os.environ, "BOX": str(tmp_path / "box")},
    )


@NEEDS_BASH
def test_bin_hlab_exports_an_image_to_a_file_here(tmp_path):
    process = bash_image(tmp_path, "export", "7", "out.qcow2")
    assert process.returncode == 0, process.stderr
    assert (tmp_path / "out.qcow2").read_text() == "image 7\n"
    # the transfer file in the container is gone
    assert not list((tmp_path / "box" / "hlab" / "transfer").iterdir())


@NEEDS_BASH
def test_bin_hlab_leaves_no_file_when_an_export_fails(tmp_path):
    process = bash_image(tmp_path, "export", "8", "out.qcow2")
    assert process.returncode != 0
    assert sorted(p.name for p in tmp_path.iterdir()) == ["box"]
    assert not list((tmp_path / "box" / "hlab" / "transfer").iterdir())


@NEEDS_BASH
def test_bin_hlab_imports_a_file_here(tmp_path):
    (tmp_path / "in.qcow2").write_text("prepared")
    process = bash_image(
        tmp_path, "import", "in.qcow2", "--label", "role=host", "--description", "x"
    )
    assert process.returncode == 0, process.stderr
    assert process.stdout == "imported prepared:--label role=host --description x\n"
    assert not list((tmp_path / "box" / "hlab" / "transfer").iterdir())


@NEEDS_BASH
def test_bin_hlab_refuses_to_import_a_missing_file(tmp_path):
    process = bash_image(tmp_path, "import", "missing.qcow2")
    assert process.returncode == 2 and "no such file" in process.stderr
