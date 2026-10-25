"""Live tests: real VMs in a running lab. Run them with `bin/hlab itest` (inside the lab container, a few minutes).

Three servers: a (fsn1) and b (nbg1) in network "one", c (hel1) in network "two"; an echo service on every server
answers "<host> <proto> <peer address>" on TCP and UDP port 7000 and TCP 7001; an Internet client "alice"."""

import os

import pytest

from livelab import Lab

if not os.environ.get("HLAB_LIVE"):
    pytest.skip("live tests run inside a lab (bin/hlab itest)", allow_module_level=True)


@pytest.fixture(scope="session")
def lab() -> Lab:
    lab = Lab()
    lab.control("POST", "/reset")
    nets = {}
    for name, rng in (("one", "10.0.0.0/16"), ("two", "10.1.0.0/16")):
        nets[name] = lab.api("POST", "/networks", {"name": name, "ip_range": rng, "subnets": [
            {"type": "cloud", "ip_range": rng.replace("0.0/16", "1.0/24"), "network_zone": "eu-central"}]})["network"]["id"]
    for name, loc, net in (("a", "fsn1", "one"), ("b", "nbg1", "one"), ("c", "hel1", "two")):
        lab.api("POST", "/servers", {"name": name, "server_type": "cx23", "image": "debian-13", "location": loc,
                                     "networks": [nets[net]], "labels": {"lab": "live"}})
    for name in ("a", "b", "c"):
        lab.wait_ssh(name)
        lab.sh(name, "cloud-init status --wait >/dev/null; sync", timeout=120)
        lab.start_echo(name)
    lab.control("POST", "/clients", {"name": "alice"})
    lab.nets = nets
    return lab
