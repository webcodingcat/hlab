"""The API's rate limit: 429 with Hetzner's RateLimit headers."""

from fastapi.testclient import TestClient

from hlabd.api.app import create_app
from hlabd.api.web import RateLimit


def test_rate_limit_answers_429_with_its_headers(lab):
    lab.cfg["api"]["rate_limit"] = 3
    with TestClient(
        create_app(lab), headers={"Authorization": "Bearer secret"}
    ) as client:
        codes = [client.get("/v1/locations") for _ in range(4)]
    assert [r.status_code for r in codes] == [200, 200, 200, 429]
    assert codes[0].headers["RateLimit-Limit"] == "3"
    assert codes[2].headers["RateLimit-Remaining"] == "0"
    assert codes[3].json()["error"]["code"] == "rate_limit_exceeded"
    assert int(codes[3].headers["RateLimit-Reset"]) > 0


def test_rate_limit_buckets_stay_bounded():
    limit = RateLimit(3600)
    for i in range(5000):
        limit.take(f"token{i}")
    assert len(limit.buckets) <= 1000
