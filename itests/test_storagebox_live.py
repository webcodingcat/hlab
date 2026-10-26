"""Live tests of Storage Boxes.

Most are made through the Storage Box API, the api.hetzner.com stand-in on 198.18.0.3.
Covered: logins, ports 22 and 23, sub-accounts, access settings, snapshots, and the
lab's own box account (SFTP renames, what the account sees).
"""

import subprocess
import time

import paramiko
import pytest
from livelab import LiveLab

BOX_API = "http://198.18.0.3/v1"
PW = "Box-Secret-123"
BOX_ACTIONS = "/storage_boxes"  # where the Storage Box API lists its actions


@pytest.fixture(scope="module")
def live_box():
    """Yield the Storage Box API and a box with SSH reachable from outside.

    The box is deleted after the module's tests.
    """
    api = LiveLab(BOX_API)
    response = api.api(
        "POST",
        "/storage_boxes",
        {
            "name": "live",
            "location": "fsn1",
            "storage_box_type": "bx11",
            "password": PW,
            "access_settings": {"ssh_enabled": True, "reachable_externally": True},
        },
    )
    api.wait_action(response["action"], prefix=BOX_ACTIONS)
    live_box = api.api("GET", f"/storage_boxes/{response['storage_box']['id']}")[
        "storage_box"
    ]
    yield api, live_box
    api.api(
        "POST",
        f"/storage_boxes/{live_box['id']}/actions/change_protection",
        {"delete": False},
    )
    api.wait_action(
        api.api("DELETE", f"/storage_boxes/{live_box['id']}")["action"],
        prefix=BOX_ACTIONS,
    )


def sftp(
    user: str,
    password: str | None = None,
    port: int = 22,
    host: str = "198.18.0.1",
    key=None,
):
    """Return an SFTP session to the box: its transport and its client."""
    transport = paramiko.Transport((host, port))
    transport.connect(username=user, password=password, pkey=key)
    return transport, paramiko.SFTPClient.from_transport(transport)


def ssh_cmd(user: str, password: str, port: int, cmd: str) -> tuple[int, str]:
    """Run a command on the box over SSH; return its exit code and its output."""
    ssh_client = paramiko.SSHClient()
    ssh_client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    ssh_client.connect(
        "198.18.0.1",
        port=port,
        username=user,
        password=password,
        allow_agent=False,
        look_for_keys=False,
    )
    _, out, err = ssh_client.exec_command(cmd)
    code = out.channel.recv_exit_status()
    text = out.read().decode() + err.read().decode()
    ssh_client.close()
    return code, text


def make_keypair(tmp_path) -> str:
    """Create an ed25519 key pair without passphrase; return its private key file."""
    subprocess.run(
        ["ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", str(tmp_path / "k")],
        check=True,
    )
    return str(tmp_path / "k")


def test_box_resolves_and_logs_in_with_password(lab: LiveLab, live_box):
    api, box = live_box
    assert box["server"] == f"u{box['id']}.your-storagebox.de"
    assert lab.check_output("a", f"getent ahosts {box['server']} | head -1").split()[
        0
    ] in (
        "198.18.0.1",
        "2001:db8:fffe::1",
    )
    transport, sftp_client = sftp(box["username"], PW)
    assert sftp_client.getcwd() == "/home" or sftp_client.normalize(".") == "/home"
    sftp_client.mkdir("data")
    with sftp_client.open("data/hello", "w") as file:
        file.write("hi")
    with pytest.raises(IOError):
        sftp_client.listdir("/hlab")  # nothing outside the account's directory
    transport.close()
    assert api.api("GET", f"/storage_boxes/{box['id']}/folders?path=.")["folders"] == [
        "data"
    ]


def test_port_22_is_sftp_only_and_port_23_runs_some_commands(live_box):
    _, box = live_box
    code, out = ssh_cmd(box["username"], PW, 22, "ls")
    # as on Hetzner
    assert code != 0 and "This service allows sftp connections only." in out
    code, out = ssh_cmd(box["username"], PW, 23, "ls data")
    assert code == 0 and out.strip() == "hello"
    code, out = ssh_cmd(box["username"], PW, 23, "sha256sum data/hello")
    assert out.startswith(
        "8f434346648f6b96df89dda901c5176b10a6d83961dd3c1ac88b59b2dc327aa4"
    )
    # more commands like on port 23 at Hetzner
    code, out = ssh_cmd(box["username"], PW, 23, "tail -n 1 data/hello")
    assert code == 0 and out.strip() == "hi"
    code, out = ssh_cmd(box["username"], PW, 23, "bash")
    assert code != 0 and "not allowed" in out


