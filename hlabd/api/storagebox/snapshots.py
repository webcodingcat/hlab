"""Snapshots of a box: taken by hand or by its snapshot plan, rolled back to, deleted.

As on Hetzner, a snapshot exists at once, and its action copies the data afterwards.
Until then the record is marked `_copying`.
"""

import asyncio
import logging
from datetime import datetime, timedelta, timezone

from fastapi import FastAPI, Request

from ...errors import limit_exceeded, locked, not_found
from ...lab import Lab, Plan, RecordGoneError, now
from ...records import BoxSnapshot, StorageBox
from ...state import BOX_SNAPSHOTS, STORAGE_BOXES, SUBACCOUNTS
from ..web import filtered, read_body, sort_items, update_fields
from .base import box_busy, box_or_404, sync_subaccount

log = logging.getLogger(__name__)


def snapshot_json(lab: Lab, snapshot: BoxSnapshot) -> dict:
    """Return the API object of a snapshot."""
    size = snapshot["size"]
    return {
        "storage_box": snapshot["box"],
        "id": snapshot["id"],
        "name": snapshot["name"],
        "description": snapshot.get("description") or "",
        "labels": snapshot["labels"],
        "stats": {"size": size, "size_filesystem": size},
        "is_automatic": snapshot["is_automatic"],
        "created": snapshot["created"],
    }


def snapshot_or_404(lab: Lab, box: StorageBox, ref) -> BoxSnapshot:
    """Return a snapshot of the box by ID or name, or raise 404."""
    for s in lab.state[BOX_SNAPSHOTS].values():
        if s["box"] == box["id"] and str(ref) in (str(s["id"]), s["name"]):
            return s
    raise not_found("snapshot")


def new_snapshot(
    lab: Lab,
    box: StorageBox,
    automatic: bool,
    description: str = "",
    labels: dict | None = None,
) -> BoxSnapshot:
    """Create the record of a snapshot, named by the time.

    The copy follows later (copy_snapshot).
    """
    state = lab.state
    with state.change():
        snapshot_id = state.new_id(BOX_SNAPSHOTS)
        name = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H-%M-%S")
        while any(
            s["box"] == box["id"] and s["name"] == name
            for s in state[BOX_SNAPSHOTS].values()
        ):
            name += "-1"
        snapshot: BoxSnapshot = {
            "id": snapshot_id,
            "box": box["id"],
            "name": name,
            "description": description,
            "labels": labels or {},
            "is_automatic": automatic,
            "created": now(),
            "size": 0,
            "_copying": True,
        }
        state[BOX_SNAPSHOTS][snapshot_id] = snapshot
    return snapshot


async def copy_snapshot(lab: Lab, box: StorageBox, snapshot: BoxSnapshot) -> None:
    """Copy the box's data behind a snapshot record.

    If the copy fails, the record is removed again. If the record was deleted while
    the data was copied, the copy is removed too, and RecordGoneError fails the action.
    """
    fabric_box, name = lab.fabric.box, snapshot["name"]
    bid, snapshot_id = box["id"], snapshot["id"]
    try:
        await lab.blocking(fabric_box.take_snapshot, bid, name)
    except Exception:  # undo the partial change, then raise
        with lab.state.change():
            lab.state[BOX_SNAPSHOTS].pop(snapshot_id, None)
        raise
    # the snapshot was deleted in the meantime: leave no copy
    if lab.state.get(BOX_SNAPSHOTS, snapshot_id) is None:
        await lab.blocking(fabric_box.remove_snapshot, bid, name, check=False)
        raise RecordGoneError(
            f"the snapshot {snapshot_id} was deleted while it was taken"
        )
    size = await lab.blocking(fabric_box.snapshot_bytes, bid, name)
    with lab.state.change():
        copied = lab.current(BOX_SNAPSHOTS, snapshot_id)
        copied["size"] = size
        copied.pop("_copying", None)


