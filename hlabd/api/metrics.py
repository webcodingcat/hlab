"""Metrics of servers and load balancers over time.

A sampler reads the counters of every running VM (the fabric's `VMCounters`) and of
every load balancer's HAProxy (`BalancerCounters`) each `SAMPLE_SECONDS`, and keeps
them for `KEEP_SECONDS` in `lab.readings`, in memory. A request turns them into
rates and averages those per step, as Hetzner's /metrics endpoints do. Hetzner keeps
30 days; the lab keeps one, and none across a restart.
"""

import asyncio
import logging
import math
import time
from collections import deque
from collections.abc import Sequence
from datetime import datetime, timezone

from fastapi import FastAPI, Request

from ..errors import invalid
from ..lab import Lab
from ..state import LOAD_BALANCERS, SERVERS

log = logging.getLogger(__name__)

SAMPLE_SECONDS = 15
KEEP_SECONDS = 24 * 3600
# Hetzner: without a step about 200 points come back, never more than 500
DEFAULT_POINTS = 200
MAX_POINTS = 500

# Hetzner's time series per metric type: name → (counter, factor on its rate); no
# factor: the counter is a level, shown as it is. The CPU counts per vCPU: 100 is one
# busy vCPU.
type Series = dict[str, dict[str, tuple[str, float | None]]]
SERVER_SERIES: Series = {
    "cpu": {"cpu": ("cpu_seconds", 100)},
    "disk": {
        "disk.0.iops.read": ("disk_reads", 1),
        "disk.0.iops.write": ("disk_writes", 1),
        "disk.0.bandwidth.read": ("disk_read_bytes", 1),
        "disk.0.bandwidth.write": ("disk_write_bytes", 1),
    },
    "network": {
        "network.0.pps.in": ("packets_in", 1),
        "network.0.pps.out": ("packets_out", 1),
        "network.0.bandwidth.in": ("bytes_in", 1),
        "network.0.bandwidth.out": ("bytes_out", 1),
    },
}
BALANCER_SERIES: Series = {
    "open_connections": {"open_connections": ("open_connections", None)},
    "connections_per_second": {"connections_per_second": ("connections", 1)},
    "requests_per_second": {"requests_per_second": ("requests", 1)},
    "bandwidth": {"bandwidth.in": ("bytes_in", 1), "bandwidth.out": ("bytes_out", 1)},
}


# --- sampling -------------------------------------------------------------------------


async def sampler(lab: Lab) -> None:
    """Take samples of the running servers and the load balancers while the lab runs."""
    while True:
        await asyncio.sleep(SAMPLE_SECONDS)
        if lab.resetting:
            continue
        try:
            await take_samples(lab)
        except Exception:  # the sampler keeps going; the error is in the log
            log.exception("metrics sampler")


