"""Live tests of Storage Boxes made through the Storage Box API (api.hetzner.com stand-in on 198.18.0.3): logins,
ports 22 and 23, sub-accounts, access settings, snapshots."""

import subprocess
import time

import httpx
import paramiko
import pytest

from livelab import TOKEN, Lab

BOX_API = "http://198.18.0.3/v1"
PW = "Box-Secret-123"


class BoxApi:
    def __init__(self):
        self.http = httpx.Client(base_url=BOX_API, headers={"Authorization": f"Bearer {TOKEN}"}, timeout=60)

    def __call__(self, method: str, path: str, body: dict | None = None) -> dict:
        response = self.http.request(method, path, json=body)
        assert response.status_code < 300, f"{method} {path}: {response.status_code} {response.text}"
        return response.json() if response.content else {}

    def wait(self, action: dict) -> None:
        for _ in range(120):
            action = self("GET", f"/storage_boxes/actions/{action['id']}")["action"]
            if action["status"] != "running":
                assert action["status"] == "success", action
                return
            time.sleep(0.5)
        raise AssertionError(action)


@pytest.fixture(scope="module")
def live_box():
    api = BoxApi()
    response = api("POST", "/storage_boxes", {"name": "live", "location": "fsn1", "storage_box_type": "bx11",
                                              "password": PW, "access_settings": {"ssh_enabled": True,
                                                                                  "reachable_externally": True}})
    api.wait(response["action"])
    live_box = api("GET", f"/storage_boxes/{response['storage_box']['id']}")["storage_box"]
    yield api, live_box
    api("POST", f"/storage_boxes/{live_box['id']}/actions/change_protection", {"delete": False})
    api.wait(api("DELETE", f"/storage_boxes/{live_box['id']}")["action"])


def sftp(user: str, password: str | None = None, port: int = 22, host: str = "198.18.0.1", key=None):
    transport = paramiko.Transport((host, port))
    transport.connect(username=user, password=password, pkey=key)
    return transport, paramiko.SFTPClient.from_transport(transport)


def ssh_cmd(user: str, password: str, port: int, cmd: str) -> tuple[int, str]:
    ssh_client = paramiko.SSHClient()
    ssh_client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    ssh_client.connect("198.18.0.1", port=port, username=user, password=password, allow_agent=False, look_for_keys=False)
    _, out, err = ssh_client.exec_command(cmd)
    code = out.channel.recv_exit_status()
    text = out.read().decode() + err.read().decode()
    ssh_client.close()
    return code, text


def test_box_resolves_and_logs_in_with_password(lab: Lab, live_box):
    api, box = live_box
    assert box["server"] == f"u{box['id']}.your-storagebox.de"
    assert lab.sh("a", f"getent ahosts {box['server']} | head -1").split()[0] in ("198.18.0.1", "2001:db8:fffe::1")
    transport, sftp_client = sftp(box["username"], PW)
    assert sftp_client.getcwd() == "/home" or sftp_client.normalize(".") == "/home"
    sftp_client.mkdir("data")
    with sftp_client.open("data/hello", "w") as file:
        file.write("hi")
    with pytest.raises(IOError):
        sftp_client.listdir("/hlab")                                   # nothing outside the account's directory
    transport.close()
    assert api("GET", f"/storage_boxes/{box['id']}/folders?path=.")["folders"] == ["data"]


def test_ports(live_box):
    _, box = live_box
    code, out = ssh_cmd(box["username"], PW, 22, "ls")
    assert code != 0 and "This service allows sftp connections only." in out   # as on Hetzner
    code, out = ssh_cmd(box["username"], PW, 23, "ls data")
    assert code == 0 and out.strip() == "hello"
    code, out = ssh_cmd(box["username"], PW, 23, "sha256sum data/hello")
    assert out.startswith("8f434346648f6b96df89dda901c5176b10a6d83961dd3c1ac88b59b2dc327aa4")
    code, out = ssh_cmd(box["username"], PW, 23, "tail -n 1 data/hello")   # more commands like on port 23 at Hetzner
    assert code == 0 and out.strip() == "hi"
    code, out = ssh_cmd(box["username"], PW, 23, "bash")
    assert code != 0 and "not allowed" in out


def test_authorized_keys_in_the_box(lab: Lab, live_box, tmp_path):
    _, box = live_box
    subprocess.run(["ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", str(tmp_path / "k")], check=True)
    transport, sftp_client = sftp(box["username"], PW)
    sftp_client.mkdir(".ssh")
    sftp_client.put(str(tmp_path / "k.pub"), ".ssh/authorized_keys")
    transport.close()
    key = paramiko.Ed25519Key.from_private_key_file(str(tmp_path / "k"))
    transport, sftp_client = sftp(box["username"], key=key, port=23, host="2001:db8:fffe::1")
    assert "data" in sftp_client.listdir(".")
    transport.close()
    with pytest.raises(paramiko.AuthenticationException):   # port 22 takes RFC4716 keys only, as on Hetzner
        sftp(box["username"], key=key, port=22)
    rfc = subprocess.run(["ssh-keygen", "-e", "-f", str(tmp_path / "k.pub")], check=True, capture_output=True,
                         text=True).stdout
    transport, sftp_client = sftp(box["username"], PW)
    with sftp_client.open(".ssh/authorized_keys", "a") as file:
        file.write(rfc)
    transport.close()
    transport, sftp_client = sftp(box["username"], key=key, port=22)
    assert "data" in sftp_client.listdir(".")
    transport.close()