async def take_automatic_snapshot(lab: Lab, box: StorageBox) -> BoxSnapshot:
    """Take a snapshot of the box for its plan, which keeps max_snapshots of them."""
    state = lab.state
    snapshot = new_snapshot(lab, box, automatic=True)
    await copy_snapshot(lab, box, snapshot)
    automatic = sorted(
        (
            s
            for s in state[BOX_SNAPSHOTS].values()
            if s["box"] == box["id"] and s["is_automatic"]
        ),
        key=lambda s: s["id"],
    )
    assert box["plan"] is not None  # called only for boxes with a plan
    for old in automatic[: -max(1, box["plan"]["max_snapshots"])]:
        await remove_snapshot(lab, box, old)
    return snapshot


async def remove_snapshot(lab: Lab, box: StorageBox, snapshot: BoxSnapshot) -> None:
    """Remove a snapshot's copy and its record."""
    await lab.blocking(lab.fabric.box.remove_snapshot, box["id"], snapshot["name"])
    with lab.state.change():
        del lab.state[BOX_SNAPSHOTS][snapshot["id"]]


def create_snapshot(lab: Lab, bid: int, body: dict) -> dict:
    """Take a snapshot by hand, within the snapshot limit of the box's type.

    The record exists at once; the action copies the data.
    """
    state = lab.state
    box = box_or_404(lab, bid, ready=True)
    manual = [
        s
        for s in state[BOX_SNAPSHOTS].values()
        if s["box"] == bid and not s["is_automatic"]
    ]
    if len(manual) >= lab.cfg["storage_box_types"][box["type"]]["snapshots"]:
        raise limit_exceeded(
            "snapshot limit of the Storage Box type reached", "snapshots"
        )
    snapshot = new_snapshot(
        lab, box, False, body.get("description") or "", body.get("labels")
    )
    action = lab.new_action(
        "create_snapshot",
        [(bid, "storage_box"), (snapshot["id"], "storage_box_snapshot")],
    )

    async def steps():
        await copy_snapshot(lab, box, snapshot)

    lab.run_action(action, steps)
    return {"snapshot": snapshot_json(lab, snapshot), "action": action}


def update_snapshot(lab: Lab, bid: int, ref: str, body: dict) -> dict:
    """Change a snapshot's description and labels."""
    snapshot = snapshot_or_404(lab, box_or_404(lab, bid), ref)
    with lab.state.change():
        update_fields(lab, snapshot, body, ("description", "labels"))
    return {"snapshot": snapshot_json(lab, snapshot)}


async def delete_snapshot(lab: Lab, bid: int, ref: str) -> dict:
    """Delete a snapshot at once; not while the box is busy."""
    box = box_or_404(lab, bid, ready=True)
    snapshot = snapshot_or_404(lab, box, ref)
    await remove_snapshot(lab, box, snapshot)
    return {
        "action": lab.finished_action(
            "delete_snapshot",
            [(bid, "storage_box"), (snapshot["id"], "storage_box_snapshot")],
        )
    }


async def rollback_snapshot(lab: Lab, box: StorageBox, body: dict) -> Plan:
    """Roll the box back to a snapshot (the box action rollback_snapshot).

    As on Hetzner, the data goes back to the snapshot, and newer snapshots are
    deleted. The box is busy while this runs (`_busy`), so no snapshot is taken,
    deleted or pruned.
    """
    state = lab.state
    snapshot = snapshot_or_404(lab, box, body.get("snapshot"))
    if snapshot.get("_copying"):
        raise locked("the snapshot is still being taken")

    bid = box["id"]

    async def steps():
        await lab.blocking(lab.fabric.box.roll_back, bid, snapshot["name"])
        # sub-account homes are paths: they follow what the snapshot has there
        subaccounts = [x for x in state[SUBACCOUNTS].values() if x["box"] == bid]
        for subaccount in subaccounts:
            try:
                # looked up again: the previous sub-account's sync awaited
                await sync_subaccount(
                    lab,
                    lab.current(STORAGE_BOXES, bid),
                    lab.current(SUBACCOUNTS, subaccount["id"]),
                )
            # a home the snapshot does not have stays as it was
            except Exception:
                log.exception(
                    "sub-account %s keeps its home after the rollback",
                    subaccount["username"],
                )
        newer_snapshots = [
            x
            for x in state[BOX_SNAPSHOTS].values()
            if x["box"] == bid and x["id"] > snapshot["id"]
        ]
        for newer in newer_snapshots:
            await remove_snapshot(lab, box, newer)

    return Plan(steps, resources=[(snapshot["id"], "storage_box_snapshot")])


