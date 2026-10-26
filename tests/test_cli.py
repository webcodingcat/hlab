"""The hlab command inside the container."""

from hlabd import cli


def test_env_quotes_its_values(capsys, monkeypatch):
    monkeypatch.setattr(
        cli.config, "load", lambda _: {"name": "demo", "api": {"token": "a b;$(x)"}}
    )
    cli.env()
    assert "export HCLOUD_TOKEN='a b;$(x)'" in capsys.readouterr().out
