"""Server metrics: CPU, disk and network of the running servers over time.

A sampler reads the counters of every running VM each `SAMPLE_SECONDS` (the fabric's
`VMCounters`) and keeps them in memory for `KEEP_SECONDS` (`lab.samples`). A request
turns them into rates and averages those per step, as Hetzner's
/servers/{id}/metrics does. Hetzner keeps 30 days; the lab keeps one, and none across
a restart.
"""

import asyncio
import logging
import math
import time
from collections import deque
from datetime import datetime, timezone

from fastapi import FastAPI, Request

from ..errors import invalid
from ..fabric.vm import VMCounters
from ..lab import Lab
from ..state import SERVERS

log = logging.getLogger(__name__)

SAMPLE_SECONDS = 15
KEEP_SECONDS = 24 * 3600
# Hetzner: without a step about 200 points come back, never more than 500
DEFAULT_POINTS = 200
MAX_POINTS = 500

# Hetzner's time series per metric type: name → (counter, factor on its rate). The CPU
# counts per vCPU: 100 is one busy vCPU.
SERIES: dict[str, dict[str, tuple[str, float]]] = {
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


# --- sampling -------------------------------------------------------------------------


async def sampler(lab: Lab) -> None:
    """Take samples of the running servers as long as the lab runs."""
    while True:
        await asyncio.sleep(SAMPLE_SECONDS)
        try:
            await take_samples(lab)
        except Exception:  # the sampler keeps going; the error is in the log
            log.exception("metrics sampler")


async def take_samples(lab: Lab) -> None:
    """Read the counters of every running server; forget deleted servers."""
    servers = lab.state[SERVERS]
    for sid in [sid for sid in lab.samples if sid not in servers]:
        del lab.samples[sid]
    # the IDs and taps: servers may change while the scripts run
    running = [
        (sid, server["_lab"]["tap"])
        for sid, server in servers.items()
        if server["status"] == "running"
    ]
    for sid, tap in running:
        counters = await lab.blocking(lab.fabric.vms.counters, sid, tap)
        if counters is not None and sid in lab.state[SERVERS]:
            kept = lab.samples.setdefault(
                sid, deque(maxlen=KEEP_SECONDS // SAMPLE_SECONDS)
            )
            kept.append((time.time(), counters))


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
    samples: list[tuple[float, VMCounters]], counter: str, factor: float
) -> list[tuple[float, float]]:
    """Return a counter's rate between consecutive samples, at the later one's time.

    A counter that went down belongs to a VM that started again: that pair is left out.
    """
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
    lab: Lab, sid: int, types: list[str], start: float, end: float, step: int
) -> dict:
    """Return the metrics of a server for the types between start and end."""
    samples = list(lab.samples.get(sid, ()))
    time_series = {
        name: {"values": averaged(rates(samples, counter, factor), start, end, step)}
        for kind in types
        for name, (counter, factor) in SERIES[kind].items()
    }
    return {
        "start": iso(start),
        "end": iso(end),
        "step": step,
        "time_series": time_series,
    }


def add_routes(app: FastAPI, lab: Lab) -> None:
    """Add the metrics route of servers to the app."""

    @app.get("/v1/servers/{sid}/metrics")
    async def server_metrics(sid: int, request: Request):
        lab.server_or_404(sid)
        query = request.query_params
        types = [t for value in query.getlist("type") for t in value.split(",") if t]
        unknown = [t for t in types if t not in SERIES]
        if not types or unknown:
            raise invalid("type must be cpu, disk or network", "type")
        start = parse_time(query.get("start"), "start")
        end = parse_time(query.get("end"), "end")
        if end < start:
            raise invalid("end is before start", "end")
        step = chosen_step(start, end, query.get("step"))
        return {"metrics": metrics_json(lab, sid, types, start, end, step)}
