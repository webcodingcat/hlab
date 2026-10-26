"""The real shell (fabric/shell.py): a hung script ends with its timeout."""

import shutil

import pytest

from hlabd.fabric import shell


@pytest.fixture
def hanging_shell(tmp_path, monkeypatch):
    """Return a Shell whose hlab.sh hangs for a minute."""
    if not shutil.which("sh"):
        pytest.skip("no sh")
    script = tmp_path / "hlab.sh"
    script.write_text("#!/bin/sh\nsleep 60\n")
    script.chmod(0o755)
    monkeypatch.setattr(shell, "HLAB_SH", str(script))
    return shell.Shell()


def test_a_script_that_hangs_fails_after_its_timeout(hanging_shell):
    with pytest.raises(RuntimeError, match=r"hlab\.sh kill-vm x: no end after 0\.2 s"):
        hanging_shell.run("kill-vm", "x", timeout=0.2)
    # unchecked commands time out too: a hung thread would block a lab reset
    with pytest.raises(RuntimeError, match="no end after"):
        hanging_shell.run("list-local-addrs", check=False, timeout=0.2)
    with pytest.raises(RuntimeError, match="no end after"):
        hanging_shell.ok("is-vm-alive", "x", timeout=0.2)