def test_box_takes_authorized_keys_from_its_ssh_directory(
    lab: LiveLab, live_box, tmp_path
):
    _, box = live_box
    subprocess.run(
        ["ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", str(tmp_path / "k")],
        check=True,
    )
    transport, sftp_client = sftp(box["username"], PW)
    sftp_client.mkdir(".ssh")
    sftp_client.put(str(tmp_path / "k.pub"), ".ssh/authorized_keys")
    transport.close()
    key = paramiko.Ed25519Key.from_private_key_file(str(tmp_path / "k"))
    transport, sftp_client = sftp(
        box["username"], key=key, port=23, host="2001:db8:fffe::1"
    )
    assert "data" in sftp_client.listdir(".")
    transport.close()
    # port 22 takes RFC4716 keys only, as on Hetzner
    with pytest.raises(paramiko.AuthenticationException):
        sftp(box["username"], key=key, port=22)
    rfc = subprocess.run(
        ["ssh-keygen", "-e", "-f", str(tmp_path / "k.pub")],
        check=True,
        capture_output=True,
        text=True,
    ).stdout
    transport, sftp_client = sftp(box["username"], PW)
    with sftp_client.open(".ssh/authorized_keys", "a") as file:
        file.write(rfc)
    transport.close()
    transport, sftp_client = sftp(box["username"], key=key, port=22)
    assert "data" in sftp_client.listdir(".")
    transport.close()


def test_readonly_subaccount_stays_in_its_directory(live_box):
    api, box = live_box
    response = api.api(
        "POST",
        f"/storage_boxes/{box['id']}/subaccounts",
        {
            "home_directory": "data",
            "password": "Sub-Secret-123",
            "access_settings": {
                "ssh_enabled": True,
                "readonly": True,
                "reachable_externally": True,
            },
        },
    )
    subaccount = response["subaccount"]
    try:
        transport, sftp_client = sftp(subaccount["username"], "Sub-Secret-123")
        assert sftp_client.listdir(".") == ["hello"]
        # rsync may send from a read-only account, but not remove what it sent
        code, out = ssh_cmd(
            subaccount["username"],
            "Sub-Secret-123",
            23,
            "rsync --server --sender -logDtpre.iLsfxCIvu --remove-source-files . hello",
        )
        assert code != 0 and "read-only" in out
        with pytest.raises(IOError):
            with sftp_client.open("new", "w") as file:
                file.write("x")
        transport.close()
        api.api(
            "POST",
            f"/storage_boxes/{box['id']}/subaccounts/{subaccount['id']}/actions/update_access_settings",
            {"readonly": False},
        )
        transport, sftp_client = sftp(subaccount["username"], "Sub-Secret-123")
        with sftp_client.open("new", "w") as file:
            file.write("x")
        transport.close()
        transport, sftp_client = sftp(box["username"], PW)
        # the main account sees the sub-account's files
        assert "new" in sftp_client.listdir("data")
        sftp_client.remove("data/new")
        transport.close()
    finally:
        api.api("DELETE", f"/storage_boxes/{box['id']}/subaccounts/{subaccount['id']}")


def test_ssh_disabled_and_internal_only_close_only_their_doors(lab: LiveLab, live_box):
    api, box = live_box
    api.wait_action(
        api.api(
            "POST",
            f"/storage_boxes/{box['id']}/actions/update_access_settings",
            {"ssh_enabled": False},
        )["action"],
        prefix=BOX_ACTIONS,
    )
    # SFTP on port 22 is always on, as on Hetzner
    transport, sftp_client = sftp(box["username"], PW)
    assert "data" in sftp_client.listdir(".")
    transport.close()
    with pytest.raises(Exception):  # port 23 is the SSH support
        transport, sftp_client = sftp(box["username"], PW, port=23)
        sftp_client.listdir(".")
    api.wait_action(
        api.api(
            "POST",
            f"/storage_boxes/{box['id']}/actions/update_access_settings",
            {"ssh_enabled": True, "reachable_externally": False},
        )["action"],
        prefix=BOX_ACTIONS,
    )
    try:
        with pytest.raises(Exception):  # the lab's tools are on the Internet
            transport, sftp_client = sftp(box["username"], PW)
            sftp_client.listdir(".")
        # from a server (Hetzner's network) it works: SFTP with a key from that server
        pub = lab.check_output(
            "a",
            "test -f /root/.ssh/box || "
            "ssh-keygen -q -t ed25519 -N '' -f /root/.ssh/box; "
            "cat /root/.ssh/box.pub",
        )
        api.wait_action(
            api.api(
                "POST",
                f"/storage_boxes/{box['id']}/actions/update_access_settings",
                {"reachable_externally": True},
            )["action"],
            prefix=BOX_ACTIONS,
        )
        transport, sftp_client = sftp(box["username"], PW)
        with sftp_client.open(".ssh/authorized_keys", "a") as file:
            file.write(pub + "\n")
        transport.close()
        api.wait_action(
            api.api(
                "POST",
                f"/storage_boxes/{box['id']}/actions/update_access_settings",
                {"reachable_externally": False},
            )["action"],
            prefix=BOX_ACTIONS,
        )
        out = lab.check_output(
            "a",
            "echo 'ls data' | sftp -q -P 23 -i /root/.ssh/box "
            "-o StrictHostKeyChecking=no "
            f"-b - {box['username']}@{box['server']}",
        )
        assert "hello" in out
    finally:
        api.wait_action(
            api.api(
                "POST",
                f"/storage_boxes/{box['id']}/actions/update_access_settings",
                {"reachable_externally": True, "ssh_enabled": True},
            )["action"],
            prefix=BOX_ACTIONS,
        )


