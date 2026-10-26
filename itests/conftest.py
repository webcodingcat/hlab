"""Live tests: real VMs in a running lab.

Run them with `bin/hlab itest` (inside the lab container, ~15 min).

Three servers: a (fsn1) and b (nbg1) in network "one", c (hel1) in network "two"; an
echo service on every server answers "<host> <proto> <peer address>" on TCP and UDP
port 7000 and TCP 7001; an Internet client "alice".
"""

import os

import pytest
from livelab import LiveLab

if not os.environ.get("HLAB_LIVE"):
    pytest.skip("live tests run inside a lab (bin/hlab itest)", allow_module_level=True)


def pytest_configure(config):
    """Register the disturbs_servers marker."""
    config.addinivalue_line(
        "markers", "disturbs_servers: powers off, resets or deletes a, b or c"
    )


def pytest_collection_modifyitems(items):
    """Run the tests that disturb a, b and c last, in the order of their file.

    All other tests share the three servers as the fixture leaves them, so these come
    after every other file (a stable sort keeps the order within each group).
    """
    items.sort(key=lambda item: item.get_closest_marker("disturbs_servers") is not None)


@pytest.fixture(scope="session")
def lab() -> LiveLab:
    """Provide a fresh lab with the servers a, b and c and the client alice.

    Every server runs the echo service; lab.nets maps network names to IDs.
    """
    lab = LiveLab()
    lab.control("POST", "/reset")
    nets = {}
    for name, ip_range in (("one", "10.0.0.0/16"), ("two", "10.1.0.0/16")):
        nets[name] = lab.api(
            "POST",
            "/networks",
            {
                "name": name,
                "ip_range": ip_range,
                "subnets": [
                    {
                        "type": "cloud",
                        "ip_range": ip_range.replace("0.0/16", "1.0/24"),
                        "network_zone": "eu-central",
                    }
                ],
            },
        )["network"]["id"]
    for name, loc, net in (
        ("a", "fsn1", "one"),
        ("b", "nbg1", "one"),
        ("c", "hel1", "two"),
    ):
        lab.api(
            "POST",
            "/servers",
            {
                "name": name,
                "server_type": "cx23",
                "image": "debian-13",
                "location": loc,
                "networks": [nets[net]],
                "labels": {"lab": "live"},
            },
        )
    for name in ("a", "b", "c"):
        lab.wait_ssh(name)
        lab.check_output(name, "cloud-init status --wait >/dev/null; sync", timeout=120)
        lab.start_echo(name)
    lab.control("POST", "/clients", {"name": "alice"})
    lab.nets = nets
    return lab
