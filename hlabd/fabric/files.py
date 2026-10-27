"""The lab's own files that a verb then loads: written only when they change."""

from pathlib import Path


def write_if_changed(path: Path, text: str) -> bool:
    """Write a file unless it has this text already; tell whether it changed.

    So a service reloads only when its configuration really changed.
    """
    if path.is_file() and path.read_text() == text:
        return False
    path.write_text(text)
    return True
