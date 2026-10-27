"""Server metrics: samples of the VMs' counters, turned into Hetzner's time series."""

from collections import deque
from dataclasses import replace

from conftest import create_server

from hlabd.api import metrics
from hlabd.fabric.balancers import BalancerCounters
from hlabd.fabric.vm import VMCounters, VMs

START = 1_800_000_000  # a Unix time in 2027, on a whole minute
ZERO = VMCounters(0.0, 0, 0, 0, 0, 0, 0, 0, 0)


def give_samples(api, sid, samples):
    """Put samples of a server into the lab, as the sampler would."""

    def put():
        api.lab.samples[sid] = deque(samples)

    api.on_loop(put)


def query(api, sid, **params):
    """Return the metrics of a server for a minute from START."""
    params = {"start": "2027-01-15T08:00:00Z", "end": "2027-01-15T08:01:00Z", **params}
    response = api.get(f"/v1/servers/{sid}/metrics", params=params)
    assert response.status_code == 200, response.text
    return response.json()["metrics"]


def test_counters_become_rates_per_step(api):
    # Hetzner: CPU in percent of one vCPU, disk and network per second
    sid = create_server(api, "a")["server"]["id"]
    start = metrics.parse_time("2027-01-15T08:00:00Z", "start")
    give_samples(
        api,
        sid,
        [
            (start, ZERO),
            (start + 10, replace(ZERO, cpu_seconds=5.0, bytes_out=10_000)),
            (start + 20, replace(ZERO, cpu_seconds=20.0, bytes_out=30_000)),
        ],
    )
    result = query(api, sid, type="cpu,network", step="60")
    assert (result["start"], result["step"]) == ("2027-01-15T08:00:00Z", 60)
    # 50 % and 150 % in the two intervals, averaged over the step
    assert result["time_series"]["cpu"]["values"] == [[start, "100"]]
    out = result["time_series"]["network.0.bandwidth.out"]["values"]
    assert out == [[start, "1500"]]
    assert "disk.0.iops.read" not in result["time_series"]


def test_a_vm_started_again_leaves_out_the_drop(api):
    sid = create_server(api, "a")["server"]["id"]
    start = metrics.parse_time("2027-01-15T08:00:00Z", "start")
    give_samples(
        api,
        sid,
        [
            (start, replace(ZERO, disk_reads=500)),
            (start + 10, ZERO),  # the counters began again
            (start + 20, replace(ZERO, disk_reads=100)),
        ],
    )
    result = query(api, sid, type="disk", step="5")
    assert result["time_series"]["disk.0.iops.read"]["values"] == [[start + 20, "10"]]


def test_without_samples_the_series_are_empty_and_the_step_is_chosen(api):
    # Hetzner: without a step, about 200 points; never more than 500
    sid = create_server(api, "a")["server"]["id"]
    result = query(api, sid, type="disk", end="2027-01-15T10:00:00Z")
    assert result["step"] == 36  # two hours / 200
    assert result["time_series"]["disk.0.bandwidth.write"]["values"] == []
    result = query(api, sid, type="cpu", end="2027-01-15T10:00:00Z", step="1")
    assert result["step"] == 15  # two hours / 500


def test_wrong_metric_requests_are_invalid(api):
    sid = create_server(api, "a")["server"]["id"]
    path = f"/v1/servers/{sid}/metrics"
    times = {"start": "2027-01-15T08:00:00Z", "end": "2027-01-15T07:00:00Z"}
    response = api.get(path, params={"type": "cpu", **times})
    assert response.json()["error"]["code"] == "invalid_input"
    response = api.get(path, params={"type": "cpu", **times, "start": "yesterday"})
    assert response.json()["error"]["code"] == "invalid_input"
    assert (
        api.get("/v1/servers/999/metrics", params={"type": "cpu", **times}).status_code
        == 404
    )


def test_the_sampler_reads_running_servers_and_forgets_deleted_ones(api, monkeypatch):
    sid = create_server(api, "a")["server"]["id"]
    read = []

    def counters(vm_sid, tap):
        read.append((vm_sid, tap))
        return ZERO

    monkeypatch.setattr(api.lab.fabric.vms, "counters", counters)
    give_samples(api, 999, [(START, ZERO)])  # a server that is gone
    api.on_loop(metrics.take_samples, api.lab)
    assert read == [(sid, "pub1")]
    assert api.on_loop(lambda: sorted(api.lab.samples)) == [sid]


class AnsweringShell:
    """A shell whose print-vm-counters prints the given answer."""

    def __init__(self, answer):
        """Answer with `answer`."""
        self.answer = answer

    def run(self, *args, check=True, timeout=None):
        """Return the answer."""
        return self.answer


def test_counters_are_read_from_the_verb_and_missing_ones_count_as_none(tmp_path):
    answer = (
        '{"cpu_seconds": 1.5, "disk_reads": 1, "disk_writes": 2,'
        ' "disk_read_bytes": 3, "disk_write_bytes": 4, "packets_in": 5,'
        ' "packets_out": 6, "bytes_in": 7, "bytes_out": 8}'
    )
    vms = VMs({}, tmp_path, AnsweringShell(answer))
    assert vms.counters(1, "pub1") == VMCounters(1.5, 1, 2, 3, 4, 5, 6, 7, 8)
    vms = VMs({}, tmp_path, AnsweringShell(answer.replace("1,", "null,", 1)))
    assert vms.counters(1, "pub1") is None
    assert VMs({}, tmp_path, AnsweringShell("")).counters(1, "pub1") is None


def test_balancer_metrics_show_connections_requests_and_bandwidth(api):
    balancer = api.post(
        "/v1/load_balancers",
        json={"name": "lb", "load_balancer_type": "lb11", "location": "fsn1"},
    ).json()["load_balancer"]
    start = metrics.parse_time("2027-01-15T08:00:00Z", "start")

    def put():
        api.lab.balancer_samples[balancer["id"]] = deque(
            [
                (start, BalancerCounters(4, 100, 50, 0, 0)),
                (start + 10, BalancerCounters(6, 200, 150, 10_000, 40_000)),
            ]
        )

    api.on_loop(put)
    response = api.get(
        f"/v1/load_balancers/{balancer['id']}/metrics",
        params={
            "type": "open_connections,requests_per_second,bandwidth",
            "start": "2027-01-15T08:00:00Z",
            "end": "2027-01-15T08:01:00Z",
            "step": "60",
        },
    )
    assert response.status_code == 200, response.text
    series = response.json()["metrics"]["time_series"]
    # a level is averaged as it is; counters become rates per second
    assert series["open_connections"]["values"] == [[start, "5"]]
    assert series["requests_per_second"]["values"] == [[start, "10"]]
    assert series["bandwidth.out"]["values"] == [[start, "4000"]]
