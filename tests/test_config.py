"""The lab's configuration file."""

from pathlib import Path

import pytest
import yaml

from hlabd import config

EXAMPLE = Path(__file__).resolve().parents[1] / "examples" / "hlab.yaml"


def test_the_example_shows_every_default():
    # the example says its values are the defaults
    assert yaml.safe_load(EXAMPLE.read_text(encoding="utf-8")) == config.DEFAULTS


def test_catalogue_entries_without_prices_are_reported(tmp_path):
    config_file = tmp_path / "hlab.yaml"
    config_file.write_text(
        "server_types:\n  cx23: {cores: 2, memory_gb: 4, disk_gb: 40}\n"
    )
    with pytest.raises(ValueError, match="cx23 lacks category, traffic_tb"):
        config.load(config_file)
    config_file.write_text(
        "server_types:\n"
        "  cx23: {category: cheap, cores: 2, memory_gb: 4, disk_gb: 40,\n"
        "         traffic_tb: 20, price_monthly: 5.49}\n"
    )
    with pytest.raises(ValueError, match="unknown category 'cheap'"):
        config.load(config_file)


def test_configuration_typos_are_reported(tmp_path):
    config_file = tmp_path / "hlab.yaml"
    config_file.write_text("name: x\nstorage_boxes: {enabled: false}\n")
    with pytest.raises(ValueError, match="unknown settings storage_boxes"):
        config.load(config_file)
    config_file.write_text("name: x\nstorage_box: true\n")
    with pytest.raises(ValueError, match="storage_box must be a mapping"):
        config.load(config_file)