def test_subaccount_readonly_in_its_directory(live_box):
    api, box = live_box
    response = api("POST", f"/storage_boxes/{box['id']}/subaccounts", {
        "home_directory": "data", "password": "Sub-Secret-123",
        "access_settings": {"ssh_enabled": True, "readonly": True, "reachable_externally": True}})
    subaccount = response["subaccount"]
    try:
        transport, sftp_client = sftp(subaccount["username"], "Sub-Secret-123")
        assert sftp_client.listdir(".") == ["hello"]
        # rsync may send from a read-only account, but not remove what it sent
        code, out = ssh_cmd(subaccount["username"], "Sub-Secret-123", 23,
                            "rsync --server --sender -logDtpre.iLsfxCIvu --remove-source-files . hello")
        assert code != 0 and "read-only" in out
        with pytest.raises(IOError):
            with sftp_client.open("new", "w") as file:
                file.write("x")
        transport.close()
        api("POST", f"/storage_boxes/{box['id']}/subaccounts/{subaccount['id']}/actions/update_access_settings",
            {"readonly": False})
        transport, sftp_client = sftp(subaccount["username"], "Sub-Secret-123")
        with sftp_client.open("new", "w") as file:
            file.write("x")
        transport.close()
        transport, sftp_client = sftp(box["username"], PW)
        assert "new" in sftp_client.listdir("data")                  # the main account sees the sub-account's files
        sftp_client.remove("data/new")
        transport.close()
    finally:
        api("DELETE", f"/storage_boxes/{box['id']}/subaccounts/{subaccount['id']}")




def test_ssh_disabled_and_internal_only(lab: Lab, live_box):
    api, box = live_box
    api.wait(api("POST", f"/storage_boxes/{box['id']}/actions/update_access_settings", {"ssh_enabled": False})["action"])
    transport, sftp_client = sftp(box["username"], PW)                         # SFTP on port 22 is always on, as on Hetzner
    assert "data" in sftp_client.listdir(".")
    transport.close()
    with pytest.raises(Exception):                          # port 23 is the SSH support
        transport, sftp_client = sftp(box["username"], PW, port=23)
        sftp_client.listdir(".")
    api.wait(api("POST", f"/storage_boxes/{box['id']}/actions/update_access_settings",
                 {"ssh_enabled": True, "reachable_externally": False})["action"])
    try:
        with pytest.raises(Exception):                      # the lab's tools are on the Internet
            transport, sftp_client = sftp(box["username"], PW)
            sftp_client.listdir(".")
        # from a server (Hetzner's network) it works: SFTP with a key from that server
        pub = lab.sh("a", "test -f /root/.ssh/box || ssh-keygen -q -t ed25519 -N '' -f /root/.ssh/box; "
                     "cat /root/.ssh/box.pub")
        api.wait(api("POST", f"/storage_boxes/{box['id']}/actions/update_access_settings",
                     {"reachable_externally": True})["action"])
        transport, sftp_client = sftp(box["username"], PW)
        with sftp_client.open(".ssh/authorized_keys", "a") as file:
            file.write(pub + "\n")
        transport.close()
        api.wait(api("POST", f"/storage_boxes/{box['id']}/actions/update_access_settings",
                     {"reachable_externally": False})["action"])
        out = lab.sh("a", f"echo 'ls data' | sftp -q -P 23 -i /root/.ssh/box -o StrictHostKeyChecking=no "
                     f"-b - {box['username']}@{box['server']}")
        assert "hello" in out
    finally:
        api.wait(api("POST", f"/storage_boxes/{box['id']}/actions/update_access_settings",
                     {"reachable_externally": True, "ssh_enabled": True})["action"])


def test_snapshots_zfs_view_and_rollback(live_box):
    api, box = live_box
    api.wait(api("POST", f"/storage_boxes/{box['id']}/actions/update_access_settings", {"zfs_enabled": True})["action"])
    snap = api("POST", f"/storage_boxes/{box['id']}/snapshots", {"description": "live"})["snapshot"]
    transport, sftp_client = sftp(box["username"], PW)
    assert snap["name"] in sftp_client.listdir(".zfs/snapshot")
    assert sftp_client.listdir(f".zfs/snapshot/{snap['name']}/data") == ["hello"]
    with pytest.raises(IOError):
        sftp_client.remove(f".zfs/snapshot/{snap['name']}/data/hello")   # snapshots are read-only
    sftp_client.remove("data/hello")
    transport.close()
    api.wait(api("POST", f"/storage_boxes/{box['id']}/actions/rollback_snapshot", {"snapshot": snap["id"]})["action"])
    transport, sftp_client = sftp(box["username"], PW)
    assert sftp_client.listdir("data") == ["hello"]
    transport.close()
    stats = api("GET", f"/storage_boxes/{box['id']}")["storage_box"]["stats"]
    assert stats["size_data"] > 0 and stats["size_snapshots"] > 0
