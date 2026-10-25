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
        r = self.http.request(method, path, json=body)
        assert r.status_code < 300, f"{method} {path}: {r.status_code} {r.text}"
        return r.json() if r.content else {}

    def wait(self, a: dict) -> None:
        for _ in range(120):
            a = self("GET", f"/storage_boxes/actions/{a['id']}")["action"]
            if a["status"] != "running":
                assert a["status"] == "success", a
                return
            time.sleep(0.5)
        raise AssertionError(a)


@pytest.fixture(scope="module")
def box():
    api = BoxApi()
    r = api("POST", "/storage_boxes", {"name": "live", "location": "fsn1", "storage_box_type": "bx11",
                                       "password": PW, "access_settings": {"ssh_enabled": True,
                                                                           "reachable_externally": True}})
    api.wait(r["action"])
    b = api("GET", f"/storage_boxes/{r['storage_box']['id']}")["storage_box"]
    yield api, b
    api("POST", f"/storage_boxes/{b['id']}/actions/change_protection", {"delete": False})
    api.wait(api("DELETE", f"/storage_boxes/{b['id']}")["action"])


def sftp(user: str, password: str | None = None, port: int = 22, host: str = "198.18.0.1", key=None):
    t = paramiko.Transport((host, port))
    t.connect(username=user, password=password, pkey=key)
    return t, paramiko.SFTPClient.from_transport(t)


def ssh_cmd(user: str, password: str, port: int, cmd: str) -> tuple[int, str]:
    c = paramiko.SSHClient()
    c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    c.connect("198.18.0.1", port=port, username=user, password=password, allow_agent=False, look_for_keys=False)
    _, out, err = c.exec_command(cmd)
    code = out.channel.recv_exit_status()
    text = out.read().decode() + err.read().decode()
    c.close()
    return code, text


def test_box_resolves_and_logs_in_with_password(lab: Lab, box):
    api, b = box
    assert b["server"] == f"u{b['id']}.your-storagebox.de"
    assert lab.sh("a", f"getent ahosts {b['server']} | head -1").split()[0] in ("198.18.0.1", "2001:db8:fffe::1")
    t, s = sftp(b["username"], PW)
    assert s.getcwd() == "/home" or s.normalize(".") == "/home"
    s.mkdir("data")
    with s.open("data/hello", "w") as f:
        f.write("hi")
    with pytest.raises(IOError):
        s.listdir("/hlab")                                   # nothing outside the account's directory
    t.close()
    assert api("GET", f"/storage_boxes/{b['id']}/folders?path=.")["folders"] == ["data"]


def test_ports(box):
    _, b = box
    code, out = ssh_cmd(b["username"], PW, 22, "ls")
    assert code != 0 and "This service allows sftp connections only." in out   # as on Hetzner
    code, out = ssh_cmd(b["username"], PW, 23, "ls data")
    assert code == 0 and out.strip() == "hello"
    code, out = ssh_cmd(b["username"], PW, 23, "sha256sum data/hello")
    assert out.startswith("8f434346648f6b96df89dda901c5176b10a6d83961dd3c1ac88b59b2dc327aa4")
    code, out = ssh_cmd(b["username"], PW, 23, "tail -n 1 data/hello")   # more commands like on port 23 at Hetzner
    assert code == 0 and out.strip() == "hi"
    code, out = ssh_cmd(b["username"], PW, 23, "bash")
    assert code != 0 and "not allowed" in out


def test_authorized_keys_in_the_box(lab: Lab, box, tmp_path):
    _, b = box
    subprocess.run(["ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", str(tmp_path / "k")], check=True)
    t, s = sftp(b["username"], PW)
    s.mkdir(".ssh")
    s.put(str(tmp_path / "k.pub"), ".ssh/authorized_keys")
    t.close()
    key = paramiko.Ed25519Key.from_private_key_file(str(tmp_path / "k"))
    t, s = sftp(b["username"], key=key, port=23, host="2001:db8:fffe::1")
    assert "data" in s.listdir(".")
    t.close()


