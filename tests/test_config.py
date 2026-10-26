"""The lab's configuration file."""

import pytest

from hlabd import config


def test_configuration_typos_are_reported(tmp_path):
    config_file = tmp_path / "hlab.yaml"
    config_file.write_text("name: x\nstorage_boxes: {enabled: false}\n")
    with pytest.raises(ValueError, match="unknown settings storage_boxes"):
        config.load(config_file)
    config_file.write_text("name: x\nstorage_box: true\n")
    with pytest.raises(ValueError, match="storage_box must be a mapping"):
        config.load(config_file)