async def take_samples(lab: Lab) -> None:
    """Read the counters of every running server and every load balancer.

    Deleted ones are forgotten.
    """
    servers = lab.state[SERVERS]
    for sid in [sid for sid in lab.readings.vms if sid not in servers]:
        del lab.readings.vms[sid]
    # the IDs and taps: servers may change while the scripts run
    running = [
        (sid, server["_lab"]["tap"])
        for sid, server in servers.items()
        if server["status"] == "running"
    ]
    for sid, tap in running:
        counters = await lab.blocking(lab.fabric.vms.counters, sid, tap)
        if counters is not None and sid in lab.state[SERVERS]:
            kept = lab.readings.vms.setdefault(
                sid, deque(maxlen=KEEP_SECONDS // SAMPLE_SECONDS)
            )
            kept.append((time.time(), counters))
    balancers = lab.state[LOAD_BALANCERS]
    for lid in [lid for lid in lab.readings.balancers if lid not in balancers]:
        del lab.readings.balancers[lid]
    for lid in list(balancers):
        totals = await lab.blocking(lab.fabric.balancers.counters, lid)
        if totals is not None and lid in lab.state[LOAD_BALANCERS]:
            kept_totals = lab.readings.balancers.setdefault(
                lid, deque(maxlen=KEEP_SECONDS // SAMPLE_SECONDS)
            )
            kept_totals.append((time.time(), totals))


# --- the API --------------------------------------------------------------------------


def parse_time(value: str | None, field: str) -> float:
    """Return an ISO 8601 time of the request as a Unix time (UTC if it has no zone)."""
    if not value:
        raise invalid(f"{field} is required", field)
    try:
        moment = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError:
        raise invalid(f"{field} must be an ISO 8601 time", field) from None
    if moment.tzinfo is None:
        moment = moment.replace(tzinfo=timezone.utc)
    return moment.timestamp()


def iso(moment: float) -> str:
    """Return a Unix time as Hetzner writes it: ISO 8601 in UTC."""
    return datetime.fromtimestamp(moment, timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def chosen_step(start: float, end: float, step: str | None) -> int:
    """Return the step in seconds: the requested one, made coarser beyond 500 points.

    Without a step, Hetzner picks one for about 200 points.
    """
    span = end - start
    if step is None:
        return max(1, math.ceil(span / DEFAULT_POINTS))
    try:
        seconds = max(1, int(float(step)))
    except ValueError:
        raise invalid("step must be a number of seconds", "step") from None
    return max(seconds, math.ceil(span / MAX_POINTS))


def rates(
    samples: Sequence[tuple[float, object]], counter: str, factor: float | None
) -> list[tuple[float, float]]:
    """Return a counter's rate between consecutive samples, at the later one's time.

    A counter that went down belongs to a VM or HAProxy that started again: that pair
    is left out. Without a factor the counter is a level: each sample as it is.
    """
    if factor is None:
        return [(moment, float(getattr(totals, counter))) for moment, totals in samples]
    out = []
    for (before, old), (after, new) in zip(samples, samples[1:], strict=False):
        delta = getattr(new, counter) - getattr(old, counter)
        if delta >= 0 and after > before:
            out.append((after, delta / (after - before) * factor))
    return out


def averaged(
    points: list[tuple[float, float]], start: float, end: float, step: int
) -> list[list]:
    """Return the points' average per step from `start`, as Hetzner's [time, "value"].

    A step without points is left out.
    """
    buckets: dict[int, list[float]] = {}
    for moment, value in points:
        if start <= moment <= end:
            buckets.setdefault(int((moment - start) // step), []).append(value)
    return [
        [start + index * step, f"{sum(values) / len(values):.6g}"]
        for index, values in sorted(buckets.items())
    ]


def metrics_json(
    samples: Sequence[tuple[float, object]], series: Series, request: Request
) -> dict:
    """Return the metrics a request asks for, from a server's or balancer's samples."""
    query = request.query_params
    types = [t for value in query.getlist("type") for t in value.split(",") if t]
    if not types or any(t not in series for t in types):
        raise invalid(f"type must be one of {', '.join(series)}", "type")
    start = parse_time(query.get("start"), "start")
    end = parse_time(query.get("end"), "end")
    if end < start:
        raise invalid("end is before start", "end")
    step = chosen_step(start, end, query.get("step"))
    time_series = {
        name: {"values": averaged(rates(samples, counter, factor), start, end, step)}
        for kind in types
        for name, (counter, factor) in series[kind].items()
    }
    return {
        "start": iso(start),
        "end": iso(end),
        "step": step,
        "time_series": time_series,
    }


def add_routes(app: FastAPI, lab: Lab) -> None:
    """Add the metrics routes of servers and load balancers to the app."""

    @app.get("/v1/servers/{sid}/metrics")
    async def server_metrics(sid: int, request: Request):
        lab.server_or_404(sid)
        samples = list(lab.readings.vms.get(sid, ()))
        return {"metrics": metrics_json(samples, SERVER_SERIES, request)}

    @app.get("/v1/load_balancers/{lid}/metrics")
    async def load_balancer_metrics(lid: int, request: Request):
        lab.get_or_404(LOAD_BALANCERS, lid, "load balancer")
        samples = list(lab.readings.balancers.get(lid, ()))
        return {"metrics": metrics_json(samples, BALANCER_SERIES, request)}