def test_snapshots_show_in_the_zfs_view_and_roll_back(live_box):
    api, box = live_box
    api.wait_action(
        api.api(
            "POST",
            f"/storage_boxes/{box['id']}/actions/update_access_settings",
            {"zfs_enabled": True},
        )["action"],
        prefix=BOX_ACTIONS,
    )
    snap = api.api(
        "POST", f"/storage_boxes/{box['id']}/snapshots", {"description": "live"}
    )["snapshot"]
    transport, sftp_client = sftp(box["username"], PW)
    assert snap["name"] in sftp_client.listdir(".zfs/snapshot")
    assert sftp_client.listdir(f".zfs/snapshot/{snap['name']}/data") == ["hello"]
    # snapshots are read-only
    with pytest.raises(IOError):
        sftp_client.remove(f".zfs/snapshot/{snap['name']}/data/hello")
    sftp_client.remove("data/hello")
    transport.close()
    api.wait_action(
        api.api(
            "POST",
            f"/storage_boxes/{box['id']}/actions/rollback_snapshot",
            {"snapshot": snap["id"]},
        )["action"],
        prefix=BOX_ACTIONS,
    )
    transport, sftp_client = sftp(box["username"], PW)
    assert sftp_client.listdir("data") == ["hello"]
    transport.close()
    stats = api.api("GET", f"/storage_boxes/{box['id']}")["storage_box"]["stats"]
    assert stats["size_data"] > 0 and stats["size_snapshots"] > 0


# --- the lab's own box account (POST /_lab/box/users) ---------------------------------


def test_box_account_renames_without_overwrite_and_sees_only_its_home(
    lab: LiveLab, tmp_path
):
    key = paramiko.Ed25519Key.from_private_key_file(make_keypair(tmp_path))
    lab.control(
        "POST",
        "/box/users",
        {"name": "backup1", "public_key": (tmp_path / "k.pub").read_text()},
    )
    for host in ("198.18.0.1", "2001:db8:fffe::1"):
        transport = paramiko.Transport((host, 23))
        transport.connect(username="backup1", pkey=key)
        sftp_client = paramiko.SFTPClient.from_transport(transport)
        directory = f"t-{time.time_ns()}"
        sftp_client.mkdir(directory)
        for name in ("one", "two"):
            with sftp_client.open(f"{directory}/tmp-{name}", "w") as file:
                file.write(name)
        sftp_client.rename(f"{directory}/tmp-one", f"{directory}/lease")
        # a plain rename never overwrites: exactly one winner
        with pytest.raises(IOError):
            sftp_client.rename(f"{directory}/tmp-two", f"{directory}/lease")
        with sftp_client.open(f"{directory}/lease") as file:
            assert file.read() == b"one"
        # posix-rename does overwrite
        sftp_client.posix_rename(f"{directory}/tmp-two", f"{directory}/lease")
        with sftp_client.open(f"{directory}/lease") as file:
            assert file.read() == b"two"
        sftp_client.remove(f"{directory}/lease")
        sftp_client.rmdir(directory)
        sftp_client.close()
        transport.close()
    out = subprocess.run(
        [
            "ssh",
            "-p",
            "23",
            "-i",
            str(tmp_path / "k"),
            "-o",
            "StrictHostKeyChecking=no",
            "-o",
            "UserKnownHostsFile=/dev/null",
            "backup1@198.18.0.1",
            "df",
        ],
        capture_output=True,
        text=True,
        timeout=20,
    )
    # the account sees only its own directory
    assert out.stdout.splitlines()[-1].endswith("/home")
    bad = subprocess.run(
        [
            "ssh",
            "-p",
            "23",
            "-i",
            str(tmp_path / "k"),
            "-o",
            "StrictHostKeyChecking=no",
            "-o",
            "UserKnownHostsFile=/dev/null",
            "backup1@198.18.0.1",
            "cat /etc/passwd",
        ],
        capture_output=True,
        text=True,
        timeout=20,
    )
    # the account's root is all it sees: its /etc/passwd knows only the account itself
    assert [line.split(":")[0] for line in bad.stdout.splitlines()] == ["backup1"]