def test_subaccount_readonly_in_its_directory(box):
    api, b = box
    r = api("POST", f"/storage_boxes/{b['id']}/subaccounts", {
        "home_directory": "data", "password": "Sub-Secret-123",
        "access_settings": {"ssh_enabled": True, "readonly": True, "reachable_externally": True}})
    a = r["subaccount"]
    try:
        t, s = sftp(a["username"], "Sub-Secret-123")
        assert s.listdir(".") == ["hello"]
        # rsync may send from a read-only account, but not remove what it sent
        code, out = ssh_cmd(a["username"], "Sub-Secret-123", 23,
                            "rsync --server --sender -logDtpre.iLsfxCIvu --remove-source-files . hello")
        assert code != 0 and "read-only" in out
        with pytest.raises(IOError):
            with s.open("new", "w") as f:
                f.write("x")
        t.close()
        api("POST", f"/storage_boxes/{b['id']}/subaccounts/{a['id']}/actions/update_access_settings",
            {"readonly": False})
        t, s = sftp(a["username"], "Sub-Secret-123")
        with s.open("new", "w") as f:
            f.write("x")
        t.close()
        t, s = sftp(b["username"], PW)
        assert "new" in s.listdir("data")                  # the main account sees the sub-account's files
        s.remove("data/new")
        t.close()
    finally:
        api("DELETE", f"/storage_boxes/{b['id']}/subaccounts/{a['id']}")




def test_ssh_disabled_and_internal_only(lab: Lab, box):
    api, b = box
    api.wait(api("POST", f"/storage_boxes/{b['id']}/actions/update_access_settings", {"ssh_enabled": False})["action"])
    with pytest.raises(Exception):
        t, s = sftp(b["username"], PW)
        s.listdir(".")
    api.wait(api("POST", f"/storage_boxes/{b['id']}/actions/update_access_settings",
                 {"ssh_enabled": True, "reachable_externally": False})["action"])
    try:
        with pytest.raises(Exception):                      # the lab's tools are on the Internet
            t, s = sftp(b["username"], PW)
            s.listdir(".")
        # from a server (Hetzner's network) it works: SFTP with a key from that server
        pub = lab.sh("a", "test -f /root/.ssh/box || ssh-keygen -q -t ed25519 -N '' -f /root/.ssh/box; "
                          "cat /root/.ssh/box.pub")
        api.wait(api("POST", f"/storage_boxes/{b['id']}/actions/update_access_settings",
                     {"reachable_externally": True})["action"])
        t, s = sftp(b["username"], PW)
        with s.open(".ssh/authorized_keys", "a") as f:
            f.write(pub + "\n")
        t.close()
        api.wait(api("POST", f"/storage_boxes/{b['id']}/actions/update_access_settings",
                     {"reachable_externally": False})["action"])
        out = lab.sh("a", f"echo 'ls data' | sftp -q -P 23 -i /root/.ssh/box -o StrictHostKeyChecking=no "
                          f"-b - {b['username']}@{b['server']}")
        assert "hello" in out
    finally:
        api.wait(api("POST", f"/storage_boxes/{b['id']}/actions/update_access_settings",
                     {"reachable_externally": True, "ssh_enabled": True})["action"])


def test_snapshots_zfs_view_and_rollback(box):
    api, b = box
    api.wait(api("POST", f"/storage_boxes/{b['id']}/actions/update_access_settings", {"zfs_enabled": True})["action"])
    snap = api("POST", f"/storage_boxes/{b['id']}/snapshots", {"description": "live"})["snapshot"]
    t, s = sftp(b["username"], PW)
    assert snap["name"] in s.listdir(".zfs/snapshot")
    assert s.listdir(f".zfs/snapshot/{snap['name']}/data") == ["hello"]
    with pytest.raises(IOError):
        s.remove(f".zfs/snapshot/{snap['name']}/data/hello")   # snapshots are read-only
    s.remove("data/hello")
    t.close()
    api.wait(api("POST", f"/storage_boxes/{b['id']}/actions/rollback_snapshot", {"snapshot": snap["id"]})["action"])
    t, s = sftp(b["username"], PW)
    assert s.listdir("data") == ["hello"]
    t.close()
    stats = api("GET", f"/storage_boxes/{b['id']}")["storage_box"]["stats"]
    assert stats["size_data"] > 0 and stats["size_snapshots"] > 0
