"""bin/hlab (Bash) and bin/hlab.ps1 (PowerShell) offer the same commands.

The test container has no PowerShell, so both files are read as text: the command names
from bin/hlab's case in main() and from hlab.ps1's switch, the options both accept
before the command, and the usage text at the top of each file. Formatting (indentation,
line breaks, alignment) may change; the names must match. bin/hlab's reading of the lab
name runs in Bash; hlab.ps1 does the same with the same pattern.
"""

import re
import subprocess
from pathlib import Path

import pytest

BIN = Path(__file__).resolve().parents[1] / "bin"
BASH = (BIN / "hlab").read_text()
POWERSHELL = (BIN / "hlab.ps1").read_text()

# the two arms that are no command of their own, named alike for both wrappers
EMPTY = "<none>"
DEFAULT = "<command>"


def indented_block(lines: list[str], start: int) -> list[str]:
    """Return the lines after start at the first one's depth, up to a shallower one.

    These are the arms of a case or switch whose header is lines[start].
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


def bash_dispatch() -> set[str]:
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


def powershell_dispatch() -> set[str]:
    """Return the labels of the switch over the command in bin/hlab.ps1."""
    lines = POWERSHELL.splitlines()
    start = next(
        i for i, line in enumerate(lines) if re.match(r"\s*switch\b.*\{\s*$", line)
    )
    names: set[str] = set()
    for line in indented_block(lines, start):
        label = re.match(r"\s*(?:'([^']*)'|\"([^\"]*)\"|(default))\s*\{", line)
        if not label:
            continue
        quoted = label.group(1) if label.group(1) is not None else label.group(2)
        if label.group(3):
            names.add(DEFAULT)
        else:
            names.add(quoted or EMPTY)
    return names


def usage(text: str, wrapper: str) -> tuple[set[str], set[str]]:
    """Return the commands and options named by the usage lines of a wrapper.

    A usage line is `<wrapper> [<option> <value>] <command> ...`; the command part ends
    at two spaces (the description) or the line's end, and `a | b | c` names several.
    """
    commands: set[str] = set()
    options: set[str] = set()
    line_pattern = re.escape(wrapper) + r"\s+\[(-[\w-]+)[^\]]*\]\s+(.+?)(?:\s{2,}|$)"
    for line in text.splitlines():
        found = re.search(line_pattern, line)
        if not found:
            continue
        options.add(found.group(1))
        for part in found.group(2).split("|"):
            commands.add(part.split()[0])
    return commands, options


def bash_usage() -> tuple[set[str], set[str]]:
    """Return the commands and options of bin/hlab's comment block."""
    header = re.match(r"#!.*\n((?:#.*\n)+)", BASH)
    assert header, "bin/hlab has no usage comment at the top"
    return usage(header.group(1), "bin/hlab")


def powershell_usage() -> tuple[set[str], set[str]]:
    """Return the commands and options of hlab.ps1's comment-based help."""
    description = re.search(
        r"\.DESCRIPTION\s*\n(.*?)\n\s*\.(?:EXAMPLE|NOTES)", POWERSHELL, re.S
    )
    assert description, "bin/hlab.ps1 has no .DESCRIPTION in its help"
    return usage(description.group(1), r".\bin\hlab.ps1")


def bash_options() -> set[str]:
    """Return the options main() in bin/hlab accepts before the command."""
    main = BASH[BASH.index("\nmain() {") : BASH.index('case "${1:-}"')]
    return set(re.findall(r'"\$\{1:-\}"\s*==\s*(-[\w-]+)', main))


def powershell_options() -> set[str]:
    """Return the options bin/hlab.ps1 accepts before the command."""
    found = re.search(
        r"\$CommandLine\[0\]\s+-in\s+((?:'-[\w-]+'\s*,?\s*)+)", POWERSHELL
    )
    assert found, "bin/hlab.ps1 checks no option before the command"
    return set(re.findall(r"'(-[\w-]+)'", found.group(1)))


def test_the_parsers_find_the_commands_of_both_wrappers():
    for names in (
        bash_dispatch(),
        powershell_dispatch(),
        bash_usage()[0],
        powershell_usage()[0],
    ):
        assert {"up", "down", "test", "itest", DEFAULT} <= names


def test_both_wrappers_dispatch_the_same_commands():
    assert bash_dispatch() == powershell_dispatch()


def test_every_command_bin_hlab_dispatches_is_in_its_usage():
    assert bash_dispatch() - {EMPTY} == bash_usage()[0]


def test_every_command_hlab_ps1_dispatches_is_in_its_usage():
    assert powershell_dispatch() - {EMPTY} == powershell_usage()[0]


def test_both_usages_name_the_same_commands():
    assert bash_usage()[0] == powershell_usage()[0]


def test_both_usages_name_the_same_options():
    assert bash_usage()[1] == powershell_usage()[1] == {"-c"}


def test_both_wrappers_accept_the_same_options_before_the_command():
    assert bash_options() == powershell_options()
    assert bash_usage()[1] <= bash_options()


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
def test_bin_hlab_reads_the_top_level_name(text, name, tmp_path):
    process = bash_lab_name(text, tmp_path)
    assert process.returncode == 0, process.stderr
    assert process.stdout.strip() == name


@pytest.mark.parametrize("text", ["name: my.lab\n", "name: my lab\n", "name: a#b\n"])
def test_bin_hlab_refuses_a_name_it_would_cut_off(text, tmp_path):
    process = bash_lab_name(text, tmp_path)
    assert process.returncode == 2 and "may only have" in process.stderr
