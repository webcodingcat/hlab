"""Download Hetzner's OpenAPI specifications (Python's standard library only).

    python3 tools/fetch_specs.py [directory]

The directory defaults to .cache/spec/ in the repository, where the unit tests find
the specs when HLAB_SPEC_DIR is unset and the image's /opt/hlab/spec does not exist.
The image build fetches them into /opt/hlab/spec with this script. Prints the
directory (for HLAB_SPEC_DIR).
"""

import sys
import urllib.request
from pathlib import Path

# file name → Hetzner's URL; hlabd.api.spec loads them by these names
SPECS = {
    "cloud.json": "https://docs.hetzner.cloud/cloud.spec.json",
    "hetzner.json": "https://docs.hetzner.cloud/hetzner.spec.json",
}
DEFAULT_DIR = Path(__file__).resolve().parents[1] / ".cache" / "spec"


def fetch(target: Path) -> None:
    """Download every specification into target; a file is replaced when complete."""
    target.mkdir(parents=True, exist_ok=True)
    for name, url in SPECS.items():
        with urllib.request.urlopen(url, timeout=60) as response:
            content = response.read()
        partial = target / f"{name}.part"
        partial.write_bytes(content)
        partial.replace(target / name)


if __name__ == "__main__":
    spec_dir = Path(sys.argv[1]) if len(sys.argv) > 1 else DEFAULT_DIR
    fetch(spec_dir)
    print(spec_dir.resolve())