# --- snapshot plans -------------------------------------------------------------------


def snapshot_plan_due(plan: dict, moment: datetime) -> bool:
    """Check whether a snapshot plan is due in the given minute."""
    return (
        moment.minute == plan["minute"]
        and moment.hour == plan["hour"]
        and (
            plan.get("day_of_week") is None
            or moment.isoweekday() % 7 == plan["day_of_week"] % 7
        )
        and (plan.get("day_of_month") is None or moment.day == plan["day_of_month"])
    )


async def snapshot_plan_scheduler(lab: Lab) -> None:
    """Take the automatic snapshots of all boxes with a snapshot plan.

    The plans are checked once a minute. Minutes that passed while snapshots ran (an
    hour at most) are checked too, so a slow snapshot of one box does not make another
    box miss its plan. A plan that is due while its box is busy waits until the box is
    free.
    """
    last_minute = datetime.now(timezone.utc).replace(second=0, microsecond=0)
    waiting: set[int] = set()
    while True:
        await asyncio.sleep(60 - datetime.now(timezone.utc).second)
        this_minute = datetime.now(timezone.utc).replace(second=0, microsecond=0)
        missed = min(60, int((this_minute - last_minute).total_seconds() // 60))
        minutes = [this_minute - timedelta(minutes=i) for i in range(missed)]
        last_minute = this_minute
        await run_snapshot_plans(lab, minutes, waiting)


async def run_snapshot_plans(
    lab: Lab, minutes: list[datetime], waiting: set[int]
) -> None:
    """Take the automatic snapshots whose plans are due in one of the minutes.

    `waiting` holds the IDs of boxes whose plan came due while a rollback or delete
    ran. Such a box gets one snapshot once it is free, however many times it missed.
    """
    boxes = list(lab.state[STORAGE_BOXES].values())
    # a box that is gone or lost its plan waits for nothing
    waiting.intersection_update(box["id"] for box in boxes if box.get("plan"))
    for box in boxes:
        plan = box.get("plan")
        if not plan:
            continue
        if box["id"] not in waiting and not any(
            snapshot_plan_due(plan, minute) for minute in minutes
        ):
            continue
        if box_busy(lab, box):
            waiting.add(box["id"])
            continue
        waiting.discard(box["id"])
        try:
            await take_automatic_snapshot(lab, box)
        # one failed snapshot must not end the plans of all boxes
        except Exception:
            log.exception("automatic snapshot of box %s failed", box["id"])


# --- routes ---------------------------------------------------------------------------


def add_routes(app: FastAPI, lab: Lab) -> None:
    """Add the routes of a box's snapshots to the app."""
    state = lab.state

    @app.get("/v1/storage_boxes/{bid}/snapshots")
    async def snapshot_list(bid: int, request: Request):
        box_or_404(lab, bid)
        items = filtered(
            request,
            [s for s in state[BOX_SNAPSHOTS].values() if s["box"] == bid],
            {
                "is_automatic": lambda snapshot, value: (
                    str(snapshot["is_automatic"]).lower() == value.lower()
                )
            },
        )
        return {
            "snapshots": sort_items(
                [snapshot_json(lab, s) for s in items],
                request.query_params.getlist("sort"),
            )
        }

    @app.post("/v1/storage_boxes/{bid}/snapshots", status_code=201)
    async def snapshot_create(bid: int, request: Request):
        body = await read_body(request)
        return create_snapshot(lab, bid, body)

    @app.get("/v1/storage_boxes/{bid}/snapshots/{ref}")
    async def snapshot_get(bid: int, ref: str):
        snapshot = snapshot_or_404(lab, box_or_404(lab, bid), ref)
        return {"snapshot": snapshot_json(lab, snapshot)}

    @app.put("/v1/storage_boxes/{bid}/snapshots/{ref}")
    async def snapshot_update(bid: int, ref: str, request: Request):
        body = await read_body(request)
        return update_snapshot(lab, bid, ref, body)

    @app.delete("/v1/storage_boxes/{bid}/snapshots/{ref}", status_code=201)
    async def snapshot_delete(bid: int, ref: str):
        return await delete_snapshot(lab, bid, ref)
