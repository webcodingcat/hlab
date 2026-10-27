"""The hlab command inside the container."""

from hlabd import cli


def test_env_quotes_its_values(capsys, monkeypatch):
    monkeypatch.setattr(
        cli.config,
        "load",
        lambda _: {"name": "demo", "ui_port": 8481, "api": {"token": "a b;$(x)"}},
    )
    cli.env()
    out = capsys.readouterr().out
    assert "export HCLOUD_TOKEN='a b;$(x)'" in out
    # the web UI on the configured port of the host
    assert "export HLAB_UI=http://localhost:8481" in out
