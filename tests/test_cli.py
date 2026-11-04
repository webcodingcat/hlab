"""The hlab command inside the container."""

import pytest

from hlabd import cli


def test_env_quotes_its_values(capsys, monkeypatch):
    monkeypatch.setattr(
        cli.config,
        "load",
        lambda _: {
            "name": "demo",
            "ui_port": 8481,
            "socks_port": 1081,
            "api": {"token": "a b;$(x)"},
        },
    )
    cli.env()
    out = capsys.readouterr().out
    assert "export HCLOUD_TOKEN='a b;$(x)'" in out
    # the web UI on the configured port of the host
    assert "export HLAB_UI=http://localhost:8481" in out
    assert "export HLAB_SOCKS=socks5h://localhost:1081" in out


def test_image_import_sends_the_absolute_file_and_labels(capsys, monkeypatch):
    calls = []

    def fake_call(method, path, body=None, timeout=120):
        calls.append((method, path, body))
        return {"image": {"id": 42}}

    monkeypatch.setattr(cli, "call", fake_call)
    monkeypatch.chdir("/")
    cli.main(
        ["image", "import", "tmp/a.qcow2", "--label", "role=host", "--label", "x="]
    )
    assert calls == [
        (
            "POST",
            "/images",
            {
                "file": "/tmp/a.qcow2",
                "description": None,
                "labels": {"role": "host", "x": ""},
            },
        )
    ]
    assert capsys.readouterr().out == "42\n"


def test_image_import_refuses_a_label_without_a_value(monkeypatch):
    monkeypatch.setattr(cli, "call", lambda *args, **kwargs: {})
    with pytest.raises(SystemExit, match="KEY=VALUE"):
        cli.main(["image", "import", "/a.qcow2", "--label", "role"])
